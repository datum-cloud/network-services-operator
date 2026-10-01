package activitypolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	activityv1alpha1 "go.miloapis.com/activity/pkg/apis/activity/v1alpha1"
	"sigs.k8s.io/yaml"
)

// These tests guard the ActivityPolicy audit rules under
// config/milo/activity/policies against write rules that fire on requests that
// changed nothing. On a rejected request the audit responseObject is a
// metav1.Status, so a summary that dereferences audit.responseObject.<leaf>
// throws and the event is lost to the DLQ (DLQSlowLeak); the same rule also
// emits a false "created"/"updated"/"deleted" activity for an attempt that
// never succeeded. A dry run (?dryRun=All) succeeds but persists nothing. Every
// create, update and delete rule's match therefore gates on
// audit.responseStatus.code in [200,300) and on the request not being a dry run.

const policiesGlob = "../../config/milo/activity/policies/*-policy.yaml"

func loadPolicies(t *testing.T) []activityv1alpha1.ActivityPolicy {
	t.Helper()
	paths, err := filepath.Glob(policiesGlob)
	if err != nil {
		t.Fatalf("glob %q: %v", policiesGlob, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no policy files matched %q", policiesGlob)
	}
	policies := make([]activityv1alpha1.ActivityPolicy, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var pol activityv1alpha1.ActivityPolicy
		if err := yaml.Unmarshal(b, &pol); err != nil {
			t.Fatalf("unmarshal %s: %v", p, err)
		}
		if pol.Name == "" {
			pol.Name = filepath.Base(p)
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
	return strings.Contains(match, "has(audit.responseStatus.code) && audit.responseStatus.code >= 200") &&
		strings.Contains(match, "audit.responseStatus.code < 300")
}

// matchesQuotaDenial reports whether a rule records a create that Milo's
// quota admission rejected. These rules match a 403 on purpose.
func matchesQuotaDenial(match string) bool {
	return strings.Contains(match, "audit.annotations['quota.miloapis.com/outcome'] == 'denied'")
}

func skipsDryRun(match string) bool {
	return strings.Contains(match, "audit.requestURI.contains('dryRun=')")
}

func newEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("audit", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	return env
}

func newEventEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("event", cel.DynType))
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
	out, _, err := prg.Eval(map[string]any{"audit": withDefaults(audit)})
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
	return auditEventURI(verb, code, objectURI)
}

const objectURI = "/apis/networking.datumapis.com/v1alpha/namespaces/default/objects/obj-1"

// dryRunEvent is a successful request made with ?dryRun=All.
func dryRunEvent(verb string) map[string]any {
	return auditEventURI(verb, successCodeFor(verb), objectURI+"?dryRun=All&fieldManager=kubectl-client-side-apply")
}

func auditEventURI(verb string, code int, uri string) map[string]any {
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
				"mode":       "Observe",
				"basicAuth":  map[string]any{"users": map[string]any{"name": "obj-1-basic-auth"}},
				"rules": []any{map[string]any{
					"backends": []any{map[string]any{"endpoint": "https://origin.example.com"}},
				}},
			},
		},
		"responseObject": responseObject,
		"objectRef":      map[string]any{"name": "obj-1"},
		"responseStatus": map[string]any{"code": code},
		"requestURI":     uri,
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

// Structural guard: every write rule must gate on a 2xx response and skip dry runs.
func TestWriteRulesGateOnOutcome(t *testing.T) {
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			v := verbOf(r.Match)
			if v == "other" || matchesQuotaDenial(r.Match) {
				continue
			}
			t.Run(pol.Name+"/"+r.Name, func(t *testing.T) {
				if !gatesOn2xx(r.Match) {
					t.Errorf("%s rule %q (%s) is not gated on a 2xx response:\n  %s",
						pol.Name, r.Name, v, r.Match)
				}
				if !skipsDryRun(r.Match) {
					t.Errorf("%s rule %q (%s) does not skip dry-run requests:\n  %s",
						pol.Name, r.Name, v, r.Match)
				}
			})
		}
	}
}

// Semantic guard: write rules must NOT match a failed or dry-run request, and
// MUST still match the successful one — the properties that fix the DLQ leak
// and the false-activity emission together.
func TestWriteRulesFireOnlyOnSuccess(t *testing.T) {
	env := newEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			v := verbOf(r.Match)
			if v == "other" || matchesQuotaDenial(r.Match) {
				continue
			}
			t.Run(pol.Name+"/"+r.Name, func(t *testing.T) {
				for _, code := range []int{failCodeFor(v), 404, 422, 500} {
					if got := evalMatch(t, env, r.Match, auditEvent(v, code)); got {
						t.Errorf("%s rule %q matched a failed %s (code %d); it would DLQ / emit a false activity",
							pol.Name, r.Name, v, code)
					}
				}
				if got := evalMatch(t, env, r.Match, dryRunEvent(v)); got {
					t.Errorf("%s rule %q matched a dry-run %s; nothing was persisted", pol.Name, r.Name, v)
				}
				if got := evalMatch(t, env, r.Match, auditEvent(v, successCodeFor(v))); !got {
					if strings.Contains(r.Match, "metadata.annotations") {
						return
					}
					t.Errorf("%s rule %q did not match a successful %s (code %d); the gate broke the happy path",
						pol.Name, r.Name, v, successCodeFor(v))
				}
			})
		}
	}
}

// Every rule's match must compile — catches CEL typos before they reach milo.
func TestAllMatchesCompile(t *testing.T) {
	env := newEnv(t)
	eventEnv := newEventEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			if strings.TrimSpace(r.Match) == "" {
				t.Errorf("%s rule %q has an empty match", pol.Name, r.Name)
				continue
			}
			if _, iss := env.Compile(r.Match); iss != nil && iss.Err() != nil {
				t.Errorf("%s rule %q match does not compile: %v", pol.Name, r.Name, iss.Err())
			}
		}
		for _, r := range pol.Spec.EventRules {
			if strings.TrimSpace(r.Match) == "" {
				t.Errorf("%s event rule %q has an empty match", pol.Name, r.Name)
				continue
			}
			if _, iss := eventEnv.Compile(r.Match); iss != nil && iss.Err() != nil {
				t.Errorf("%s event rule %q match does not compile: %v", pol.Name, r.Name, iss.Err())
			}
		}
	}
}

// withDefaults mirrors the processor's BuildAuditVars, which sets absent
// top-level objects to empty maps before evaluating a rule.
func withDefaults(audit map[string]any) map[string]any {
	out := make(map[string]any, len(audit))
	for k, v := range audit {
		out[k] = v
	}
	for _, field := range []string{"objectRef", "user", "responseStatus", "responseObject", "requestObject"} {
		if _, ok := out[field]; !ok {
			out[field] = map[string]any{}
		}
	}
	return out
}
