// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTPPPolicy_Fixtures(t *testing.T) {
	t.Parallel()
	pol := loadPolicy(t, "config/milo/activity/policies/trafficprotectionpolicy-policy.yaml")

	tests := []struct {
		name     string
		wantRule string
		audit    map[string]any
	}{
		{
			name:     "create annotated",
			wantRule: "create-annotated",
			audit: map[string]any{
				"user":          map[string]any{"username": "alice@example.com"},
				"verb":          "create",
				"requestObject": map[string]any{"spec": map[string]any{"mode": "Observe"}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
					"networking.datumapis.com/display-name":  "alb",
					"networking.datumapis.com/display-value": "Observe",
				}}},
				"objectRef":      map[string]any{"name": "waf"},
				"responseStatus": map[string]any{"code": 201},
			},
		},
		{
			name:     "update mode",
			wantRule: "update-mode",
			audit: map[string]any{
				"user":          map[string]any{"username": "alice@example.com"},
				"verb":          "patch",
				"objectRef":     map[string]any{"name": "waf"},
				"requestObject": map[string]any{"spec": map[string]any{"mode": "Enforce"}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
					"networking.datumapis.com/display-name":   "alb",
					"networking.datumapis.com/activity-field": "mode",
					"networking.datumapis.com/activity-value": "Observe to Enforce",
				}}},
				"responseStatus": map[string]any{"code": 200},
			},
		},
		{
			name:     "delete annotated",
			wantRule: "delete-annotated",
			audit: map[string]any{
				"user": map[string]any{"username": "alice@example.com"},
				"verb": "delete",
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
					"networking.datumapis.com/display-name": "alb",
				}}},
				"objectRef": map[string]any{"name": "waf"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantRule, firstMatchingAuditRule(t, pol, tt.audit))
		})
	}

	t.Run("programmed related event", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "programmed-related", firstMatchingEventRule(t, pol, map[string]any{
			"reason": "Programmed",
			"metadata": map[string]any{"annotations": map[string]any{
				"networking.datumapis.com/display-name": "alb",
			}},
			"regarding": map[string]any{"name": "waf"},
			"related":   map[string]any{"name": "alb", "kind": "HTTPProxy"},
		}))
	})
}
