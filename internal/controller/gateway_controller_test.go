package controller

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func TestEnsureDownstreamGateway(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  uuid.NewUUID(),
		},
	}

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
			IPFamilies: []networkingv1alpha.IPFamily{
				networkingv1alpha.IPv4Protocol,
				networkingv1alpha.IPv6Protocol,
			},
		},
	}

	tests := []struct {
		name                      string
		upstreamGateway           *gatewayv1.Gateway
		existingUpstreamObjects   []client.Object
		existingDownstreamObjects []client.Object
		assert                    func(t *testing.T, upstreamGateway, downstreamGateway *gatewayv1.Gateway)
	}{
		{
			name: "http and https listeners",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {

			}),
			assert: func(t *testing.T, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				if assert.Len(t, downstreamGateway.Spec.Listeners, 2) {

					assert.Equal(t, gatewayv1.PortNumber(DefaultHTTPPort), downstreamGateway.Spec.Listeners[0].Port)
					assert.Equal(t, gatewayv1.HTTPProtocolType, downstreamGateway.Spec.Listeners[0].Protocol)

					assert.Equal(t, gatewayv1.PortNumber(DefaultHTTPSPort), downstreamGateway.Spec.Listeners[1].Port)
					assert.Equal(t, gatewayv1.HTTPSProtocolType, downstreamGateway.Spec.Listeners[1].Protocol)
				}

				assert.Len(t, upstreamGateway.Status.Addresses, 3)
			},
		},
		{
			name: "existing addresses are preserved",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Status.Addresses = []gatewayv1.GatewayStatusAddress{
					{
						Type:  ptr.To(gatewayv1.HostnameAddressType),
						Value: "existing-address-1.example.com",
					},
					{
						Type:  ptr.To(gatewayv1.HostnameAddressType),
						Value: "existing-address-2.example.com",
					},
				}
			}),
			assert: func(t *testing.T, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				if assert.Len(t, upstreamGateway.Status.Addresses, 2) {
					assert.Equal(t, "existing-address-1.example.com", upstreamGateway.Status.Addresses[0].Value)
					assert.Equal(t, "existing-address-2.example.com", upstreamGateway.Status.Addresses[1].Value)
				}
			},
		},
		{
			name: "hostname claimed by different gateway",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     "custom-hostname-0",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
					{
						Name:     "custom-hostname-1",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("test.example.com")),
					},
				}
			}),
			existingUpstreamObjects: []client.Object{
				newDomain(upstreamNamespace.Name, "example.com", func(d *networkingv1alpha.Domain) {
					apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
						Type:   networkingv1alpha.DomainConditionVerified,
						Status: metav1.ConditionTrue,
					})
				}),
			},
			existingDownstreamObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testConfig.Gateway.DownstreamHostnameAccountingNamespace,
						Name:      "example.com",
					},
					Data: map[string]string{
						"owner": "some/other/gateway",
					},
				},
			},
			assert: func(t *testing.T, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {

				currentListenerStatus := map[gatewayv1.SectionName]gatewayv1.ListenerStatus{}
				for _, listener := range upstreamGateway.Status.Listeners {
					currentListenerStatus[listener.Name] = *listener.DeepCopy()
				}

				for _, listener := range upstreamGateway.Spec.Listeners {
					if listener.Hostname == nil || *listener.Hostname != "example.com" {
						continue
					}

					listenerStatus := currentListenerStatus[listener.Name]

					acceptedCondition := apimeta.FindStatusCondition(listenerStatus.Conditions, string(gatewayv1.ListenerConditionAccepted))
					programmedCondition := apimeta.FindStatusCondition(listenerStatus.Conditions, string(gatewayv1.ListenerConditionProgrammed))

					if assert.NotNil(t, acceptedCondition, "did not find accepted condition on listener") {
						assert.Equal(t, metav1.ConditionFalse, acceptedCondition.Status)
						assert.Equal(t, networkingv1alpha.HostnameInUseReason, acceptedCondition.Reason)
					}

					if assert.NotNil(t, programmedCondition, "did not find programmed condition on listener") {
						assert.Equal(t, metav1.ConditionFalse, programmedCondition.Status)
						assert.Equal(t, networkingv1alpha.HostnameInUseReason, programmedCondition.Reason)
					}
				}
			},
		},
	}

	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			tt.existingUpstreamObjects = append(tt.existingUpstreamObjects, &gatewayv1.GatewayClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: gatewayv1.GatewayClassSpec{
					ControllerName: gatewayv1.GatewayController("test"),
				},
			})

			for _, obj := range append(tt.existingUpstreamObjects, tt.existingDownstreamObjects...) {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
			}

			fakeUpstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.upstreamGateway, upstreamNamespace).
				WithObjects(tt.existingUpstreamObjects...).
				WithStatusSubresource(tt.upstreamGateway).
				WithStatusSubresource(tt.existingUpstreamObjects...).
				Build()

			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.existingDownstreamObjects...).
				WithStatusSubresource(&gatewayv1.Gateway{}).
				WithStatusSubresource(tt.existingDownstreamObjects...).
				Build()

			ctx := context.Background()
			ctx = log.IntoContext(ctx, logger)

			mgr := &fakeMockManager{cl: fakeUpstreamClient}

			reconciler := &GatewayReconciler{
				mgr:               mgr,
				Config:            testConfig,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}

			downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, fakeDownstreamClient)

			reconciler.prepareUpstreamGateway(tt.upstreamGateway)
			result, downstreamGateway := reconciler.ensureDownstreamGateway(
				ctx,
				"test-suite",
				fakeUpstreamClient,
				tt.upstreamGateway,
				downstreamStrategy,
			)
			assert.NoError(t, result.Err, "failed ensuring downstream gateway HTTPRoutes")

			_, err := result.Complete(ctx)
			assert.NoError(t, err, "failed completing result")

			if tt.assert != nil {
				updatedUpstreamGateway := &gatewayv1.Gateway{}

				assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(tt.upstreamGateway), updatedUpstreamGateway))

				tt.assert(t, updatedUpstreamGateway, downstreamGateway)
			}
		})
	}
}

