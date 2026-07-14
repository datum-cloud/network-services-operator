// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestTrafficProtectionPolicySpec_InvertedParanoiaLevels(t *testing.T) {
	t.Parallel()

	owaspRuleSet := func(blocking, detection int) networkingv1alpha.TrafficProtectionPolicyRuleSet {
		return networkingv1alpha.TrafficProtectionPolicyRuleSet{
			Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
			OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
				ParanoiaLevels: networkingv1alpha.ParanoiaLevels{
					Blocking:  blocking,
					Detection: detection,
				},
			},
		}
	}

	tests := []struct {
		name         string
		ruleSets     []networkingv1alpha.TrafficProtectionPolicyRuleSet
		wantInverted bool
	}{
		{
			name:         "no rulesets",
			ruleSets:     nil,
			wantInverted: false,
		},
		{
			name:         "equal levels",
			ruleSets:     []networkingv1alpha.TrafficProtectionPolicyRuleSet{owaspRuleSet(2, 2)},
			wantInverted: false,
		},
		{
			name:         "higher detection",
			ruleSets:     []networkingv1alpha.TrafficProtectionPolicyRuleSet{owaspRuleSet(1, 3)},
			wantInverted: false,
		},
		{
			name:         "detection below blocking",
			ruleSets:     []networkingv1alpha.TrafficProtectionPolicyRuleSet{owaspRuleSet(2, 1)},
			wantInverted: true,
		},
		{
			name:         "detection far below blocking",
			ruleSets:     []networkingv1alpha.TrafficProtectionPolicyRuleSet{owaspRuleSet(4, 1)},
			wantInverted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := networkingv1alpha.TrafficProtectionPolicySpec{RuleSets: tt.ruleSets}
			levels := spec.InvertedParanoiaLevels()

			if tt.wantInverted {
				if assert.NotNil(t, levels, "inverted ruleset must be reported") {
					assert.Less(t, levels.Detection, levels.Blocking,
						"reported levels must be inverted")
				}
				return
			}
			assert.Nil(t, levels, "no ruleset is inverted")
		})
	}
}
