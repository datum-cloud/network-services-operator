package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

const routeConfigurationTypeURL = "type.googleapis.com/envoy.config.route.v3.RouteConfiguration"

//nolint:gocyclo
func TestHTTPProxyCollectDesiredResources(t *testing.T) {

	operatorConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain: "example.com",
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test"),
			},
		},
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test",
		},
	}

	tests := []struct {
		name        string
		httpProxy   *networkingv1alpha.HTTPProxy
		expectError string
		assert      func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources)
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
			name: "HTTPS with IPv4 address without tls.hostname returns error",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "https://192.168.1.1"
					}
				}
			}),
			expectError: "HTTPS endpoint with IP address requires tls.hostname",
		},
		{
			name: "HTTPS with IPv4 address and tls.hostname",
			httpProxy: newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
				for ruleIndex, proxyRule := range h.Spec.Rules {
					for backendIndex := range proxyRule.Backends {
						h.Spec.Rules[ruleIndex].Backends[backendIndex].Endpoint = "https://192.168.1.1"
						h.Spec.Rules[ruleIndex].Backends[backendIndex].TLS = &networkingv1alpha.HTTPProxyBackendTLS{
							Hostname: ptr.To("api.example.com"),
						}
					}
				}
			}),
			assert: func(t *testing.T, httpProxy *networkingv1alpha.HTTPProxy, desiredResources *desiredHTTPProxyResources) {
				httpRoute := desiredResources.httpRoute
				endpointSlices := desiredResources.endpointSlices

				for ruleIndex := range httpProxy.Spec.Rules {
					routeRule := httpRoute.Spec.Rules[ruleIndex]

					// Verify URLRewrite filter has the TLS hostname
					if assert.Len(t, routeRule.Filters, 1) {
						urlRewriteFilter := routeRule.Filters[0]
						assert.Equal(t, gatewayv1.HTTPRouteFilterURLRewrite, urlRewriteFilter.Type)
						assert.Equal(t, "api.example.com", string(ptr.Deref(urlRewriteFilter.URLRewrite.Hostname, "")))
					}

					for backendRefIndex := range routeRule.BackendRefs {
						backendRefIndexMsg := fmt.Sprintf("backendRef index %d", backendRefIndex)

						endpointSlice := endpointSlices[ruleIndex+backendRefIndex]
						assert.Equal(t, discoveryv1.AddressTypeIPv4, endpointSlice.AddressType, backendRefIndexMsg)
						assert.Equal(t, "192.168.1.1", endpointSlice.Endpoints[0].Addresses[0])
						assert.Equal(t, "https", *endpointSlice.Ports[0].AppProtocol, backendRefIndexMsg)
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
			cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			desiredResources, err := reconciler.collectDesiredResources(context.Background(), cl, tt.httpProxy)

			if tt.expectError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectError)
				return
			}
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
			assert.Equal(t, operatorConfig.Gateway.ListenerTLSOptions, gateway.Spec.Listeners[1].TLS.Options)

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
	assert.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test-gateway-class",
		},
		Gateway: config.GatewayConfig{
			ControllerName:             gatewayv1.GatewayController("test-gateway-class"),
			DownstreamGatewayClassName: "test-downstream-gateway-class",
			TargetDomain:               "example.com",
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test-issuer"),
			},
		},
	}

	type testContext struct {
		*testing.T
		reconciler       *HTTPProxyReconciler
		gateway          *gatewayv1.Gateway
		downstreamClient client.Client
	}

	connectorNamespaceUID := types.UID("11111111-1111-1111-1111-111111111111")
	connectorDownstreamNamespace := fmt.Sprintf("ns-%s", connectorNamespaceUID)
	connectorHTTPProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		h.Spec.Rules[0].Backends[0].Connector = &networkingv1alpha.ConnectorReference{
			Name: "connector-1",
		}
	})
	connectorClearedHTTPProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		h.Spec.Rules[0].Backends[0].Connector = &networkingv1alpha.ConnectorReference{
			Name: "connector-1",
		}
	})

	connectorDownstreamObjects := func(proxy *networkingv1alpha.HTTPProxy) []client.Object {
		return []client.Object{
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: connectorDownstreamNamespace}},
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("anchor-%s", proxy.UID),
				Namespace:         connectorDownstreamNamespace,
				CreationTimestamp: metav1.Now(),
			}},
		}
	}

	tests := []struct {
		name                    string
		httpProxy               *networkingv1alpha.HTTPProxy
		existingObjects         []client.Object
		downstreamObjects       []client.Object
		namespaceUID            string
		postCreateGatewayStatus func(*gatewayv1.Gateway)
		expectedError           bool
		expectedConditions      []metav1.Condition
		assert                  func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy)
	}{
		{
			name:              "connector backend creates envoy patch policy",
			httpProxy:         connectorHTTPProxy,
			downstreamObjects: connectorDownstreamObjects(connectorHTTPProxy),
			namespaceUID:      string(connectorNamespaceUID),
			existingObjects: []client.Object{
				&networkingv1alpha1.Connector{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "connector-1",
						Namespace: "test",
					},
					Status: networkingv1alpha1.ConnectorStatus{
						Conditions: []metav1.Condition{
							{
								Type:   networkingv1alpha1.ConnectorConditionReady,
								Status: metav1.ConditionTrue,
								Reason: networkingv1alpha1.ConnectorReasonReady,
							},
						},
						ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
							Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
							PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
								Id:            "node-123",
								DiscoveryMode: networkingv1alpha1.DNSPublicKeyDiscoveryMode,
								HomeRelay:     "https://relay.example.test",
								Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{
									{
										Address: "127.0.0.1",
										Port:    80,
									},
								},
							},
						},
					},
				},
			},
			postCreateGatewayStatus: func(g *gatewayv1.Gateway) {
				setGatewayProgrammedWithDefaultHTTPSListener(g)
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
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
			},
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				var patchList envoygatewayv1alpha1.EnvoyPatchPolicyList
				err := t.downstreamClient.List(context.Background(), &patchList)
				assert.NoError(t, err)
				assert.Len(t, patchList.Items, 1)
				assert.Equal(t, fmt.Sprintf("connector-%s", httpProxy.Name), patchList.Items[0].Name)
			},
		},
		{
			name:              "connector backend waits for default-https listener programmed",
			httpProxy:         connectorHTTPProxy,
			downstreamObjects: connectorDownstreamObjects(connectorHTTPProxy),
			namespaceUID:      string(connectorNamespaceUID),
			existingObjects: []client.Object{
				&networkingv1alpha1.Connector{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "connector-1",
						Namespace: "test",
					},
					Status: networkingv1alpha1.ConnectorStatus{
						Conditions: []metav1.Condition{
							{
								Type:   networkingv1alpha1.ConnectorConditionReady,
								Status: metav1.ConditionTrue,
								Reason: networkingv1alpha1.ConnectorReasonReady,
							},
						},
						ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
							Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
							PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
								Id:            "node-123",
								DiscoveryMode: networkingv1alpha1.DNSPublicKeyDiscoveryMode,
								HomeRelay:     "https://relay.example.test",
								Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{
									{
										Address: "127.0.0.1",
										Port:    80,
									},
								},
							},
						},
					},
				},
			},
			postCreateGatewayStatus: func(g *gatewayv1.Gateway) {
				// Gateway-level Programmed is not enough without default-https listener Programmed.
				apimeta.SetStatusCondition(&g.Status.Conditions, metav1.Condition{
					Type:               string(gatewayv1.GatewayConditionProgrammed),
					Status:             metav1.ConditionTrue,
					ObservedGeneration: g.Generation,
					Reason:             string(gatewayv1.GatewayReasonProgrammed),
				})
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
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
			},
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				var patchList envoygatewayv1alpha1.EnvoyPatchPolicyList
				err := t.downstreamClient.List(context.Background(), &patchList)
				assert.NoError(t, err)
				assert.Len(t, patchList.Items, 0)
			},
		},
		{
			name:              "connector not ready uses direct response",
			httpProxy:         connectorHTTPProxy,
			downstreamObjects: connectorDownstreamObjects(connectorHTTPProxy),
			namespaceUID:      string(connectorNamespaceUID),
			existingObjects: []client.Object{
				&networkingv1alpha1.Connector{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "connector-1",
						Namespace: "test",
					},
					Status: networkingv1alpha1.ConnectorStatus{
						Conditions: []metav1.Condition{
							{
								Type:   networkingv1alpha1.ConnectorConditionReady,
								Status: metav1.ConditionFalse,
								Reason: networkingv1alpha1.ConnectorReasonNotReady,
							},
						},
					},
				},
			},
			postCreateGatewayStatus: func(g *gatewayv1.Gateway) {
				setGatewayProgrammedWithDefaultHTTPSListener(g)
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
				ctx := context.Background()

				var patchList envoygatewayv1alpha1.EnvoyPatchPolicyList
				err := t.downstreamClient.List(ctx, &patchList)
				assert.NoError(t, err)
				assert.Len(t, patchList.Items, 0)

				httpRouteFilter := &envoygatewayv1alpha1.HTTPRouteFilter{}
				filterKey := client.ObjectKey{Namespace: httpProxy.Namespace, Name: connectorOfflineFilterName(httpProxy)}
				assert.NoError(t, cl.Get(ctx, filterKey, httpRouteFilter))
				assert.Equal(t, "Tunnel not online", ptr.Deref(httpRouteFilter.Spec.DirectResponse.Body.Inline, ""))

				httpRoute := &gatewayv1.HTTPRoute{}
				assert.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(httpProxy), httpRoute))
				if assert.Len(t, httpRoute.Spec.Rules, 1) {
					assert.Empty(t, httpRoute.Spec.Rules[0].BackendRefs)
					found := false
					for _, filter := range httpRoute.Spec.Rules[0].Filters {
						if filter.Type == gatewayv1.HTTPRouteFilterExtensionRef &&
							filter.ExtensionRef != nil &&
							filter.ExtensionRef.Kind == envoygatewayv1alpha1.KindHTTPRouteFilter &&
							filter.ExtensionRef.Name == gatewayv1.ObjectName(httpRouteFilter.Name) {
							found = true
							break
						}
					}
					assert.True(t, found, "expected HTTPRouteFilter extension ref on rule")
				}
			},
		},
		{
			name:              "connector patch policy removed when connector cleared",
			httpProxy:         connectorClearedHTTPProxy,
			downstreamObjects: connectorDownstreamObjects(connectorClearedHTTPProxy),
			namespaceUID:      string(connectorNamespaceUID),
			existingObjects: []client.Object{
				&networkingv1alpha1.Connector{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "connector-1",
						Namespace: "test",
					},
					Status: networkingv1alpha1.ConnectorStatus{
						Conditions: []metav1.Condition{
							{
								Type:   networkingv1alpha1.ConnectorConditionReady,
								Status: metav1.ConditionTrue,
								Reason: networkingv1alpha1.ConnectorReasonReady,
							},
						},
						ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
							Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
							PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
								Id:            "node-123",
								DiscoveryMode: networkingv1alpha1.DNSPublicKeyDiscoveryMode,
								HomeRelay:     "https://relay.example.test",
								Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{
									{
										Address: "127.0.0.1",
										Port:    80,
									},
								},
							},
						},
					},
				},
			},
			postCreateGatewayStatus: func(g *gatewayv1.Gateway) {
				setGatewayProgrammedWithDefaultHTTPSListener(g)
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
					Status: metav1.ConditionFalse,
					Reason: networkingv1alpha.HTTPProxyReasonPending,
				},
			},
			assert: func(t *testContext, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) {
				ctx := context.Background()

				var patchList envoygatewayv1alpha1.EnvoyPatchPolicyList
				err := t.downstreamClient.List(ctx, &patchList)
				assert.NoError(t, err)
				assert.Len(t, patchList.Items, 1)

				updatedProxy := &networkingv1alpha.HTTPProxy{}
				assert.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(httpProxy), updatedProxy))
				updatedProxy.Spec.Rules[0].Backends[0].Connector = nil
				assert.NoError(t, cl.Update(ctx, updatedProxy))

				req := mcreconcile.Request{
					Request: reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(httpProxy),
					},
					ClusterName: "test-cluster",
				}
				for i := 0; i < 2; i++ {
					_, err = t.reconciler.Reconcile(ctx, req)
					assert.NoError(t, err)
				}

				patchList = envoygatewayv1alpha1.EnvoyPatchPolicyList{}
				err = t.downstreamClient.List(ctx, &patchList)
				assert.NoError(t, err)
				assert.Len(t, patchList.Items, 0)
			},
		},
		{
			name:          "basic reconcile - creates resources",
			httpProxy:     newHTTPProxy(),
			expectedError: false,
			expectedConditions: []metav1.Condition{
				{
					Type:   networkingv1alpha.HTTPProxyConditionAccepted,
					Status: metav1.ConditionTrue,
					Reason: networkingv1alpha.HTTPProxyReasonAccepted,
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
			postCreateGatewayStatus: func(g *gatewayv1.Gateway) {
				g.Status = gatewayv1.GatewayStatus{
					Addresses: []gatewayv1.GatewayStatusAddress{
						{
							Type:  ptr.To(gatewayv1.HostnameAddressType),
							Value: testConfig.Gateway.GatewayDNSAddress(g),
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
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: gatewayutil.DefaultHTTPListenerName,
							Conditions: []metav1.Condition{
								{
									Type:   string(gatewayv1.GatewayConditionAccepted),
									Status: metav1.ConditionTrue,
									Reason: string(gatewayv1.GatewayReasonAccepted),
								},
							},
						},
						{
							Name: gatewayutil.DefaultHTTPSListenerName,
							Conditions: []metav1.Condition{
								{
									Type:   string(gatewayv1.GatewayConditionAccepted),
									Status: metav1.ConditionTrue,
									Reason: string(gatewayv1.GatewayReasonAccepted),
								},
							},
						},
					},
				}
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
					assert.Equal(t, t.gateway.Status.Addresses[0].Value, httpProxy.Status.Addresses[0].Value)
				}

				if assert.Len(t, httpProxy.Status.Hostnames, 1) {
					assert.Equal(t, ptr.Deref(t.gateway.Spec.Listeners[0].Hostname, ""), httpProxy.Status.Hostnames[0])
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

				hostnamesVerifiedCondition := apimeta.FindStatusCondition(httpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesVerified)
				if assert.NotNil(t, hostnamesVerifiedCondition, "did not find HostnamesVerified condition on HTTPProxy") {
					assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, hostnamesVerifiedCondition.Reason)
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

								hostnamesVerifiedCondition := apimeta.FindStatusCondition(updatedHttpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesVerified)
								if assert.NotNil(t, hostnamesVerifiedCondition, "did not find HostnamesVerified condition on HTTPProxy") {
									assert.Equal(t, networkingv1alpha.UnverifiedHostnamesPresent, hostnamesVerifiedCondition.Reason)
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

								hostnamesVerifiedCondition := apimeta.FindStatusCondition(updatedHttpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesVerified)
								if assert.NotNil(t, hostnamesVerifiedCondition, "did not find HostnamesVerified condition on HTTPProxy") {
									assert.Equal(t, networkingv1alpha.HTTPProxyReasonHostnamesVerified, hostnamesVerifiedCondition.Reason)
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

			tt.existingObjects = append(tt.existingObjects, &gatewayv1.GatewayClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-gateway-class",
				},
				Spec: gatewayv1.GatewayClassSpec{
					ControllerName: testConfig.Gateway.ControllerName,
				},
			})

			for _, obj := range tt.existingObjects {
				obj.SetCreationTimestamp(metav1.Now())
			}

			initialObjects = append(initialObjects, tt.httpProxy)
			initialObjects = append(initialObjects, tt.existingObjects...)
			if tt.httpProxy.Namespace != "" {
				namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tt.httpProxy.Namespace}}
				if tt.namespaceUID != "" {
					namespace.SetUID(types.UID(tt.namespaceUID))
				} else {
					namespace.SetUID(uuid.NewUUID())
				}
				initialObjects = append(initialObjects, namespace)
			}

			fakeClientBuilder := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(initialObjects...).
				WithStatusSubresource(initialObjects...).
				WithStatusSubresource(&gatewayv1.Gateway{}).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						obj.SetUID(uuid.NewUUID())
						obj.SetCreationTimestamp(metav1.Now())
						return client.Create(ctx, obj, opts...)
					},
				})

			fakeClient := fakeClientBuilder.Build()

			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.downstreamObjects...).
				WithStatusSubresource(&gatewayv1.Gateway{}).
				Build()

			mgr := &fakeMockManager{cl: fakeClient}

			reconciler := &HTTPProxyReconciler{
				mgr:               mgr,
				Config:            testConfig,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}

			gatewayReconciler := &GatewayReconciler{
				mgr:               mgr,
				Config:            testConfig,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}

			req := mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(tt.httpProxy),
				},
				ClusterName: "test-cluster",
			}

			ctx := context.Background()
			ctx = log.IntoContext(ctx, logger)

			var err error
			for i := 0; i < 3; i++ {
				_, err = reconciler.Reconcile(ctx, req)
				if i < 2 {
					assert.NoError(t, err)
					continue
				}
				if tt.expectedError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}

			_, err = gatewayReconciler.Reconcile(ctx, req)
			assert.NoError(t, err, "unexpected error reconciling gateway")

			var updatedProxy networkingv1alpha.HTTPProxy
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.httpProxy), &updatedProxy)
			assert.NoError(t, err)

			var gateway gatewayv1.Gateway
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.httpProxy), &gateway)
			if assert.NoError(t, err) {
				apimeta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
					Type:               string(gatewayv1.GatewayConditionAccepted),
					Status:             metav1.ConditionTrue,
					ObservedGeneration: gateway.Generation,
					Reason:             "TestSuite",
					Message:            "set by test suite",
				})

				if assert.NoError(t, fakeClient.Status().Update(ctx, &gateway), "unexpected error while updating gateway status") {
					if tt.postCreateGatewayStatus != nil {

						tt.postCreateGatewayStatus(&gateway)
						err = fakeClient.Status().Update(ctx, &gateway)
						assert.NoError(t, err)
					}

					_, err = reconciler.Reconcile(ctx, req)
					assert.NoError(t, err)

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
				testCtx := &testContext{
					T:                t,
					reconciler:       reconciler,
					gateway:          &gateway,
					downstreamClient: fakeDownstreamClient,
				}
				tt.assert(testCtx, fakeClient, &updatedProxy)
			}

		})
	}
}

