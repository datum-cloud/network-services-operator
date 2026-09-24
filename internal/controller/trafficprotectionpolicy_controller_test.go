package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		reconciler                *TrafficProtectionPolicyReconciler
		gateways                  []gatewayv1.Gateway
		httpRoutes                []gatewayv1.HTTPRoute
		trafficProtectionPolicies []*policyContext
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
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
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
					policy := t.trafficProtectionPolicies[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.Route)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)

					if assert.Len(t, policy.Status.Ancestors, 1, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[0]
						assert.Equal(t, "Gateway", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Gateway.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")
						assert.Equal(t, string(gatewayv1.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
					}
				}
			},
		},
		{
			name: "multiple direct gateway attachments",
			gateways: []gatewayv1.Gateway{
				newGatewayFunc("default", "gateway-1"),
				newGatewayFunc("default", "gateway-2"),
			},
			trafficProtectionPolicies: []networkingv1alpha.TrafficProtectionPolicy{
				newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
								Kind: "Gateway",
								Name: "gateway-1",
							},
						},
						{
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
								Kind: "Gateway",
								Name: "gateway-2",
							},
						},
					}
				}),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				policy := t.trafficProtectionPolicies[0]

				if assert.Len(t, policyAttachments, 2, "expected one policy attachment") {
					attachment := policyAttachments[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.Route)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)

					if assert.Len(t, policy.Status.Ancestors, 2, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[0]
						assert.Equal(t, "Gateway", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Gateway.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")
						if assert.Len(t, ancestor.Conditions, 1) {
							assert.Equal(t, string(gatewayv1.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
						}
					}

					attachment = policyAttachments[1]

					assert.Equal(t, t.gateways[1].Name, attachment.Gateway.Name, "expected attachment to gateway-2")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.Route)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)

					if assert.Len(t, policy.Status.Ancestors, 2, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[1]
						assert.Equal(t, "Gateway", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Gateway.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")

						if assert.Len(t, ancestor.Conditions, 1) {
							assert.Equal(t, string(gatewayv1.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
						}
					}
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
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
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

					policy := t.trafficProtectionPolicies[0]
					if assert.Len(t, policy.Status.Ancestors, 1, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[0]
						assert.Equal(t, "Gateway", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Gateway.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")
						if assert.Len(t, ancestor.Conditions, 1) {
							assert.Equal(t, string(gatewayv1.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
						}
					}
				}
			},
		},
		{
			name: "multiple direct httproute attachments",
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
				*newHTTPRoute("default", "route-2", func(route *gatewayv1.HTTPRoute) {
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
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
								Kind: "HTTPRoute",
								Name: "route-1",
							},
						},
						{
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
								Kind: "HTTPRoute",
								Name: "route-2",
							},
						},
					}
				}),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, policyAttachments, 2, "expected one policy attachment") {
					attachment := policyAttachments[0]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Equal(t, t.httpRoutes[0].Name, attachment.Route.Name, "expected attachment to route-1")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)

					policy := t.trafficProtectionPolicies[0]
					if assert.Len(t, policy.Status.Ancestors, 2, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[0]
						assert.Equal(t, "HTTPRoute", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Route.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")
						if assert.Len(t, ancestor.Conditions, 1) {
							assert.Equal(t, string(gatewayv1.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
						}
					}

					attachment = policyAttachments[1]

					assert.Equal(t, t.gateways[0].Name, attachment.Gateway.Name, "expected attachment to gateway-1")
					assert.Equal(t, t.httpRoutes[1].Name, attachment.Route.Name, "expected attachment to route-2")
					assert.Nil(t, attachment.Listener)
					assert.Nil(t, attachment.RuleSectionName)
					assert.Greater(t, len(attachment.CorazaDirectives), 0)

					if assert.Len(t, policy.Status.Ancestors, 2, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[1]
						assert.Equal(t, "HTTPRoute", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Route.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")
						if assert.Len(t, ancestor.Conditions, 1) {
							assert.Equal(t, string(gatewayv1.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
						}
					}
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
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
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
							LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
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

			tppContexts := reconciler.getTrafficProtectionPolicyContexts(tt.trafficProtectionPolicies)

			attachments := reconciler.collectTrafficProtectionPolicyAttachments(
				t.Context(),
				tppContexts,
				tt.gateways,
				tt.httpRoutes,
			)

			testCtx := &testContext{
				T:                         t,
				reconciler:                reconciler,
				gateways:                  tt.gateways,
				httpRoutes:                tt.httpRoutes,
				trafficProtectionPolicies: tppContexts,
			}

			tt.assert(testCtx, attachments)

		})
	}
}

func TestProcessTrafficProtectionPolicyForHTTPRoute(t *testing.T) {

	type testContext struct {
		*testing.T
		policy *policyContext
	}

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

	tests := []struct {
		name              string
		policy            *policyContext
		routeMap          map[client.ObjectKey]*policyRouteTargetContext
		gatewayMap        map[client.ObjectKey]*policyGatewayTargetContext
		policyAttachments []policyAttachment
		targetRef         gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName
		assert            func(t *testContext, policyAttachments []policyAttachment)
	}{
		{
			name: "route already directly attached",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1")),
			},
			routeMap: map[client.ObjectKey]*policyRouteTargetContext{
				{Namespace: "default", Name: "route-1"}: {
					HTTPRoute: newHTTPRoute("default", "route-1"),
					attached:  true,
				},
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
					}
				}
			},
		},
		{
			name: "route rule not found",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1")),
			},
			routeMap: map[client.ObjectKey]*policyRouteTargetContext{
				{Namespace: "default", Name: "route-1"}: {
					HTTPRoute: newHTTPRoute("default", "route-1"),
					attached:  true,
				},
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName("non-existent-rule")),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1.PolicyReasonTargetNotFound), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
					}
				}
			},
		},
		{
			name: "route rule conflict",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1")),
			},
			routeMap: map[client.ObjectKey]*policyRouteTargetContext{
				{Namespace: "default", Name: "route-1"}: {
					HTTPRoute: newHTTPRoute("default", "route-1", func(route *gatewayv1.HTTPRoute) {
						route.Spec.Rules = []gatewayv1.HTTPRouteRule{
							{
								Name: ptr.To(gatewayv1.SectionName("some-rule")),
							},
						}
					}),
					attachedToRouteRules: sets.Set[string]{
						"some-rule": {},
					},
				},
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName("some-rule")),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
					}
				}
			},
		},
		{
			name: "inverted paranoia levels rejected",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.RuleSets[0].OWASPCoreRuleSet.ParanoiaLevels = networkingv1alpha.ParanoiaLevels{
						Blocking:  2,
						Detection: 1,
					}
				})),
			},
			routeMap: map[client.ObjectKey]*policyRouteTargetContext{
				{Namespace: "default", Name: "route-1"}: {
					HTTPRoute: newHTTPRoute("default", "route-1"),
				},
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				assert.Empty(t, policyAttachments, "invalid policy must not be attached")
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						cond := t.policy.Status.Ancestors[0].Conditions[0]
						assert.Equal(t, string(gatewayv1.PolicyReasonInvalid), cond.Reason, "expected invalid reason")
						assert.Equal(t, metav1.ConditionFalse, cond.Status, "expected Accepted=False")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			reconciler := &TrafficProtectionPolicyReconciler{Config: operatorConfig}

			attachments := reconciler.processTrafficProtectionPolicyForHTTPRoute(
				t.Context(),
				tt.routeMap,
				tt.gatewayMap,
				tt.policyAttachments,
				tt.policy,
				tt.targetRef,
			)

			testCtx := &testContext{
				T:      t,
				policy: tt.policy,
			}

			tt.assert(testCtx, attachments)
		})
	}
}

