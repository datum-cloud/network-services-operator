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

func TestHTTPPRoxyCollectDesiredResources(t *testing.T) {
	httpProxy := newHTTPProxy()

	operatorConfig := config.NetworkServicesOperator{
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test",
			GatewayTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test"),
			},
		},
	}

	reconciler := &HTTPProxyReconciler{Config: operatorConfig}
	desiredResources, err := reconciler.collectDesiredResources(httpProxy)
	assert.NoError(t, err)

	gateway := desiredResources.gateway
	httpRoute := desiredResources.httpRoute
	endpointSlices := desiredResources.endpointSlices

	assert.NotNil(t, gateway)
	assert.NotNil(t, httpRoute)
	assert.Len(t, endpointSlices, 2)

	// Gateway assertions on items that are not hard coded
	assert.Equal(t, httpProxy.Namespace, gateway.Namespace)
	assert.Equal(t, httpProxy.Name, gateway.Name)
	assert.Equal(t, operatorConfig.HTTPProxy.GatewayClassName, gateway.Spec.GatewayClassName)

	assert.Len(t, gateway.Spec.Listeners, 2)
	assert.Equal(t, operatorConfig.HTTPProxy.GatewayTLSOptions, gateway.Spec.Listeners[1].TLS.Options)

	// HTTPRoute assertions on items that are not hard coded
	assert.Equal(t, httpProxy.Namespace, httpRoute.Namespace)
	assert.Equal(t, httpProxy.Name, httpRoute.Name)
	assert.Len(t, httpRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gateway.Name, string(httpRoute.Spec.ParentRefs[0].Name))
	assert.Len(t, httpRoute.Spec.Rules, len(httpProxy.Spec.Rules))

	for ruleIndex, proxyRule := range httpProxy.Spec.Rules {
		routeRule := httpRoute.Spec.Rules[ruleIndex]

		ruleIndexMsg := fmt.Sprintf("rule index %d", ruleIndex)
		assert.Len(t, routeRule.Matches, len(proxyRule.Matches), ruleIndexMsg)
		assert.Len(t, routeRule.Filters, len(proxyRule.Filters), ruleIndexMsg)
		assert.Len(t, routeRule.BackendRefs, len(proxyRule.Backends), ruleIndexMsg)

		assert.Equal(t, "www.example.com", string(ptr.Deref(routeRule.Filters[0].URLRewrite.Hostname, "")))

		for backendRefIndex, backendRef := range routeRule.BackendRefs {
			backendRefIndexMsg := fmt.Sprintf("%s backendRef index %d", ruleIndexMsg, backendRefIndex)

			assert.Equal(t, "EndpointSlice", string(ptr.Deref(backendRef.Kind, "")), backendRefIndexMsg)

			endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
			assert.Equal(t, httpProxy.Namespace, endpointSlice.Namespace, backendRefIndexMsg)

			switch ruleIndex {
			case 0:
				assert.Equal(t, "http", ptr.Deref(endpointSlice.Ports[0].AppProtocol, ""))
				assert.Equal(t, 80, int(ptr.Deref(endpointSlice.Ports[0].Port, 0)))
				assert.Equal(t, 80, int(ptr.Deref(backendRef.Port, 0)))
			case 1:
				assert.Equal(t, "https", ptr.Deref(endpointSlice.Ports[0].AppProtocol, ""))
				assert.Equal(t, 8443, int(ptr.Deref(endpointSlice.Ports[0].Port, 0)))
				assert.Equal(t, 8443, int(ptr.Deref(backendRef.Port, 0)))
			}
		}
	}

}

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

	tests := []struct {
		name                    string
		httpProxy               *networkingv1alpha.HTTPProxy
		existingObjects         []client.Object
		postCreateGatewayStatus *gatewayv1.Gateway
		expectedError           bool
		expectedConditions      []metav1.Condition
		assert                  func(t *testing.T, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy)
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
			assert: func(t *testing.T, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
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
				assert.Len(t, httpRoute.Spec.Rules, 2)

				var endpointSliceList discoveryv1.EndpointSliceList
				err = cl.List(ctx, &endpointSliceList, client.InNamespace(httpProxy.Namespace))
				assert.NoError(t, err)
				assert.Len(t, endpointSliceList.Items, 2)
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
			assert: func(t *testing.T, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				if assert.Len(t, httpProxy.Status.Addresses, 1) {
					assert.Equal(t, "test.example.com", httpProxy.Status.Addresses[0].Value)
				}

				if assert.Len(t, httpProxy.Status.Hostnames, 1) {
					assert.Equal(t, gatewayv1.Hostname("test.example.com"), httpProxy.Status.Hostnames[0])
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
				WithStatusSubresource(initialObjects...)

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
				tt.assert(t, fakeClient, &updatedProxy)
			}

		})
	}
}

func newHTTPProxy() *networkingv1alpha.HTTPProxy {
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
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
								Value: ptr.To("/test2"),
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
							Endpoint: "https://www.example.com:8443",
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