func TestConnectorRouteJSONPathTargetsRuleMatch(t *testing.T) {
	path := connectorRouteJSONPath(
		"ns-test",
		&gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw"}},
		"route-name",
		ptr.To(gatewayv1.SectionName("default-https")),
		2,
		1,
	)

	assert.Contains(t, path, `sectionName=="default-https"`)
	assert.Contains(t, path, `kind=="HTTPRoute"`)
	assert.Contains(t, path, `name=="route-name"`)
	assert.Contains(t, path, `/rule/2/match/1/`)
}

func TestConnectorRouteJSONPathDistinctPerRuleMatch(t *testing.T) {
	pathA := connectorRouteJSONPath(
		"ns-test",
		&gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw"}},
		"route-name",
		ptr.To(gatewayv1.SectionName("default-https")),
		0,
		0,
	)
	pathB := connectorRouteJSONPath(
		"ns-test",
		&gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw"}},
		"route-name",
		ptr.To(gatewayv1.SectionName("default-https")),
		1,
		0,
	)

	assert.NotEqual(t, pathA, pathB)
	assert.Contains(t, pathA, `/rule/0/match/0/`)
	assert.Contains(t, pathB, `/rule/1/match/0/`)
}

func TestBuildConnectorEnvoyPatchesTargetsAllHTTPSListeners(t *testing.T) {
	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{
					Name:     gatewayv1.SectionName("default-http"),
					Protocol: gatewayv1.HTTPProtocolType,
				},
				{
					Name:     gatewayv1.SectionName("default-https"),
					Protocol: gatewayv1.HTTPSProtocolType,
				},
				{
					Name:     gatewayv1.SectionName("https-hostname-0"),
					Protocol: gatewayv1.HTTPSProtocolType,
				},
			},
		},
	}

	backends := []connectorBackendPatch{
		{
			ruleIndex:  0,
			matchIndex: 0,
			targetHost: "127.0.0.1",
			targetPort: 5432,
			nodeID:     "node-123",
		},
	}

	patches, err := buildConnectorEnvoyPatches(
		"ns-test",
		"connector-tunnel",
		gateway,
		newHTTPProxy(),
		backends,
	)
	assert.NoError(t, err)

	routeConfigPatchCounts := map[string]int{}
	for _, patch := range patches {
		if patch.Type != routeConfigurationTypeURL {
			continue
		}
		routeConfigPatchCounts[patch.Name]++
	}

	assert.Equal(t, 2, routeConfigPatchCounts["ns-test/gw/default-https"])
	assert.Equal(t, 2, routeConfigPatchCounts["ns-test/gw/https-hostname-0"])
	assert.NotContains(t, routeConfigPatchCounts, "ns-test/gw/default-http")
}