func TestEnsureDownstreamGatewayHTTPRoutes(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
		},
	}

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  uuid.NewUUID(),
		},
	}
	downstreamNamespaceName := fmt.Sprintf("ns-%s", upstreamNamespace.UID)

	tests := []struct {
		name                    string
		upstreamGateway         *gatewayv1.Gateway
		existingUpstreamObjects []client.Object
		assert                  func(t *testing.T, gateway *gatewayv1.Gateway)
	}{
		{
			name:            "no routes",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test"),
			assert: func(t *testing.T, gateway *gatewayv1.Gateway) {
				assert.Len(t, gateway.Status.Listeners, len(gateway.Spec.Listeners), "number of listeners in status does not match spec")

				for _, l := range gateway.Status.Listeners {
					assert.EqualValues(t, 0, l.AttachedRoutes, "on listener %q", l.Name)
				}
			},
		},
		{
			name:            "single route, all listeners",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test"),
			existingUpstreamObjects: []client.Object{
				newHTTPRoute(upstreamNamespace.Name, "route-1", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name: "test",
						},
					}
				}),
			},
			assert: func(t *testing.T, gateway *gatewayv1.Gateway) {
				assert.Len(t, gateway.Status.Listeners, len(gateway.Spec.Listeners), "number of listeners in status does not match spec")

				for _, l := range gateway.Status.Listeners {
					assert.EqualValues(t, 1, l.AttachedRoutes, "on listener %q", l.Name)
				}
			},
		},
		{
			name: "multiple routes, varied listener attachments",
			upstreamGateway: newGateway(testConfig,
				upstreamNamespace.Name,
				"test",
				func(g *gatewayv1.Gateway) {
					g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
						Name: gatewayv1.SectionName("custom"),
						Port: gatewayv1.PortNumber(80),
					})
				},
			),
			existingUpstreamObjects: []client.Object{
				newHTTPRoute(upstreamNamespace.Name, "route-all-listeners", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name: "test",
						},
					}
				}),
				newHTTPRoute(upstreamNamespace.Name, "route-all-listeners-by-section-name", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName(SchemeHTTP)),
						},
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName(SchemeHTTPS)),
						},
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName("custom")),
						},
					}
				}),
				newHTTPRoute(upstreamNamespace.Name, "route-missing-section-name", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName("does-not-exist")),
						},
					}
				}),
				newHTTPRoute(upstreamNamespace.Name, "route-http-listener-0", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName(SchemeHTTP)),
						},
					}
				}),
				newHTTPRoute(upstreamNamespace.Name, "route-http-listener-1", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName(SchemeHTTP)),
						},
					}
				}),
				newHTTPRoute(upstreamNamespace.Name, "route-https-listener-0", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName(SchemeHTTPS)),
						},
					}
				}),
				newHTTPRoute(upstreamNamespace.Name, "route-custom-listener-0", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name:        "test",
							SectionName: ptr.To(gatewayv1.SectionName("custom")),
						},
					}
				}),
			},
			assert: func(t *testing.T, gateway *gatewayv1.Gateway) {
				assert.Len(t, gateway.Status.Listeners, len(gateway.Spec.Listeners), "number of listeners in status does not match spec")

				for _, l := range gateway.Status.Listeners {
					switch l.Name {
					case SchemeHTTP:
						assert.EqualValues(t, 4, l.AttachedRoutes, "on http listener")
					case SchemeHTTPS:
						assert.EqualValues(t, 3, l.AttachedRoutes, "on https listener")
					case "custom":
						assert.EqualValues(t, 3, l.AttachedRoutes, "on custom listener")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			for _, obj := range tt.existingUpstreamObjects {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
			}

			fakeUpstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.upstreamGateway, upstreamNamespace).
				WithObjects(tt.existingUpstreamObjects...).
				WithStatusSubresource(tt.upstreamGateway).
				WithStatusSubresource(tt.existingUpstreamObjects...).
				Build()

			downstreamGateway := newGateway(testConfig, downstreamNamespaceName, tt.upstreamGateway.Name)

			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(downstreamGateway).
				WithStatusSubresource(downstreamGateway).
				Build()

			ctx := context.Background()

			mgr := &fakeMockManager{cl: fakeUpstreamClient}

			reconciler := &GatewayReconciler{
				mgr:               mgr,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}

			downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, fakeDownstreamClient)

			result := reconciler.ensureDownstreamGatewayHTTPRoutes(
				ctx,
				fakeUpstreamClient,
				tt.upstreamGateway,
				"test",
				downstreamGateway,
				downstreamStrategy,
				nil,
				nil,
			)
			assert.NoError(t, result.Err, "failed ensuring downstream gateway HTTPRoutes")

			_, err := result.Complete(ctx)
			assert.NoError(t, err, "failed completing result")

			if tt.assert != nil {
				updatedUpstreamGateway := &gatewayv1.Gateway{}

				assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(tt.upstreamGateway), updatedUpstreamGateway))

				tt.assert(t, updatedUpstreamGateway)
			}
		})
	}

}

