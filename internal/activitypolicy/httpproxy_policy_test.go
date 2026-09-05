// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

type activityPolicy struct {
	Spec struct {
		AuditRules []struct {
			Name    string `json:"name"`
			Match   string `json:"match"`
			Summary string `json:"summary"`
		} `json:"auditRules"`
		EventRules []struct {
			Name    string `json:"name"`
			Match   string `json:"match"`
			Summary string `json:"summary"`
		} `json:"eventRules"`
	} `json:"spec"`
}

func loadPolicy(t *testing.T, rel string) activityPolicy {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	data, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)
	var pol activityPolicy
	require.NoError(t, yaml.Unmarshal(data, &pol))
	return pol
}

func firstMatchingEventRule(t *testing.T, pol activityPolicy, event map[string]any) string {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("event", cel.DynType))
	require.NoError(t, err)
	for _, r := range pol.Spec.EventRules {
		ast, iss := env.Compile(r.Match)
		require.NoError(t, iss.Err())
		prg, err := env.Program(ast)
		require.NoError(t, err)
		out, _, err := prg.Eval(map[string]any{"event": event})
		require.NoError(t, err)
		if matched, ok := out.Value().(bool); ok && matched {
			return r.Name
		}
	}
	return ""
}

func firstMatchingAuditRule(t *testing.T, pol activityPolicy, audit map[string]any) string {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("audit", cel.DynType))
	require.NoError(t, err)
	for _, r := range pol.Spec.AuditRules {
		ast, iss := env.Compile(r.Match)
		require.NoError(t, iss.Err())
		prg, err := env.Program(ast)
		require.NoError(t, err)
		out, _, err := prg.Eval(map[string]any{"audit": audit})
		require.NoError(t, err)
		if matched, ok := out.Value().(bool); ok && matched {
			return r.Name
		}
	}
	return ""
}

func TestHTTPProxyPolicy_Fixtures(t *testing.T) {
	t.Parallel()
	pol := loadPolicy(t, "config/milo/activity/policies/httpproxy-policy.yaml")

	displayAnns := map[string]any{
		"networking.datumapis.com/display-name":  "alb",
		"networking.datumapis.com/display-value": "https://origin.example.com",
	}

	tests := []struct {
		name     string
		wantRule string
		audit    map[string]any
	}{
		{
			name:     "create annotated with backend",
			wantRule: "create-annotated-backend",
			audit: map[string]any{
				"user":           map[string]any{"username": "alice@example.com"},
				"verb":           "create",
				"requestObject":  map[string]any{"spec": map[string]any{"hostnames": []any{"app.example.com"}}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": displayAnns}},
				"objectRef":      map[string]any{"name": "alb"},
				"responseStatus": map[string]any{"code": 201},
			},
		},
		{
			name:     "create name fallback",
			wantRule: "create-name-backend",
			audit: map[string]any{
				"user": map[string]any{"username": "alice@example.com"},
				"verb": "create",
				"requestObject": map[string]any{"spec": map[string]any{
					"hostnames": []any{"app.example.com"},
					"rules":     []any{map[string]any{"backends": []any{map[string]any{"endpoint": "https://origin.example.com"}}}},
				}},
				"responseObject": map[string]any{"metadata": map[string]any{"name": "alb"}},
				"objectRef":      map[string]any{"name": "alb"},
				"responseStatus": map[string]any{"code": 201},
			},
		},
		{
			name:     "update hostname added",
			wantRule: "update-hostname-added",
			audit: map[string]any{
				"user":          map[string]any{"username": "alice@example.com"},
				"verb":          "patch",
				"objectRef":     map[string]any{"name": "alb"},
				"requestObject": map[string]any{"spec": map[string]any{"hostnames": []any{"app.example.com", "api.example.com"}}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
					"networking.datumapis.com/display-name":    "alb",
					"networking.datumapis.com/activity-change": "added",
					"networking.datumapis.com/activity-field":  "hostname",
					"networking.datumapis.com/activity-name":   "api.example.com",
				}}},
				"responseStatus": map[string]any{"code": 200},
			},
		},
		{
			name:     "update backend",
			wantRule: "update-backend",
			audit: map[string]any{
				"user":          map[string]any{"username": "alice@example.com"},
				"verb":          "update",
				"objectRef":     map[string]any{"name": "alb"},
				"requestObject": map[string]any{"spec": map[string]any{"hostnames": []any{"app.example.com"}}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
					"networking.datumapis.com/display-name":   "alb",
					"networking.datumapis.com/activity-field": "backend",
					"networking.datumapis.com/activity-value": "https://new-origin.example.com",
				}}},
				"responseStatus": map[string]any{"code": 200},
			},
		},
		{
			name:     "metadata-only patch is silent",
			wantRule: "",
			audit: map[string]any{
				"user":           map[string]any{"username": "alice@example.com"},
				"verb":           "patch",
				"objectRef":      map[string]any{"name": "alb"},
				"requestObject":  map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}},
				"responseObject": map[string]any{"metadata": map[string]any{"annotations": displayAnns}},
				"responseStatus": map[string]any{"code": 200},
			},
		},
		{
			name:     "failed create is silent",
			wantRule: "",
			audit: map[string]any{
				"user":           map[string]any{"username": "alice@example.com"},
				"verb":           "create",
				"requestObject":  map[string]any{"spec": map[string]any{"hostnames": []any{"app.example.com"}}},
				"responseObject": map[string]any{"kind": "Status", "code": 403},
				"responseStatus": map[string]any{"code": 403},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantRule, firstMatchingAuditRule(t, pol, tt.audit))
		})
	}

	t.Run("programmed event", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "programmed", firstMatchingEventRule(t, pol, map[string]any{
			"reason": "Programmed",
			"metadata": map[string]any{"annotations": map[string]any{
				"networking.datumapis.com/display-name": "alb",
			}},
			"regarding": map[string]any{"name": "alb"},
		}))
	})
}