func TestBuildConnectorEnvoyPatchesAddsExtendedConnectPathRouteAndFallback(t *testing.T) {
	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{
					Name:     gatewayv1.SectionName("default-https"),
					Protocol: gatewayv1.HTTPSProtocolType,
				},
			},
		},
	}

	httpProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		h.Spec.Rules = []networkingv1alpha.HTTPProxyRule{
			{
				Matches: []gatewayv1.HTTPRouteMatch{
					{
						Method: ptr.To(gatewayv1.HTTPMethod(http.MethodConnect)),
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
							Value: ptr.To("/"),
						},
					},
				},
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{
					{
						Endpoint: "http://www.example.com",
						Connector: &networkingv1alpha.ConnectorReference{
							Name: "connector-a",
						},
					},
				},
			},
			{
				Matches: []gatewayv1.HTTPRouteMatch{
					{
						Method: ptr.To(gatewayv1.HTTPMethod(http.MethodConnect)),
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
							Value: ptr.To("/ws"),
						},
					},
				},
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{
					{
						Endpoint: "http://www.example.com",
						Connector: &networkingv1alpha.ConnectorReference{
							Name: "connector-b",
						},
					},
				},
			},
		}
	})

	backends := []connectorBackendPatch{
		{
			ruleIndex:  0,
			matchIndex: 0,
			targetHost: "127.0.0.1",
			targetPort: 5432,
			nodeID:     "node-1",
		},
		{
			ruleIndex:  1,
			matchIndex: 0,
			targetHost: "127.0.0.2",
			targetPort: 8443,
			nodeID:     "node-2",
		},
	}

	patches, err := buildConnectorEnvoyPatches(
		"ns-test",
		"connector-tunnel",
		gateway,
		httpProxy,
		backends,
	)
	assert.NoError(t, err)

	type routeDoc struct {
		Name  string                 `json:"name"`
		Match map[string]interface{} `json:"match"`
		Route struct {
			Cluster string `json:"cluster"`
		} `json:"route"`
	}

	routes := make([]routeDoc, 0, len(patches))
	for _, patch := range patches {
		if patch.Type != routeConfigurationTypeURL {
			continue
		}
		if ptr.Deref(patch.Operation.Path, "") != "/virtual_hosts/0/routes/0" {
			continue
		}
		var parsed routeDoc
		assert.NoError(t, json.Unmarshal(patch.Operation.Value.Raw, &parsed))
		routes = append(routes, parsed)
	}

	assert.Len(t, routes, 2)

	// One path-aware extended CONNECT route should target rule 1 cluster (/ws).
	foundExtended := false
	for _, r := range routes {
		if r.Name != "connector-connect-test-rule-1" {
			continue
		}
		foundExtended = true
		assert.Equal(t, "httproute/ns-test/test/rule/1", r.Route.Cluster)
		assert.Equal(t, "/ws", r.Match["prefix"])
	}
	assert.True(t, foundExtended, "expected extended CONNECT path-aware route")

	// One pure CONNECT fallback route should target fallback (rule 0, path "/").
	foundFallback := false
	for _, r := range routes {
		if r.Name != "connector-connect-test" {
			continue
		}
		foundFallback = true
		assert.Equal(t, "httproute/ns-test/test/rule/0", r.Route.Cluster)
		_, hasConnectMatcher := r.Match["connect_matcher"]
		assert.True(t, hasConnectMatcher)
	}
	assert.True(t, foundFallback, "expected pure CONNECT fallback route")
}