func TestProcessTrafficProtectionPolicyForGateway(t *testing.T) {
	type testContext struct {
		*testing.T
		policy *policyContext
	}

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

	tests := []struct {
		name              string
		policy            *policyContext
		gatewayMap        map[client.ObjectKey]*policyGatewayTargetContext
		policyAttachments []policyAttachment
		targetRef         gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName
		assert            func(t *testContext, policyAttachments []policyAttachment)
	}{
		{
			name: "gateway already directly attached",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1")),
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway:  ptr.To(newGatewayFunc("default", "gateway-1")),
					attached: true,
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
					}
				}
			},
		},
		{
			name: "gateway listener not found",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1")),
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName("non-existent-listener")),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1.PolicyReasonTargetNotFound), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
					}
				}
			},
		},
		{
			name: "gateway listener conflict",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1")),
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
					attachedToListeners: sets.Set[string]{
						gatewayutil.DefaultHTTPListenerName: {},
					},
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName(gatewayutil.DefaultHTTPListenerName)),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
					}
				}
			},
		},
		{
			name: "inverted paranoia levels rejected",
			policy: &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.RuleSets[0].OWASPCoreRuleSet.ParanoiaLevels = networkingv1alpha.ParanoiaLevels{
						Blocking:  2,
						Detection: 1,
					}
				})),
			},
			gatewayMap: map[client.ObjectKey]*policyGatewayTargetContext{
				{Namespace: "default", Name: "gateway-1"}: {
					Gateway: ptr.To(newGatewayFunc("default", "gateway-1")),
				},
			},
			targetRef: gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				assert.Empty(t, policyAttachments, "invalid policy must not be attached")
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						cond := t.policy.Status.Ancestors[0].Conditions[0]
						assert.Equal(t, string(gatewayv1.PolicyReasonInvalid), cond.Reason, "expected invalid reason")
						assert.Equal(t, metav1.ConditionFalse, cond.Status, "expected Accepted=False")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			reconciler := &TrafficProtectionPolicyReconciler{Config: operatorConfig}

			attachments := reconciler.processTrafficProtectionPolicyForGateway(
				t.Context(),
				tt.gatewayMap,
				tt.policyAttachments,
				tt.policy,
				tt.targetRef,
			)

			testCtx := &testContext{
				T:      t,
				policy: tt.policy,
			}

			tt.assert(testCtx, attachments)

		})
	}
}

