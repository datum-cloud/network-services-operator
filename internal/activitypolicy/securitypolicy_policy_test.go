// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityPolicyPolicy_Fixtures(t *testing.T) {
	t.Parallel()
	pol := loadPolicy(t, "config/milo/activity/policies/securitypolicy-policy.yaml")

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
				"requestObject": map[string]any{"spec": map[string]any{"basicAuth": map[string]any{"users": map[string]any{"name": "alb-basic-auth"}}}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
					"networking.datumapis.com/display-name": "alb",
				}}},
				"objectRef":      map[string]any{"name": "alb"},
				"responseStatus": map[string]any{"code": 201},
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
				"objectRef": map[string]any{"name": "alb"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantRule, firstMatchingAuditRule(t, pol, tt.audit))
		})
	}
}