func TestBuildConnectorEnvoyPatchesScopesRouteConfigBySectionName(t *testing.T) {
	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{
					Name:     gatewayv1.SectionName("default-https"),
					Protocol: gatewayv1.HTTPSProtocolType,
				},
				{
					Name:     gatewayv1.SectionName("https-hostname-0"),
					Protocol: gatewayv1.HTTPSProtocolType,
				},
			},
		},
	}

	backends := []connectorBackendPatch{
		{
			sectionName: ptr.To(gatewayv1.SectionName("https-hostname-0")),
			ruleIndex:   0,
			matchIndex:  0,
			targetHost:  "127.0.0.1",
			targetPort:  5432,
			nodeID:      "node-123",
		},
	}

	patches, err := buildConnectorEnvoyPatches(
		"ns-test",
		"connector-tunnel",
		gateway,
		newHTTPProxy(),
		backends,
	)
	assert.NoError(t, err)

	routeConfigPatchCounts := map[string]int{}
	for _, patch := range patches {
		if patch.Type != routeConfigurationTypeURL {
			continue
		}
		routeConfigPatchCounts[patch.Name]++
	}

	assert.Equal(t, 2, routeConfigPatchCounts["ns-test/gw/https-hostname-0"])
	assert.NotContains(t, routeConfigPatchCounts, "ns-test/gw/default-https")
}