func TestParanoiaLevelsResolveError(t *testing.T) {
	tests := []struct {
		name      string
		blocking  int
		detection int
		wantError bool
	}{
		{name: "equal levels", blocking: 2, detection: 2, wantError: false},
		{name: "higher detection", blocking: 1, detection: 3, wantError: false},
		{name: "detection below blocking", blocking: 2, detection: 1, wantError: true},
		{name: "defaulted detection below blocking", blocking: 4, detection: 1, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &policyContext{
				TrafficProtectionPolicy: ptr.To(newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
					tpp.Spec.RuleSets[0].OWASPCoreRuleSet.ParanoiaLevels = networkingv1alpha.ParanoiaLevels{
						Blocking:  tt.blocking,
						Detection: tt.detection,
					}
				})),
			}

			resolveErr := paranoiaLevelsResolveError(policy)
			if tt.wantError {
				if assert.NotNil(t, resolveErr) {
					assert.Equal(t, gatewayv1.PolicyReasonInvalid, resolveErr.Reason)
				}
			} else {
				assert.Nil(t, resolveErr)
			}
		})
	}
}

func TestGetCorazaDirectivesForTrafficProtectionPolicy(t *testing.T) {
	operatorConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain: "example.com",
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test"),
			},
			DownstreamGatewayClassName: "test-gateway-class",
			Coraza: config.CorazaConfig{
				ListenerDirectives: []string{
					"SecRuleEngine On",
				},
				RouteBaseDirectives: []string{
					"Include @crs-setup-conf", "Include @recommended-conf",
				},
			},
		},
	}

	tests := []struct {
		name                     string
		policy                   networkingv1alpha.TrafficProtectionPolicy
		expectedCorazaDirectives []string
	}{
		{
			name:   "default OWASP CRS settings - Observe",
			policy: newTrafficProtectionPolicy("default", "tpp-1"),
			expectedCorazaDirectives: []string{
				"Include @crs-setup-conf",
				"Include @recommended-conf",
				"SecRuleEngine DetectionOnly",
				"SecAction \"id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=5,setvar:tx.outbound_anomaly_score_threshold=4\"",
				"SecAction \"id:900000,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.blocking_paranoia_level=1\"",
				"SecAction \"id:900001,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.detection_paranoia_level=1\"",
				"Include @owasp_crs/*.conf",
			},
		},
		{
			name: "default OWASP CRS settings - Enforce",
			policy: newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
				tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce
			}),
			expectedCorazaDirectives: []string{
				"Include @crs-setup-conf",
				"Include @recommended-conf",
				"SecRuleEngine On",
				"SecAction \"id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=5,setvar:tx.outbound_anomaly_score_threshold=4\"",
				"SecAction \"id:900000,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.blocking_paranoia_level=1\"",
				"SecAction \"id:900001,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.detection_paranoia_level=1\"",
				"Include @owasp_crs/*.conf",
			},
		},
		{
			name: "default OWASP CRS settings - Disabled",
			policy: newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
				tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyDisabled
			}),
			expectedCorazaDirectives: []string{
				"Include @crs-setup-conf",
				"Include @recommended-conf",
				"SecRuleEngine Off",
				"SecAction \"id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=5,setvar:tx.outbound_anomaly_score_threshold=4\"",
				"SecAction \"id:900000,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.blocking_paranoia_level=1\"",
				"SecAction \"id:900001,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.detection_paranoia_level=1\"",
				"Include @owasp_crs/*.conf",
			},
		},
		{
			name: "customized OWASP CRS settings",
			policy: newTrafficProtectionPolicy("default", "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
				tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce
				tpp.Spec.SamplingPercentage = 50
				owaspCRS := &tpp.Spec.RuleSets[0].OWASPCoreRuleSet

				owaspCRS.ScoreThresholds.Inbound = 1000
				owaspCRS.ScoreThresholds.Outbound = 1000

				owaspCRS.ParanoiaLevels.Blocking = 4
				owaspCRS.ParanoiaLevels.Detection = 4

				owaspCRS.RuleExclusions = &networkingv1alpha.OWASPRuleExclusions{
					Tags: []networkingv1alpha.OWASPTag{"tag1", "tag2"},
					IDs:  []int{9999},
					IDRanges: []networkingv1alpha.OWASPIDRange{
						"1000-2000",
					},
				}
			}),
			expectedCorazaDirectives: []string{
				"Include @crs-setup-conf",
				"Include @recommended-conf",
				"SecRuleEngine On",
				"SecAction \"id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=1000,setvar:tx.outbound_anomaly_score_threshold=1000\"",
				"SecAction \"id:900000,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.blocking_paranoia_level=4\"",
				"SecAction \"id:900001,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.detection_paranoia_level=4\"",
				"SecAction \"id:900400,phase:1,pass,nolog,setvar:tx.sampling_percentage=50\"",
				"Include @owasp_crs/*.conf",
				"SecRuleRemoveByTag \"tag1\"",
				"SecRuleRemoveByTag \"tag2\"",
				"SecRuleRemoveById 9999",
				"SecRuleRemoveById \"1000-2000\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			reconciler := &TrafficProtectionPolicyReconciler{Config: operatorConfig}
			corazaDirectives := reconciler.getCorazaDirectivesForTrafficProtectionPolicy(&policyContext{ptr.To(tt.policy)})
			assert.EqualValues(t, tt.expectedCorazaDirectives, corazaDirectives)
		})
	}
}

