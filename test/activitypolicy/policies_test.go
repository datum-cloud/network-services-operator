package activitypolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"sigs.k8s.io/yaml"
)

// These tests guard the ActivityPolicy audit rules under
// config/milo/activity/policies against a single defect with two symptoms:
// create/update rules that fire on FAILED (non-2xx) requests. On a rejected
// request the audit responseObject is a metav1.Status, so a summary that
// dereferences audit.responseObject.<leaf> throws and the event is lost to the
// DLQ (DLQSlowLeak); the same rule also emits a false "created"/"updated"
// activity for an attempt that never succeeded. The fix gates every create and
// update rule's match on audit.responseStatus.code in [200,300).

const policiesGlob = "../../config/milo/activity/policies/*-policy.yaml"

type auditRule struct {
	Name    string `json:"name"`
	Match   string `json:"match"`
	Summary string `json:"summary"`
}

type policy struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		AuditRules []auditRule `json:"auditRules"`
	} `json:"spec"`
}

func loadPolicies(t *testing.T) []policy {
	t.Helper()
	paths, err := filepath.Glob(policiesGlob)
	if err != nil {
		t.Fatalf("glob %q: %v", policiesGlob, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no policy files matched %q", policiesGlob)
	}
	policies := make([]policy, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var pol policy
		if err := yaml.Unmarshal(b, &pol); err != nil {
			t.Fatalf("unmarshal %s: %v", p, err)
		}
		if pol.Metadata.Name == "" {
			pol.Metadata.Name = filepath.Base(p)
		}
		policies = append(policies, pol)
	}
	return policies
}

// verbOf classifies a rule by the write verb its match targets.
func verbOf(match string) string {
	switch {
	case strings.Contains(match, "audit.verb == 'create'"):
		return "create"
	case strings.Contains(match, "audit.verb in ['update', 'patch']"):
		return "update"
	case strings.Contains(match, "audit.verb == 'delete'"):
		return "delete"
	default:
		return "other"
	}
}

func gatesOn2xx(match string) bool {
	return strings.Contains(match, "audit.responseStatus.code >= 200") &&
		strings.Contains(match, "audit.responseStatus.code < 300")
}

func newEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("audit", cel.DynType))
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	return env
}

func evalMatch(t *testing.T, env *cel.Env, match string, audit map[string]any) bool {
	t.Helper()
	ast, iss := env.Compile(match)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile %q: %v", match, iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program %q: %v", match, err)
	}
	out, _, err := prg.Eval(map[string]any{"audit": audit})
	if err != nil {
		t.Fatalf("eval %q: %v", match, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		t.Fatalf("match %q did not evaluate to bool, got %T", match, out.Value())
	}
	return b
}

// auditEvent builds an audit payload for the given verb and response code.
// A 2xx response carries a real created/updated object; a non-2xx response
// carries a metav1.Status (no metadata.name, no spec), as the API server sends
// on a rejected write.
func auditEvent(verb string, code int) map[string]any {
	var responseObject map[string]any
	if code >= 200 && code < 300 {
		responseObject = map[string]any{
			"metadata": map[string]any{"name": "obj-1"},
			"spec": map[string]any{
				"domainName": "example.datumchainsaw.art",
				"hostnames":  []any{"example.datumchainsaw.art"},
			},
		}
	} else {
		responseObject = map[string]any{
			"kind":       "Status",
			"apiVersion": "v1",
			"status":     "Failure",
			"reason":     "Forbidden",
			"code":       code,
		}
	}
	return map[string]any{
		"user": map[string]any{"username": "alice@example.com"},
		"verb": verb,
		"requestObject": map[string]any{
			"spec": map[string]any{
				"domainName": "example.datumchainsaw.art",
				"hostnames":  []any{"example.datumchainsaw.art"},
			},
		},
		"responseObject": responseObject,
		"objectRef":      map[string]any{"name": "obj-1"},
		"responseStatus": map[string]any{"code": code},
	}
}

func failCodeFor(verb string) int {
	if verb == "create" {
		return 403
	}
	return 409
}

func successCodeFor(verb string) int {
	if verb == "create" {
		return 201
	}
	return 200
}

// Structural guard: every create/update rule must gate on a 2xx response.
func TestCreateUpdateRulesGateOn2xx(t *testing.T) {
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			v := verbOf(r.Match)
			if v != "create" && v != "update" {
				continue
			}
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				if !gatesOn2xx(r.Match) {
					t.Errorf("%s rule %q (%s) is not gated on a 2xx response:\n  %s",
						pol.Metadata.Name, r.Name, v, r.Match)
				}
			})
		}
	}
}

// Semantic guard: create/update rules must NOT match a failed request, and MUST
// still match the successful one — the two properties that fix the DLQ leak and
// the false-activity emission together.
func TestCreateUpdateRulesFireOnlyOnSuccess(t *testing.T) {
	env := newEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			v := verbOf(r.Match)
			if v != "create" && v != "update" {
				continue
			}
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				if got := evalMatch(t, env, r.Match, auditEvent(v, failCodeFor(v))); got {
					t.Errorf("%s rule %q matched a failed %s (code %d); it would DLQ / emit a false activity",
						pol.Metadata.Name, r.Name, v, failCodeFor(v))
				}
				if got := evalMatch(t, env, r.Match, auditEvent(v, successCodeFor(v))); !got {
					t.Errorf("%s rule %q did not match a successful %s (code %d); the gate broke the happy path",
						pol.Metadata.Name, r.Name, v, successCodeFor(v))
				}
			})
		}
	}
}

// Every rule's match must compile — catches CEL typos before they reach milo.
func TestAllMatchesCompile(t *testing.T) {
	env := newEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			if strings.TrimSpace(r.Match) == "" {
				t.Errorf("%s rule %q has an empty match", pol.Metadata.Name, r.Name)
				continue
			}
			if _, iss := env.Compile(r.Match); iss != nil && iss.Err() != nil {
				t.Errorf("%s rule %q match does not compile: %v", pol.Metadata.Name, r.Name, iss.Err())
			}
		}
	}
}