func TestHTTPProxyFinalizerCleanup(t *testing.T) {
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test-gateway-class",
		},
		Gateway: config.GatewayConfig{
			ControllerName:             gatewayv1.GatewayController("test-gateway-class"),
			DownstreamGatewayClassName: "test-downstream-gateway-class",
		},
	}

	httpProxy := newHTTPProxy()
	deletionTime := metav1.Now()
	httpProxy.DeletionTimestamp = &deletionTime
	httpProxy.Finalizers = append(httpProxy.Finalizers, httpProxyFinalizer)

	namespaceUID := types.UID("11111111-1111-1111-1111-111111111111")
	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: httpProxy.Namespace}}
	upstreamNamespace.SetUID(namespaceUID)

	downstreamNamespaceName := fmt.Sprintf("ns-%s", namespaceUID)
	downstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: downstreamNamespaceName}}
	downstreamPolicy := &envoygatewayv1alpha1.EnvoyPatchPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("connector-%s", httpProxy.Name),
			Namespace: downstreamNamespaceName,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(httpProxy, upstreamNamespace).
		WithStatusSubresource(httpProxy).
		Build()

	fakeDownstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(downstreamNamespace, downstreamPolicy).
		Build()

	reconciler := &HTTPProxyReconciler{
		mgr:               &fakeMockManager{cl: fakeClient},
		Config:            testConfig,
		DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
	}

	req := mcreconcile.Request{
		Request: reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(httpProxy),
		},
		ClusterName: "test-cluster",
	}

	_, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)

	policyList := envoygatewayv1alpha1.EnvoyPatchPolicyList{}
	assert.NoError(t, fakeDownstreamClient.List(ctx, &policyList))
	assert.Len(t, policyList.Items, 0)

	updatedProxy := &networkingv1alpha.HTTPProxy{}
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(httpProxy), updatedProxy)
	if err == nil {
		assert.False(t, controllerutil.ContainsFinalizer(updatedProxy, httpProxyFinalizer))
	} else {
		assert.True(t, apierrors.IsNotFound(err))
	}
}

