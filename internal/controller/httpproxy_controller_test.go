package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

//nolint:gocyclo
func TestHTTPProxyCollectDesiredResources(t *testing.T) {

	operatorConfig := config.NetworkServicesOperator{
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test",
			GatewayTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test"),
			},
		},
	}

	tests := []struct {
		name      string
		httpProxy *networkingv1alpha.HTTPProxy
		assert    func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources)
	}{
		{
			name:      "existing URLRewrite filter in rule with hostname in endpoint",
			httpProxy: newHTTPProxy(),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute

				for ruleIndex, proxyRule := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					assert.Len(t, routeRule.Filters, len(proxyRule.Filters))
					assert.Equal(t, "www.example.com", string(ptr.Deref(routeRule.Filters[0].URLRewrite.Hostname, "")))
				}
			},
		},
		{
			name: "no URLRewrite filter in rule with hostname in endpoint",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				h.Spec.Rules[0].Filters = nil
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					if assert.Len(t, routeRule.Filters, 1) {
						urlRewriteFilter := routeRule.Filters[0]
						assert.Equal(t, gatewayv1.HTTPRouteFilterURLRewrite, urlRewriteFilter.Type)
						assert.Equal(t, "www.example.com", string(ptr.Deref(routeRule.Filters[0].URLRewrite.Hostname, "")))
					}
				}
			},
		},
		{
			name: "https scheme",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "https://www.example.com"
					}
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute
				endpointSlices := desiredResources.endpointSlices

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					for backendRefIndex := range routeRule.BackendRefs {
						backendRefIndexMsg := fmt.Sprintf("backendRef index %d", backendRefIndex)

						endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
						assert.Equal(t, SchemeHTTPS, ptr.Deref(endpointSlice.Ports[0].AppProtocol, ""), backendRefIndexMsg)
						assert.EqualValues(t, DefaultHTTPSPort, ptr.Deref(endpointSlice.Ports[0].Port, 0), backendRefIndexMsg)
					}
				}
			},
		},
		{
			name: "http scheme",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "http://www.example.com"
					}
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute
				endpointSlices := desiredResources.endpointSlices

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					for backendRefIndex := range routeRule.BackendRefs {
						backendRefIndexMsg := fmt.Sprintf("backendRef index %d", backendRefIndex)

						endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
						assert.Equal(t, SchemeHTTP, ptr.Deref(endpointSlice.Ports[0].AppProtocol, ""), backendRefIndexMsg)
						assert.EqualValues(t, DefaultHTTPPort, ptr.Deref(endpointSlice.Ports[0].Port, 0), backendRefIndexMsg)
					}
				}
			},
		},
		{
			name: "custom port",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "http://www.example.com:8080"
					}
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute
				endpointSlices := desiredResources.endpointSlices

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					for backendRefIndex := range routeRule.BackendRefs {
						backendRefIndexMsg := fmt.Sprintf("backendRef index %d", backendRefIndex)

						endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
						assert.EqualValues(t, 8080, ptr.Deref(endpointSlice.Ports[0].Port, 0), backendRefIndexMsg)
					}
				}
			},
		},
		{
			name: "IPv4 Address in host",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "http://127.0.0.1"
					}
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute
				endpointSlices := desiredResources.endpointSlices

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					for backendRefIndex := range routeRule.BackendRefs {
						backendRefIndexMsg := fmt.Sprintf("backendRef index %d", backendRefIndex)

						endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
						assert.Equal(t, discoveryv1.AddressTypeIPv4, endpointSlice.AddressType, backendRefIndexMsg)
						assert.Equal(t, "127.0.0.1", endpointSlice.Endpoints[0].Addresses[0])
					}
				}
			},
		},
		{
			name: "IPv6 Address in host",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "http://[::1]"
					}
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute
				endpointSlices := desiredResources.endpointSlices

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]
					for backendRefIndex := range routeRule.BackendRefs {
						backendRefIndexMsg := fmt.Sprintf("backendRef index %d", backendRefIndex)

						endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
						assert.Equal(t, discoveryv1.AddressTypeIPv6, endpointSlice.AddressType, backendRefIndexMsg)
						assert.Equal(t, "::1", endpointSlice.Endpoints[0].Addresses[0])
					}
				}
			},
		},
		{
			name: "custom hostnames",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				h.Spec.Hostnames = []gatewayv1.Hostname{
					gatewayv1.Hostname("test.example.com"),
					gatewayv1.Hostname("test2.example.com"),
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				gateway := desiredResources.gateway

				for i, hostname := range httpProxy.Spec.Hostnames {
					hostnameHTTPListenerFound := false
					hostnameHTTPSListenerFound := false

					for _, listener := range gateway.Spec.Listeners {
						if ptr.Deref(listener.Hostname, "") == hostname {
							switch listener.Protocol {
							case gatewayv1.HTTPProtocolType:
								hostnameHTTPListenerFound = true
							case gatewayv1.HTTPSProtocolType:
								hostnameHTTPSListenerFound = true
							}
						}
					}

					assert.True(t, hostnameHTTPListenerFound, "http listener not found for hostname %q at index %d", hostname, i)
					assert.True(t, hostnameHTTPSListenerFound, "https listener not found for hostname %q at index %d", hostname, i)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			reconciler := &HTTPProxyReconciler{Config: operatorConfig}
			desiredResources, err := reconciler.collectDesiredResources(tt.httpProxy)
			assert.NoError(t, err)

			gateway := desiredResources.gateway
			httpRoute := desiredResources.httpRoute
			endpointSlices := desiredResources.endpointSlices

			assert.NotNil(t, gateway)
			assert.NotNil(t, httpRoute)
			assert.Len(t, endpointSlices, 1)

			// Gateway assertions on items that are not hard coded
			assert.Equal(t, tt.httpProxy.Namespace, gateway.Namespace)
			assert.Equal(t, tt.httpProxy.Name, gateway.Name)
			assert.Equal(t, operatorConfig.HTTPProxy.GatewayClassName, gateway.Spec.GatewayClassName)

			assert.Len(t, gateway.Spec.Listeners, 2+(len(tt.httpProxy.Spec.Hostnames)*2))
			assert.Equal(t, operatorConfig.HTTPProxy.GatewayTLSOptions, gateway.Spec.Listeners[1].TLS.Options)

			// HTTPRoute assertions on items that are not hard coded
			assert.Equal(t, tt.httpProxy.Namespace, httpRoute.Namespace)
			assert.Equal(t, tt.httpProxy.Name, httpRoute.Name)
			assert.Len(t, httpRoute.Spec.ParentRefs, 1)
			assert.Equal(t, gateway.Name, string(httpRoute.Spec.ParentRefs[0].Name))
			assert.Len(t, httpRoute.Spec.Rules, len(tt.httpProxy.Spec.Rules))

			for ruleIndex, proxyRule := range tt.httpProxy.Spec.Rules {
				routeRule := httpRoute.Spec.Rules[ruleIndex]

				ruleIndexMsg := fmt.Sprintf("rule index %d", ruleIndex)
				assert.Len(t, routeRule.Matches, len(proxyRule.Matches), ruleIndexMsg)

				assert.Len(t, routeRule.BackendRefs, len(proxyRule.Backends), ruleIndexMsg)

				for backendRefIndex, backendRef := range routeRule.BackendRefs {
					backendRefIndexMsg := fmt.Sprintf("%s backendRef index %d", ruleIndexMsg, backendRefIndex)

					assert.Equal(t, "EndpointSlice", string(ptr.Deref(backendRef.Kind, "")), backendRefIndexMsg)

					endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
					assert.Equal(t, tt.httpProxy.Namespace, endpointSlice.Namespace, backendRefIndexMsg)
				}
			}

			if tt.assert != nil {
				tt.assert(t, tt.httpProxy, desiredResources)
			}
		})
	}
}

