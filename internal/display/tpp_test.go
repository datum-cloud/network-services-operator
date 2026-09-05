// SPDX-License-Identifier: AGPL-3.0-only

package display

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestComputeTPPActivityDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		old  *networkingv1alpha.TrafficProtectionPolicy
		new  *networkingv1alpha.TrafficProtectionPolicy
		want ActivityDiff
	}{
		{
			name: "mode changed",
			old:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil),
			new:  tppWith(networkingv1alpha.TrafficProtectionPolicyEnforce, 100, nil),
			want: ActivityDiff{
				Change: ActivityChangeUpdated,
				Field:  ActivityFieldMode,
				Value:  "Observe to Enforce",
			},
		},
		{
			name: "sampling changed",
			old:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil),
			new:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 50, nil),
			want: ActivityDiff{
				Change: ActivityChangeUpdated,
				Field:  ActivityFieldSampling,
				Value:  "50%",
			},
		},
		{
			name: "exclusions changed",
			old:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil),
			new:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, &networkingv1alpha.OWASPRuleExclusions{IDs: []int{920100}}),
			want: ActivityDiff{
				Change: ActivityChangeUpdated,
				Field:  ActivityFieldExclusions,
				Value:  "920100",
			},
		},
		{
			name: "unchanged",
			old:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil),
			new:  tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil),
			want: ActivityDiff{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ComputeTPPActivityDiff(tt.old, tt.new))
		})
	}
}

func TestEnsureTPPAnnotations(t *testing.T) {
	t.Parallel()

	policy := tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil)
	require.True(t, EnsureTPPAnnotations(policy, nil, "alb"))
	assert.Equal(t, "alb", policy.Annotations[AnnotationDisplayName])
	assert.Equal(t, "Observe", policy.Annotations[AnnotationDisplayValue])

	old := tppWith(networkingv1alpha.TrafficProtectionPolicyObserve, 100, nil)
	updated := tppWith(networkingv1alpha.TrafficProtectionPolicyEnforce, 100, nil)
	require.True(t, EnsureTPPAnnotations(updated, old, "alb"))
	assert.Equal(t, ActivityFieldMode, updated.Annotations[AnnotationActivityField])
	assert.Equal(t, "Observe to Enforce", updated.Annotations[AnnotationActivityValue])
	assert.Equal(t, "alb", updated.Annotations[AnnotationActivityName])
}

func tppWith(mode networkingv1alpha.TrafficProtectionPolicyMode, sampling int, exclusions *networkingv1alpha.OWASPRuleExclusions) *networkingv1alpha.TrafficProtectionPolicy {
	return &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "waf", Namespace: "proj"},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Group: gatewayv1.GroupName,
					Kind:  gatewayv1.Kind("Gateway"),
					Name:  gatewayv1.ObjectName("alb"),
				},
			}},
			Mode:               mode,
			SamplingPercentage: sampling,
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
					RuleExclusions: exclusions,
				},
			}},
		},
	}
}