func TestGetCorazaDirectivesDoesNotAliasRouteBaseDirectives(t *testing.T) {
	base := make([]string, 2, 8)
	base[0] = "Include @crs-setup-conf"
	base[1] = "Include @recommended-conf"

	operatorConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			Coraza: config.CorazaConfig{RouteBaseDirectives: base},
		},
	}
	reconciler := &TrafficProtectionPolicyReconciler{Config: operatorConfig}

	enforce := reconciler.getCorazaDirectivesForTrafficProtectionPolicy(&policyContext{ptr.To(
		newTrafficProtectionPolicy("default", "tpp-enforce", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
			tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce
		}),
	)})
	assert.Contains(t, enforce, "SecRuleEngine On")

	reconciler.getCorazaDirectivesForTrafficProtectionPolicy(&policyContext{ptr.To(
		newTrafficProtectionPolicy("default", "tpp-observe"),
	)})

	assert.Contains(t, enforce, "SecRuleEngine On",
		"second policy leaked into the first via a shared RouteBaseDirectives backing array")
	assert.EqualValues(t, []string{"Include @crs-setup-conf", "Include @recommended-conf"},
		reconciler.Config.Gateway.Coraza.RouteBaseDirectives,
		"shared base directives were mutated")
}

// nolint:unparam
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