func setGatewayProgrammedWithDefaultHTTPSListener(g *gatewayv1.Gateway) {
	apimeta.SetStatusCondition(&g.Status.Conditions, metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionProgrammed),
		Status:             metav1.ConditionTrue,
		ObservedGeneration: g.Generation,
		Reason:             string(gatewayv1.GatewayReasonProgrammed),
	})

	defaultHTTPSListenerStatus := gatewayv1.ListenerStatus{
		Name: gatewayutil.DefaultHTTPSListenerName,
		Conditions: []metav1.Condition{
			{
				Type:               string(gatewayv1.ListenerConditionProgrammed),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: g.Generation,
				Reason:             string(gatewayv1.ListenerReasonProgrammed),
			},
		},
	}

	for i := range g.Status.Listeners {
		if g.Status.Listeners[i].Name == gatewayutil.DefaultHTTPSListenerName {
			g.Status.Listeners[i] = defaultHTTPSListenerStatus
			return
		}
	}
	g.Status.Listeners = append(g.Status.Listeners, defaultHTTPSListenerStatus)
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

func (c *fakeCluster) GetAPIReader() client.Reader {
	// In unit tests we use the same fake client as both cached and uncached readers.
	return c.cl
}

func (c *fakeCluster) GetScheme() *runtime.Scheme {
	return c.cl.Scheme()
}
