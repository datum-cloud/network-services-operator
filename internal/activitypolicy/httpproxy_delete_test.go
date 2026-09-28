// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPProxyDeleteOutcomes(t *testing.T) {
	policy := loadPolicy(t, "config/milo/activity/policies/httpproxy-policy.yaml")
	cases := []struct {
		name     string
		code     int
		response any
		want     string
	}{
		{"failed delete without response", 500, nil, ""},
		{"forbidden delete", 403, nil, ""},
		{"not found", 404, nil, ""},
		{"failed delete with annotations", 500, map[string]any{"metadata": map[string]any{"annotations": map[string]any{"networking.datumapis.com/display-name": "storefront"}}}, ""},
		{"successful delete without response", 200, nil, "delete-name"},
		{"accepted delete with status response", 202, map[string]any{"kind": "Status", "status": "Success"}, "delete-name"},
		{"successful delete without annotations", 200, map[string]any{"metadata": map[string]any{}}, "delete-name"},
		{"successful annotated delete", 200, map[string]any{"metadata": map[string]any{"annotations": map[string]any{"networking.datumapis.com/display-name": "storefront"}}}, "delete-annotated"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			audit := map[string]any{"verb": "delete", "user": map[string]any{"username": "alice@example.com"}, "objectRef": map[string]any{"name": "proxy"}, "responseStatus": map[string]any{"code": tt.code}}
			if tt.response != nil {
				audit["responseObject"] = tt.response
			}
			rule := firstMatchingAuditRule(t, policy, audit)
			require.Equal(t, tt.want, rule)
			if rule != "" {
				require.NotEmpty(t, auditSummary(t, policy, rule, audit))
			}
		})
	}
}
