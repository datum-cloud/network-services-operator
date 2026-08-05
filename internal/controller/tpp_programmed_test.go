package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestProgrammedFromDownstream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		downstream *networkingv1alpha.TrafficProtectionPolicy
		missing    bool
		generation int64
		status     metav1.ConditionStatus
		reason     gatewayv1.PolicyConditionReason
		msgContain string
	}{
		{
			name:       "missing downstream",
			missing:    true,
			generation: 2,
			status:     metav1.ConditionFalse,
			reason:     PolicyReasonProgrammedPending,
			msgContain: "generation 2",
		},
		{
			name:       "no programmed condition",
			downstream: &networkingv1alpha.TrafficProtectionPolicy{},
			generation: 1,
			status:     metav1.ConditionFalse,
			reason:     PolicyReasonProgrammedPending,
			msgContain: "Waiting",
		},
		{
			name: "stale generation",
			downstream: &networkingv1alpha.TrafficProtectionPolicy{
				Status: networkingv1alpha.TrafficProtectionPolicyStatus{
					PolicyStatus: gatewayv1alpha2.PolicyStatus{
						Ancestors: []gatewayv1.PolicyAncestorStatus{{
							Conditions: []metav1.Condition{{
								Type:               conditionTypeProgrammed,
								Status:             metav1.ConditionTrue,
								Reason:             string(PolicyReasonProgrammed),
								ObservedGeneration: 1,
								Message:            "1/1 edges programmed generation 1",
							}},
						}},
					},
				},
			},
			generation: 2,
			status:     metav1.ConditionFalse,
			reason:     PolicyReasonProgrammedPending,
			msgContain: "generation 2",
		},
		{
			name: "all edges programmed",
			downstream: &networkingv1alpha.TrafficProtectionPolicy{
				Status: networkingv1alpha.TrafficProtectionPolicyStatus{
					PolicyStatus: gatewayv1alpha2.PolicyStatus{
						Ancestors: []gatewayv1.PolicyAncestorStatus{{
							Conditions: []metav1.Condition{{
								Type:               conditionTypeProgrammed,
								Status:             metav1.ConditionTrue,
								Reason:             string(PolicyReasonProgrammed),
								ObservedGeneration: 3,
								Message:            "3/3 edges programmed generation 3",
							}},
						}},
					},
				},
			},
			generation: 3,
			status:     metav1.ConditionTrue,
			reason:     PolicyReasonProgrammed,
			msgContain: "3/3",
		},
		{
			name: "partial failure",
			downstream: &networkingv1alpha.TrafficProtectionPolicy{
				Status: networkingv1alpha.TrafficProtectionPolicyStatus{
					PolicyStatus: gatewayv1alpha2.PolicyStatus{
						Ancestors: []gatewayv1.PolicyAncestorStatus{{
							Conditions: []metav1.Condition{{
								Type:               conditionTypeProgrammed,
								Status:             metav1.ConditionFalse,
								Reason:             string(PolicyReasonProgrammedPartialFailure),
								ObservedGeneration: 4,
								Message:            "2/5 edges programmed generation 4",
							}},
						}},
					},
				},
			},
			generation: 4,
			status:     metav1.ConditionFalse,
			reason:     PolicyReasonProgrammedPartialFailure,
			msgContain: "2/5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, reason, message := programmedFromDownstream(tt.downstream, tt.missing, tt.generation)
			assert.Equal(t, tt.status, status)
			assert.Equal(t, tt.reason, reason)
			assert.Contains(t, message, tt.msgContain)
		})
	}
}