//nolint:gocyclo
func TestHTTPProxyReconcile(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test-gateway-class",
			GatewayTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test-issuer"),
			},
		},
		Gateway: config.GatewayConfig{
			TargetDomain: "example.com",
		},
	}

	type testContext struct {
		*testing.T
		reconciler *HTTPProxyReconciler
	}

	tests := []struct {
		name                    string
		httpProxy               *networkingv1alpha.HTTPProxy
		existingObjects         []client.Object
		postCreateGatewayStatus *gatewayv1.Gateway
		expectedError           bool
		expectedConditions      []metav1.Condition
		assert                  func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy)
	}{
		{
			name:          "basic reconcile - creates resources",
			httpProxy:     newHTTPProxy(),
			expectedError: false,
			expectedConditions: []metav1.Condition{
				{
					Type:   networkingv1alpha.HTTPProxyConditionAccepted,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
				{
					Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
			},
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				ctx := context.Background()

				objectKey := client.ObjectKeyFromObject(httpProxy)

				var gateway gatewayv1.Gateway
				err := cl.Get(ctx, objectKey, &gateway)
				assert.NoError(t, err)
				assert.Equal(t, testConfig.HTTPProxy.GatewayClassName, gateway.Spec.GatewayClassName)
				assert.Len(t, gateway.Spec.Listeners, 2)

				var httpRoute gatewayv1.HTTPRoute
				err = cl.Get(ctx, objectKey, &httpRoute)
				assert.NoError(t, err)
				assert.Len(t, httpRoute.Spec.Rules, 1)

				var endpointSliceList discoveryv1.EndpointSliceList
				err = cl.List(ctx, &endpointSliceList, client.InNamespace(httpProxy.Namespace))
				assert.NoError(t, err)
				assert.Len(t, endpointSliceList.Items, 1)
			},
		},
		{
			name:      "gateway conflict - already exists with different owner",
			httpProxy: newHTTPProxy(),
			existingObjects: []client.Object{
				&gatewayv1.Gateway{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "test",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "networking.datumapis.com/v1alpha",
								Kind:       "HTTPProxy",
								Name:       "other-proxy",
								UID:        uuid.NewUUID(),
								Controller: ptr.To(true),
							},
						},
					},
					Spec: gatewayv1.GatewaySpec{
						GatewayClassName: testConfig.HTTPProxy.GatewayClassName,
					},
				},
			},
			expectedError: false,
			expectedConditions: []metav1.Condition{
				{
					Type:   networkingv1alpha.HTTPProxyConditionAccepted,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
				{
					Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonConflict,
				},
			},
		},
		{
			name:      "httproute conflict - already exists with different owner",
			httpProxy: newHTTPProxy(),
			existingObjects: []client.Object{
				&gatewayv1.HTTPRoute{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "test",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "networking.datumapis.com/v1alpha",
								Kind:       "HTTPProxy",
								Name:       "other-proxy",
								UID:        uuid.NewUUID(),
								Controller: ptr.To(true),
							},
						},
					},
					Spec: gatewayv1.HTTPRouteSpec{},
				},
			},
			expectedError: false,
			expectedConditions: []metav1.Condition{
				{
					Type:   networkingv1alpha.HTTPProxyConditionAccepted,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
				{
					Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonConflict,
				},
			},
		},
		{
			name:      "endpointslice conflict - already exists with different owner",
			httpProxy: newHTTPProxy(),
			existingObjects: []client.Object{
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-0-0",
						Namespace: "test",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "networking.datumapis.com/v1alpha",
								Kind:       "HTTPProxy",
								Name:       "other-proxy",
								UID:        uuid.NewUUID(),
								Controller: ptr.To(true),
							},
						},
					},
					AddressType: discoveryv1.AddressTypeFQDN,
				},
			},
			expectedError: false,
			expectedConditions: []metav1.Condition{
				{
					Type:   networkingv1alpha.HTTPProxyConditionAccepted,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
				{
					Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonConflict,
				},
			},
		},
		{
			name:      "address and hostname propagation",
			httpProxy: newHTTPProxy(),
			postCreateGatewayStatus: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Addresses: []gatewayv1.GatewayStatusAddress{
						{
							Type:  ptr.To(gatewayv1.HostnameAddressType),
							Value: "test.example.com",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(gatewayv1.GatewayConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gatewayv1.GatewayReasonAccepted),
						},
						{
							Type:   string(gatewayv1.GatewayConditionProgrammed),
							Status: metav1.ConditionTrue,
							Reason: string(gatewayv1.GatewayReasonProgrammed),
						},
					},
				},
			},
			expectedError: false,
			expectedConditions: []metav1.Condition{
				{
					Type:   networkingv1alpha.HTTPProxyConditionAccepted,
					Status: metav1.ConditionTrue,
					Reason: networkingv1alpha.HTTPProxyReasonAccepted,
				},
				{
					Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
					Status: metav1.ConditionTrue,
					Reason: networkingv1alpha.HTTPProxyReasonProgrammed,
				},
			},
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				if assert.Len(t, httpProxy.Status.Addresses, 1) {
					assert.Equal(t, "test.example.com", httpProxy.Status.Addresses[0].Value)
				}

				if assert.Len(t, httpProxy.Status.Hostnames, 1) {
					assert.Equal(t, gatewayv1.Hostname("test.example.com"), httpProxy.Status.Hostnames[0])
				}
			},
		},
		{
			name: "custom hostnames programmed",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				h.Spec.Hostnames = []gatewayv1.Hostname{"example.com"}
			}),
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				ctx := context.Background()

				objectKey := client.ObjectKeyFromObject(httpProxy)

				var gateway gatewayv1.Gateway
				err := cl.Get(ctx, objectKey, &gateway)
				assert.NoError(t, err)

				// Assert that an HTTP and HTTPS listener were programmed
				for _, hostname := range httpProxy.Spec.Hostnames {
					for _, protocol := range []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType} {
						found := false
						for _, listener := range gateway.Spec.Listeners {
							if listener.Protocol == protocol && ptr.Equal(listener.Hostname, &hostname) {
								found = true
								break
							}
						}
						if !found {
							assert.Fail(t, "did not find listener for hostname %q in protocol %q", hostname, protocol)
						}
					}
				}

				hostnamesReadyCondition := apimeta.FindStatusCondition(httpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesReady)
				if assert.NotNil(t, hostnamesReadyCondition, "did not find HostnamesReady condition on HTTPProxy") {
					assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, hostnamesReadyCondition.Reason)
				}

			},
		},
		{
			name: "custom hostnames unverified",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				h.Spec.Hostnames = []gatewayv1.Hostname{"example.com"}
			}),
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				ctx := context.Background()

				objectKey := client.ObjectKeyFromObject(httpProxy)

				var gateway gatewayv1.Gateway
				if assert.NoError(t, cl.Get(ctx, objectKey, &gateway), "unexpected error fetching gateway") {
					gateway.Status.Listeners = make([]gatewayv1.ListenerStatus, len(gateway.Spec.Listeners))
					for listenerIndex, listener := range gateway.Spec.Listeners {
						listenerStatus := gatewayv1.ListenerStatus{
							Name: listener.Name,
						}

						apimeta.SetStatusCondition(&listenerStatus.Conditions, metav1.Condition{
							Type:   string(gatewayv1.ListenerConditionAccepted),
							Status: metav1.ConditionFalse,
							Reason: networkingv1alpha.UnverifiedHostnamesPresent,
						})

						gateway.Status.Listeners[listenerIndex] = listenerStatus
					}

					if assert.NoError(t, cl.Status().Update(ctx, &gateway), "unexpected error updating gateway status") {
						// Run reconcile again to get the HTTPProxy status updated
						req := mcreconcile.Request{
							Request: reconcile.Request{
								NamespacedName: objectKey,
							},
							ClusterName: "test-cluster",
						}
						_, err := t.reconciler.Reconcile(ctx, req)
						if assert.NoError(t, err, "unexpected error reconciling HTTPProxy") {

							updatedHttpProxy := &networkingv1alpha.HTTPProxy{}

							if assert.NoError(t, cl.Get(ctx, objectKey, updatedHttpProxy), "error fetching HTTPProxy") {

								hostnamesReadyCondition := apimeta.FindStatusCondition(updatedHttpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesReady)
								if assert.NotNil(t, hostnamesReadyCondition, "did not find HostnamesReady condition on HTTPProxy") {
									assert.Equal(t, networkingv1alpha.UnverifiedHostnamesPresent, hostnamesReadyCondition.Reason)
								}
							}
						}
					}
				}
			},
		},
		{
			name: "custom hostnames verified",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				h.Spec.Hostnames = []gatewayv1.Hostname{"example.com"}
			}),
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				ctx := context.Background()

				objectKey := client.ObjectKeyFromObject(httpProxy)

				var gateway gatewayv1.Gateway
				if assert.NoError(t, cl.Get(ctx, objectKey, &gateway), "unexpected error fetching gateway") {
					gateway.Status.Listeners = make([]gatewayv1.ListenerStatus, len(gateway.Spec.Listeners))
					for listenerIndex, listener := range gateway.Spec.Listeners {
						listenerStatus := gatewayv1.ListenerStatus{
							Name: listener.Name,
						}

						apimeta.SetStatusCondition(&listenerStatus.Conditions, metav1.Condition{
							Type:   string(gatewayv1.ListenerConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gatewayv1.ListenerReasonAccepted),
						})

						gateway.Status.Listeners[listenerIndex] = listenerStatus
					}

					if assert.NoError(t, cl.Status().Update(ctx, &gateway), "unexpected error updating gateway status") {
						// Run reconcile again to get the HTTPProxy status updated
						req := mcreconcile.Request{
							Request: reconcile.Request{
								NamespacedName: objectKey,
							},
							ClusterName: "test-cluster",
						}
						_, err := t.reconciler.Reconcile(ctx, req)
						if assert.NoError(t, err, "unexpected error reconciling HTTPProxy") {

							updatedHttpProxy := &networkingv1alpha.HTTPProxy{}

							if assert.NoError(t, cl.Get(ctx, objectKey, updatedHttpProxy), "error fetching HTTPProxy") {

								hostnamesReadyCondition := apimeta.FindStatusCondition(updatedHttpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesReady)
								if assert.NotNil(t, hostnamesReadyCondition, "did not find HostnamesReady condition on HTTPProxy") {
									assert.Equal(t, networkingv1alpha.HTTPProxyReasonHostnamesAccepted, hostnamesReadyCondition.Reason)
								}
							}
						}
					}
				}
			},
		},
	}

	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var initialObjects []client.Object

			for _, obj := range tt.existingObjects {
				obj.SetCreationTimestamp(metav1.Now())
			}

			initialObjects = append(initialObjects, tt.httpProxy)
			initialObjects = append(initialObjects, tt.existingObjects...)

			fakeClientBuilder := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(initialObjects...).
				WithStatusSubresource(initialObjects...).
				WithStatusSubresource(&gatewayv1.Gateway{})

			if tt.postCreateGatewayStatus != nil {
				fakeClientBuilder.WithStatusSubresource(tt.postCreateGatewayStatus)
			}

			fakeClient := fakeClientBuilder.Build()
			mgr := &fakeMockManager{cl: fakeClient}

			reconciler := &HTTPProxyReconciler{
				mgr:    mgr,
				Config: testConfig,
			}

			req := mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(tt.httpProxy),
				},
				ClusterName: "test-cluster",
			}

			ctx := context.Background()
			ctx = log.IntoContext(ctx, logger)

			_, err := reconciler.Reconcile(ctx, req)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			var updatedProxy networkingv1alpha.HTTPProxy
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.httpProxy), &updatedProxy)
			assert.NoError(t, err)

			if tt.postCreateGatewayStatus != nil {
				var gateway gatewayv1.Gateway
				err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.httpProxy), &gateway)
				if assert.NoError(t, err) {
					gateway.Status = tt.postCreateGatewayStatus.Status
					err = fakeClient.Status().Update(ctx, &gateway)
					assert.NoError(t, err)

					_, err = reconciler.Reconcile(ctx, req)
					if tt.expectedError {
						assert.Error(t, err)
					} else {
						assert.NoError(t, err)
					}

					err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.httpProxy), &updatedProxy)
					assert.NoError(t, err)
				}
			}

			for _, expectedCondition := range tt.expectedConditions {
				actualCondition := apimeta.FindStatusCondition(updatedProxy.Status.Conditions, expectedCondition.Type)
				assert.NotNil(t, actualCondition, "Expected condition %s not found", expectedCondition.Type)
				if actualCondition != nil {
					assert.Equal(t, expectedCondition.Status, actualCondition.Status, "Condition %s status mismatch", expectedCondition.Type)
					assert.Equal(t, expectedCondition.Reason, actualCondition.Reason, "Condition %s reason mismatch", expectedCondition.Type)
				}
			}

			if tt.assert != nil {
				tt.assert(&testContext{T: t, reconciler: reconciler}, fakeClient, &updatedProxy)
			}

		})
	}
}

