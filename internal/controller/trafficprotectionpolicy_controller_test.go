package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func TestCollectTrafficProtectionPolicyAttachments(t *testing.T) {

	operatorConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain: "example.com",
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test"),
			},
		},
	}

	newGatewayFunc := func(namespace, name string, opts ...func(*gatewayv1.Gateway)) gatewayv1.Gateway {
		return *newGateway(operatorConfig, namespace, name, opts...)
	}

	type testContext struct {
		*testing.T
		reconciler *TrafficProtectionPolicyReconciler
		gateways   []gatewayv1.Gateway
		httpRoutes []gatewayv1.HTTPRoute
	}

	tests := []struct {
		name                      string
		gateways                  []gatewayv1.Gateway
		httpRoutes                []gatewayv1.HTTPRoute
		trafficProtectionPolicies []networkingv1alpha.TrafficProtectionPolicy
		assert                    func(t *testContext, policyAttachments []policyAttachment)
	}{
		{
			name: "direct gateway attachment",
			gateways: []gatewayv1.Gateway{
				newGatewayFunc("default", "gateway-1"),
			},
			trafficProtectionPolicies: []networkingv1alpha.TrafficProtectionPolicy{
				newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
								Kind: "Gateway",
								Name: "gateway-1",
							},
						},
					}
				}),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, policyAttachments, 1, "expected one policy attachment") {
					attachment := policyAttachments[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.Route)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)
				}
			},
		},
		{
			name: "gateway listener attachment",
			gateways: []gatewayv1.Gateway{
				newGatewayFunc("default", "gateway-1"),
			},
			trafficProtectionPolicies: []networkingv1alpha.TrafficProtectionPolicy{
				newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
								Kind: "Gateway",
								Name: "gateway-1",
							},
							SectionName: ptr.To(gatewayv1.SectionName(gatewayutil.DefaultHTTPListenerName)),
						},
					}
				}),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, policyAttachments, 1, "expected one policy attachment") {
					attachment := policyAttachments[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Equal(t, gatewayv1.SectionName(gatewayutil.DefaultHTTPListenerName), ptr.Deref(attachment.Listener, ""))
					assert.Nil(t, attachment.Route)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)
				}
			},
		},
		{
			name: "direct httproute attachment",
			gateways: []gatewayv1.Gateway{
				newGatewayFunc("default", "gateway-1"),
			},
			httpRoutes: []gatewayv1.HTTPRoute{
				*newHTTPRoute("default", "route-1", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name: gatewayv1.ObjectName("gateway-1"),
						},
					}
				}),
			},
			trafficProtectionPolicies: []networkingv1alpha.TrafficProtectionPolicy{
				newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
								Kind: "HTTPRoute",
								Name: "route-1",
							},
						},
					}
				}),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, policyAttachments, 1, "expected one policy attachment") {
					attachment := policyAttachments[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Equal(t, t.httpRoutes[0].Name, attachment.Route.Name, "expected attachment to route-1")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)
				}
			},
		},
		{
			name: "httproute rule attachment",
			gateways: []gatewayv1.Gateway{
				newGatewayFunc("default", "gateway-1"),
			},
			httpRoutes: []gatewayv1.HTTPRoute{
				*newHTTPRoute("default", "route-1", func(route *gatewayv1.HTTPRoute) {
					route.Spec.ParentRefs = []gatewayv1.ParentReference{
						{
							Name: gatewayv1.ObjectName("gateway-1"),
						},
					}
					route.Spec.Rules = []gatewayv1.HTTPRouteRule{
						{
							Name: ptr.To(gatewayv1.SectionName("rule-1")),
						},
					}
				}),
			},
			trafficProtectionPolicies: []networkingv1alpha.TrafficProtectionPolicy{
				newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
								Kind: "HTTPRoute",
								Name: "route-1",
							},
							SectionName: ptr.To(gatewayv1.SectionName("rule-1")),
						},
					}
				}),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, policyAttachments, 1, "expected one policy attachment") {
					attachment := policyAttachments[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Equal(t, t.httpRoutes[0].Name, attachment.Route.Name, "expected attachment to route-1")
					assert.Nil(t, attachment.Listener)
					assert.Equal(t, gatewayv1.SectionName("rule-1"), ptr.Deref(attachment.RuleSectionName, ""))
					assert.Greater(t, len(attachment.CorazaDirectives), 0)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			reconciler := &TrafficProtectionPolicyReconciler{Config: operatorConfig}
			attachments := reconciler.collectTrafficProtectionPolicyAttachments(
				t.Context(),
				reconciler.getTrafficProtectionPolicyContexts(tt.trafficProtectionPolicies),
				tt.gateways,
				tt.httpRoutes,
			)

			testCtx := &testContext{
				T:          t,
				reconciler: reconciler,
				gateways:   tt.gateways,
				httpRoutes: tt.httpRoutes,
			}

			tt.assert(testCtx, attachments)

		})
	}
}

func newTrafficProtectionPolicy(
	namespace,
	name string,
	opts ...func(*networkingv1alpha.TrafficProtectionPolicy),
) networkingv1alpha.TrafficProtectionPolicy {
	tpp := networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uuid.NewUUID(),
		},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode:               networkingv1alpha.TrafficProtectionPolicyObserve,
			SamplingPercentage: 100,
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{
				{
					Type: "OWASPCoreRuleSet",
					OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
						ParanoiaLevels: networkingv1alpha.ParanoiaLevels{
							Blocking:  1,
							Detection: 1,
						},
						ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{
							Inbound:  5,
							Outbound: 4,
						},
					},
				},
			},
		},
	}

	for _, opt := range opts {
		opt(&tpp)
	}

	return tpp
}