func TestEnsureHostnamesClaimed(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  uuid.NewUUID(),
		},
	}
	downstreamNamespaceName := fmt.Sprintf("ns-%s", upstreamNamespace.UID)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
		},
	}

	tests := []struct {
		name                        string
		upstreamGateway             *gatewayv1.Gateway
		downstreamGateway           *gatewayv1.Gateway
		existingUpstreamObjects     []client.Object
		existingDownstreamObjects   []client.Object
		expectedVerifiedHostnames   []string
		expectedClaimedHostnames    []string
		expectedNotClaimedHostnames []string
		assert                      func(ctx context.Context, t *testing.T, cl client.Client, gateway *gatewayv1.Gateway)
	}{
		{
			name: "domains created for custom hostnames on HTTP listeners",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     "listener-1",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
					{
						Name:     "listener-2",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("test.example.com")),
					},
					{
						Name:     "listener-3",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("test.example")),
					},
				}
			}),
			expectedVerifiedHostnames: []string{},
			assert: func(ctx context.Context, t *testing.T, cl client.Client, gateway *gatewayv1.Gateway) {
				var domainList networkingv1alpha.DomainList
				if assert.NoError(t, cl.List(ctx, &domainList, client.InNamespace(gateway.Namespace)), "unexpected error while listing domains") {
					assert.Len(t, domainList.Items, 2)
					for _, expected := range []string{"example.com", "test.example"} {
						found := false
						for _, domain := range domainList.Items {
							if domain.Spec.DomainName == expected {
								found = true
								break
							}
						}
						assert.True(t, found, "did not find expected domain: %s", expected)
					}
				}
			},
		},
		{
			name: "domain exists but is not verified",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     gatewayv1.SectionName(SchemeHTTP),
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
				}
			}),
			existingUpstreamObjects: []client.Object{
				newDomain(upstreamNamespace.Name, "example.com"),
			},
			expectedVerifiedHostnames: []string{},
		},
		{
			name: "verified domain exists",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     "listener-1",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
					{
						Name:     "listener-2",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("test.example.com")),
					},
				}
			}),
			existingUpstreamObjects: []client.Object{
				newDomain(upstreamNamespace.Name, "example.com", func(d *networkingv1alpha.Domain) {
					apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
						Type:   networkingv1alpha.DomainConditionVerified,
						Status: metav1.ConditionTrue,
					})
				}),
			},
			expectedVerifiedHostnames: []string{"example.com", "test.example.com"},
			expectedClaimedHostnames:  []string{"example.com", "test.example.com"},
		},
		{
			name: "verified domain exists but hostname is claimed by different gateway",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     "listener-1",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
					{
						Name:     "listener-2",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("test.example.com")),
					},
				}
			}),
			existingUpstreamObjects: []client.Object{
				newDomain(upstreamNamespace.Name, "example.com", func(d *networkingv1alpha.Domain) {
					apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
						Type:   networkingv1alpha.DomainConditionVerified,
						Status: metav1.ConditionTrue,
					})
				}),
			},
			existingDownstreamObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testConfig.Gateway.DownstreamHostnameAccountingNamespace,
						Name:      "example.com",
					},
					Data: map[string]string{
						"owner": "some/other/gateway",
					},
				},
			},
			expectedVerifiedHostnames:   []string{"example.com", "test.example.com"},
			expectedClaimedHostnames:    []string{"test.example.com"},
			expectedNotClaimedHostnames: []string{"example.com"},
		},
		{
			name: "hostname verified by being programmed on downstream gateway",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     gatewayv1.SectionName(SchemeHTTP),
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
				}
			}),
			downstreamGateway: newGateway(testConfig, downstreamNamespaceName, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     gatewayv1.SectionName(SchemeHTTP),
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
				}
			}),
			expectedVerifiedHostnames: []string{"example.com"},
			expectedClaimedHostnames:  []string{"example.com"},
		},
		{
			name: "exact or subdomain match only",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     "listener-1",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
					{
						Name:     "listener-2",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("test.example.com")),
					},
					// The following hostname will not be considered verified, and a
					// Domain will be created for it.
					{
						Name:     "listener-3",
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("testexample.com")),
					},
				}
			}),
			existingUpstreamObjects: []client.Object{
				newDomain(upstreamNamespace.Name, "example.com", func(d *networkingv1alpha.Domain) {
					apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
						Type:   networkingv1alpha.DomainConditionVerified,
						Status: metav1.ConditionTrue,
					})
				}),
			},
			expectedVerifiedHostnames: []string{"example.com", "test.example.com"},
			expectedClaimedHostnames:  []string{"example.com", "test.example.com"},
			assert: func(ctx context.Context, t *testing.T, cl client.Client, gateway *gatewayv1.Gateway) {
				domainObjectKey := client.ObjectKey{
					Namespace: gateway.Namespace,
					Name:      "testexample.com",
				}
				assert.NoError(t, cl.Get(ctx, domainObjectKey, &networkingv1alpha.Domain{}), "expected to find a domain, but encountered an errro")
			},
		},
		{
			name: "hostname matches address",
			upstreamGateway: newGateway(testConfig, upstreamNamespace.Name, "test", func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = []gatewayv1.Listener{
					{
						Name:     gatewayv1.SectionName(SchemeHTTP),
						Port:     DefaultHTTPPort,
						Protocol: gatewayv1.HTTPProtocolType,
						Hostname: ptr.To(gatewayv1.Hostname("example.com")),
					},
				}
				g.Status.Addresses = []gatewayv1.GatewayStatusAddress{
					{
						Type:  ptr.To(gatewayv1.HostnameAddressType),
						Value: "example.com",
					},
				}
			}),
			expectedVerifiedHostnames: []string{"example.com"},
			expectedClaimedHostnames:  []string{"example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			for _, obj := range append(tt.existingUpstreamObjects, tt.existingDownstreamObjects...) {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
			}

			if tt.downstreamGateway == nil {
				tt.downstreamGateway = &gatewayv1.Gateway{}
			}

			fakeUpstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.upstreamGateway, upstreamNamespace).
				WithObjects(tt.existingUpstreamObjects...).
				WithStatusSubresource(tt.upstreamGateway).
				WithStatusSubresource(tt.existingUpstreamObjects...).
				Build()

			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.downstreamGateway).
				WithObjects(tt.existingDownstreamObjects...).
				WithStatusSubresource(&gatewayv1.Gateway{}).
				WithStatusSubresource(tt.existingDownstreamObjects...).
				Build()

			ctx := context.Background()

			mgr := &fakeMockManager{cl: fakeUpstreamClient}

			reconciler := &GatewayReconciler{
				mgr:               mgr,
				Config:            testConfig,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}

			verifiedHostnames, claimedHostnames, notClaimedHostnames, err := reconciler.ensureHostnamesClaimed(
				ctx,
				"test-suite",
				fakeUpstreamClient,
				tt.upstreamGateway,
				tt.downstreamGateway,
			)

			if assert.NoError(t, err, "unexpected error calling ensureHostnameVerification") {
				expectedVerifiedHostnames := append(
					tt.expectedVerifiedHostnames,
					testConfig.Gateway.GatewayDNSAddress(tt.upstreamGateway),
				)
				expectedClaimedHostnames := append(
					tt.expectedClaimedHostnames,
					testConfig.Gateway.GatewayDNSAddress(tt.upstreamGateway),
				)
				slices.Sort(expectedVerifiedHostnames)
				slices.Sort(expectedClaimedHostnames)
				assert.EqualValues(t, expectedVerifiedHostnames, verifiedHostnames, "expected verified hostnames mismatch")
				assert.EqualValues(t, expectedClaimedHostnames, claimedHostnames, "expected claimed hostnames mistmatch")
				assert.EqualValues(t, tt.expectedNotClaimedHostnames, notClaimedHostnames, "expected not claimed hostnames mismatch")
			}

			updatedUpstreamGateway := &gatewayv1.Gateway{}

			assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(tt.upstreamGateway), updatedUpstreamGateway))

			if tt.assert != nil {
				tt.assert(ctx, t, fakeUpstreamClient, updatedUpstreamGateway)
			}
		})
	}
}

func newGateway(
	testConfig config.NetworkServicesOperator,
	namespace,
	name string,
	opts ...func(*gatewayv1.Gateway),
) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uuid.NewUUID(),
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "test",
		},
	}

	gatewayutil.SetDefaultListeners(gw, testConfig.Gateway)

	for _, opt := range opts {
		opt(gw)
	}

	return gw
}

func newHTTPRoute(namespace, name string, opts ...func(*gatewayv1.HTTPRoute)) *gatewayv1.HTTPRoute {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uuid.NewUUID(),
		},
		Spec: gatewayv1.HTTPRouteSpec{},
	}

	for _, opt := range opts {
		opt(route)
	}

	return route
}