func newHTTPProxy(opts ...func(*networkingv1alpha.HTTPProxy)) *networkingv1alpha.HTTPProxy {
	p := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "test",
			Name:              "test",
			CreationTimestamp: metav1.Now(),
			UID:               uuid.NewUUID(),
		},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
								Value: ptr.To("/test"),
							},
						},
					},
					Filters: []gatewayv1.HTTPRouteFilter{
						{
							Type: gatewayv1.HTTPRouteFilterURLRewrite,
							URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
								Path: &gatewayv1.HTTPPathModifier{
									Type:               gatewayv1.PrefixMatchHTTPPathModifier,
									ReplacePrefixMatch: ptr.To("/test"),
								},
							},
						},
					},
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint: "http://www.example.com",
							Filters: []gatewayv1.HTTPRouteFilter{
								{
									Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
										Set: []gatewayv1.HTTPHeader{
											{
												Name:  gatewayv1.HTTPHeaderName("x-test"),
												Value: "test",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

type fakeMockManager struct {
	mcmanager.Manager
	cl client.Client
}

func (m *fakeMockManager) GetCluster(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	return &fakeCluster{cl: m.cl}, nil
}

type fakeCluster struct {
	cluster.Cluster
	cl client.Client
}

func (c *fakeCluster) GetClient() client.Client {
	return c.cl
}

func (c *fakeCluster) GetScheme() *runtime.Scheme {
	return c.cl.Scheme()
}
