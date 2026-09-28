// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	activityv1alpha1 "go.miloapis.com/activity/pkg/apis/activity/v1alpha1"
)

// These fixtures use the array-shaped requestObject recorded for JSON Patch
// audit events, including the hostname-removal request that failed in production.
func TestPolicies_JSONPatch(t *testing.T) {
	t.Parallel()
	policies := []struct {
		file           string
		silentMetadata bool
		wantRule       string
	}{
		{"httpproxy", true, "update-name"},
		{"trafficprotectionpolicy", true, "update-fallback"},
		{"securitypolicy", true, "update-fallback"},
		{"gateway", false, "update-fallback"},
		{"httproute", false, "update-fallback"},
		{"backendtlspolicy", false, "update-fallback"},
		{"connector", false, "update-fallback"},
		{"connectoradvertisement", false, "update-fallback"},
		{"domain", false, "update-fallback"},
	}
	tests := []struct {
		name        string
		request     string
		changesSpec bool
	}{
		{"remove all hostnames", `[{"op":"replace","path":"/spec/hostnames","value":[]}]`, true},
		{"add hostname", `[{"op":"add","path":"/spec/hostnames/-","value":"app.example.com"}]`, true},
		{"remove hostname", `[{"op":"remove","path":"/spec/hostnames/0"}]`, true},
		{"replace spec", `[{"op":"replace","path":"/spec","value":{}}]`, true},
		{"replace whole resource", `[{"op":"replace","path":"","value":{"spec":{}}}]`, true},
		{"copy into spec", `[{"op":"copy","from":"/metadata/annotations/example.com~1hostname","path":"/spec/hostnames/0"}]`, true},
		{"move out of spec", `[{"op":"move","from":"/spec/hostnames/0","path":"/metadata/annotations/example.com~1hostname"}]`, true},
		{"test then modify spec", `[{"op":"test","path":"/metadata/resourceVersion","value":"42"},{"op":"replace","path":"/spec/hostnames","value":[]}]`, true},
		{"metadata only", `[{"op":"add","path":"/metadata/labels/example.com~1team","value":"dev"}]`, false},
		{"test spec only", `[{"op":"test","path":"/spec/hostnames","value":[]}]`, false},
		{"test spec and modify metadata", `[{"op":"test","path":"/spec/hostnames","value":[]},{"op":"add","path":"/metadata/labels/example.com~1team","value":"dev"}]`, false},
		{"copy out of spec", `[{"op":"copy","from":"/spec/hostnames/0","path":"/metadata/annotations/example.com~1hostname"}]`, false},
		{"spec prefix is not spec", `[{"op":"add","path":"/specification","value":{}}]`, false},
		{"empty patch", `[]`, false},
	}
	for _, p := range policies {
		t.Run(p.file, func(t *testing.T) {
			t.Parallel()
			pol := loadPolicy(t, "config/milo/activity/policies/"+p.file+"-policy.yaml")
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					var request any
					require.NoError(t, json.Unmarshal([]byte(tt.request), &request))
					audit := patchAudit(request, map[string]any{})
					rule := firstMatchingAuditRule(t, pol, audit)
					if tt.changesSpec || !p.silentMetadata {
						assert.Equal(t, p.wantRule, rule, "successful patch must produce an activity")
						assert.NotEmpty(t, auditSummary(t, pol, rule, audit))
					} else {
						assert.Empty(t, rule, "non-mutating and metadata-only patches must stay silent")
					}
				})
			}
			for _, exclusion := range []string{"system user", "failed request", "status subresource"} {
				t.Run(exclusion, func(t *testing.T) {
					audit := patchAudit([]any{map[string]any{"op": "replace", "path": "/spec/hostnames", "value": []any{}}}, map[string]any{})
					switch exclusion {
					case "system user":
						audit["user"] = map[string]any{"username": "system:serviceaccount:default:controller"}
					case "failed request":
						audit["responseStatus"] = map[string]any{"code": 422}
					case "status subresource":
						audit["objectRef"] = map[string]any{"name": "alb", "subresource": "status"}
					}
					assert.Empty(t, firstMatchingAuditRule(t, pol, audit))
				})
			}
		})
	}
}

