package controller

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

func TestHTTPPRoxyCollectDesiredResources(t *testing.T) {
	httpProxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "test",
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
