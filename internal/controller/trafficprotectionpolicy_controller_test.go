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
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
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
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
								Kind: "Gateway",
								Name: "gateway-1",
							},
						},
						{
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
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
							assert.Equal(t, string(gatewayv1alpha2.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
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
							assert.Equal(t, string(gatewayv1alpha2.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
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

					policy := t.trafficProtectionPolicies[0]
					if assert.Len(t, policy.Status.Ancestors, 1, "expected one ancestor status") {
						ancestor := policy.Status.Ancestors[0]
						assert.Equal(t, "Gateway", string(ptr.Deref(ancestor.AncestorRef.Kind, "")))
						assert.Equal(t, attachment.Gateway.Name, string(ancestor.AncestorRef.Name), "expected ancestor name to match gateway name")
						if assert.Len(t, ancestor.Conditions, 1) {
							assert.Equal(t, string(gatewayv1alpha2.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
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
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
								Kind: "HTTPRoute",
								Name: "route-1",
							},
						},
						{
							LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
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
							assert.Equal(t, string(gatewayv1alpha2.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
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
							assert.Equal(t, string(gatewayv1alpha2.PolicyReasonAccepted), ancestor.Conditions[0].Reason, "expected accepted reason")
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
				LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
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
				LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName("non-existent-rule")),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonTargetNotFound), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
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
				LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: "route-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName("some-rule")),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
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
				LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
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
				LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName("non-existent-listener")),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonTargetNotFound), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
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
				LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "gateway-1",
				},
				SectionName: ptr.To(gatewayv1.SectionName(gatewayutil.DefaultHTTPListenerName)),
			},
			assert: func(t *testContext, policyAttachments []policyAttachment) {
				if assert.Len(t, t.policy.Status.Ancestors, 1) {
					if assert.Len(t, t.policy.Status.Ancestors[0].Conditions, 1) {
						assert.Equal(t, string(gatewayv1alpha2.PolicyReasonConflicted), t.policy.Status.Ancestors[0].Conditions[0].Reason, "expected conflicted reason")
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