func patchAudit(request any, annotations map[string]any) map[string]any {
	return map[string]any{
		"user":           map[string]any{"username": "alice@example.com"},
		"verb":           "patch",
		"objectRef":      map[string]any{"name": "alb"},
		"requestObject":  request,
		"responseObject": map[string]any{"metadata": map[string]any{"name": "alb", "annotations": annotations}},
		"responseStatus": map[string]any{"code": 200},
	}
}

func TestHTTPProxyPolicy_JSONPatchAnnotations(t *testing.T) {
	t.Parallel()
	pol := loadPolicy(t, "config/milo/activity/policies/httpproxy-policy.yaml")
	for _, tt := range []struct {
		name        string
		patch       string
		field       string
		change      string
		want        string
		wantSummary string
	}{
		{"hostname removal", `[{"op":"replace","path":"/spec/hostnames","value":[]}]`, "hostname", "removed", "update-hostname-removed", "Alice removed hostname api.example.com from alb"},
		{"hostname addition", `[{"op":"add","path":"/spec/hostnames/-","value":"api.example.com"}]`, "hostname", "added", "update-hostname-added", "Alice added a custom hostname api.example.com to alb"},
		{"rename", `[{"op":"replace","path":"/metadata/annotations/app.kubernetes.io~1name","value":"New name"}]`, "display-name", "updated", "update-display-name", "Alice renamed api.example.com to New name"},
		{"metadata patch with stale hostname annotations", `[{"op":"add","path":"/metadata/labels/example.com~1team","value":"dev"}]`, "hostname", "removed", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var request any
			require.NoError(t, json.Unmarshal([]byte(tt.patch), &request))
			audit := patchAudit(request, map[string]any{
				"networking.datumapis.com/display-name":    "alb",
				"networking.datumapis.com/activity-field":  tt.field,
				"networking.datumapis.com/activity-change": tt.change,
				"networking.datumapis.com/activity-name":   "api.example.com",
				"networking.datumapis.com/activity-value":  "New name",
			})
			rule := firstMatchingAuditRule(t, pol, audit)
			assert.Equal(t, tt.want, rule)
			if tt.want != "" {
				assert.Equal(t, tt.wantSummary, auditSummary(t, pol, rule, audit))
			}
		})
	}
}

// Evaluate the selected policy's summary with the processor's string/dynamic
// link signature. The stub returns display text; link collection is outside
// these policy tests. This catches missing fields and invalid summary types.
func auditSummary(t *testing.T, pol activityv1alpha1.ActivityPolicy, ruleName string, audit map[string]any) string {
	t.Helper()
	env, err := cel.NewEnv(
		cel.Variable("audit", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("actor", cel.StringType),
		cel.Function("link", cel.Overload("link_string_dyn",
			[]*cel.Type{cel.StringType, cel.DynType}, cel.StringType,
			cel.BinaryBinding(func(displayText, resourceRef ref.Val) ref.Val { return displayText }))),
	)
	require.NoError(t, err)
	var summary string
	for _, rule := range pol.Spec.AuditRules {
		if rule.Name == ruleName {
			summary = rule.Summary
			break
		}
	}
	require.NotEmpty(t, summary)
	rendered := regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`).ReplaceAllStringFunc(summary, func(part string) string {
		expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(part, "{{"), "}}"))
		ast, issues := env.Compile(expr)
		require.NoError(t, issues.Err())
		program, err := env.Program(ast)
		require.NoError(t, err)
		value, _, err := program.Eval(map[string]any{"audit": audit, "actor": "Alice"})
		require.NoError(t, err, "rule %s summary expression %s", ruleName, expr)
		text, ok := value.Value().(string)
		require.True(t, ok, "summary expressions must produce strings")
		return text
	})
	require.NotContains(t, rendered, "{{", "summary must not retain unevaluated expressions")
	return rendered
}
