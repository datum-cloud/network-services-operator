// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// inScopeAPIFiles are the api/v1alpha files whose condition reasons reach a
// customer asking about an Application Load Balancer.
var inScopeAPIFiles = []string{
	"httpproxy_types.go",
	"domain_types.go",
	"networkservice_types.go",
}

// outOfScopeAPIFiles are the rest of the networking group. They are declared
// rather than ignored so that "scoped" cannot quietly become "stale": a new
// file has to be argued into one list or the other.
var outOfScopeAPIFiles = map[string]string{
	"edgereachability_types.go":        "hub-side record of which addresses an edge should reach; no customer-facing status",
	"groupversion_info.go":             "scheme registration, no reasons",
	"locationbinding_types.go":         "placement plumbing behind a location, never named by a load balancer",
	"network_types.go":                 "the network a service sits on; its own product surface",
	"networkbinding_types.go":          "network attachment plumbing",
	"networkcontext_types.go":          "network attachment plumbing",
	"networkinterface_types.go":        "an instance's interface; compute's surface",
	"networkinterfaceclaim_types.go":   "an instance's interface; compute's surface",
	"networkpolicy_types.go":           "its own product surface, not reached through a load balancer",
	"subnet_types.go":                  "address management below a network",
	"subnetclaim_types.go":             "address management below a network",
	"trafficprotectionpolicy_types.go": "declares no reasons of its own; the controller writes upstream Gateway API policy reasons",
	"zz_generated.deepcopy.go":         "generated",
}

// extraReasonConsts are reason constants whose NAME does not contain "Reason",
// so the heuristic below would skip them and the catalog could go incomplete
// with this test still green. UnverifiedHostnamesPresent is the live example:
// it is the reason behind every custom hostname awaiting domain verification.
var extraReasonConsts = map[string]bool{
	"UnverifiedHostnamesPresent": true,
}

func apiDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "api", "v1alpha")
}

// TestEveryAPIFileIsClassifiedInOrOutOfScope fails when a file appears in
// api/v1alpha that neither list names, so narrowing the catalog's scope stays a
// decision somebody made rather than one that rotted.
func TestEveryAPIFileIsClassifiedInOrOutOfScope(t *testing.T) {
	entries, err := os.ReadDir(apiDir(t))
	require.NoError(t, err)

	in := make(map[string]bool, len(inScopeAPIFiles))
	for _, f := range inScopeAPIFiles {
		in[f] = true
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if in[name] {
			continue
		}
		if _, ok := outOfScopeAPIFiles[name]; ok {
			continue
		}
		t.Errorf("api/v1alpha/%s is in neither inScopeAPIFiles nor outOfScopeAPIFiles: "+
			"decide whether an Application Load Balancer customer ever sees its reasons, and say so", name)
	}
}

// apiReasons returns every reason string the in-scope API files declare, mapped
// to the constant that declared it.
func apiReasons(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, file := range inScopeAPIFiles {
		path := filepath.Join(apiDir(t), file)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		require.NoError(t, err, "parsing %s", file)

		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				name := vs.Names[0].Name
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				// A condition type is not a reason, even though several share a
				// spelling with one ("Verified", "Programmed").
				if strings.Contains(name, "Condition") {
					continue
				}
				if !strings.Contains(name, "Reason") && !extraReasonConsts[name] {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out[v] = name
			}
		}
	}
	require.NotEmpty(t, out, "parsed no reasons at all; the heuristic has drifted from the API")
	return out
}

// TestCatalogCoversEveryInScopeAPIReason is what stops the catalog falling
// behind the API. A reason with no entry reaches a customer as a bare code with
// no explanation and nobody named to act on it.
func TestCatalogCoversEveryInScopeAPIReason(t *testing.T) {
	covered := map[string]bool{}
	for _, info := range AllReasons() {
		covered[info.Reason] = true
	}

	var missing []string
	for reason, constName := range apiReasons(t) {
		if !covered[reason] {
			missing = append(missing, constName+" ("+reason+")")
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "these API reasons have no catalog entry; classify each one "+
		"user/platform/transient/informational and write the customer's explanation")
}

// TestCatalogEntriesAreUniquePerConditionType pins the key. Reason strings are
// not unique in this API, so an entry may repeat a reason only on a different
// condition type.
func TestCatalogEntriesAreUniquePerConditionType(t *testing.T) {
	seen := map[Key]string{}
	for _, info := range AllReasons() {
		k := Key{ConditionType: info.ConditionType, Reason: info.Reason}
		if prev, dup := seen[k]; dup {
			t.Errorf("two entries for %s on %s (%q and %q); one of them is unreachable",
				info.Reason, info.ConditionType, prev, info.Explanation)
		}
		seen[k] = info.Explanation
	}
}

// TestAmbiguousReasonsAreExplainedPerConditionType is the reason the catalog is
// keyed on the pair at all. "Pending" means three different things here, with
// different advice; a lookup by reason alone would answer all three with
// whichever was registered last.
func TestAmbiguousReasonsAreExplainedPerConditionType(t *testing.T) {
	byReasonCount := map[string][]ReasonInfo{}
	for _, info := range AllReasons() {
		byReasonCount[info.Reason] = append(byReasonCount[info.Reason], info)
	}

	var ambiguous []string
	for reason, infos := range byReasonCount {
		if len(infos) < 2 {
			continue
		}
		ambiguous = append(ambiguous, reason)
		for _, info := range infos {
			got, ok := ExplainReason(info.ConditionType, info.Reason)
			require.True(t, ok, "%s on %s is not reachable by its own key", info.Reason, info.ConditionType)
			assert.Equal(t, info.Explanation, got.Explanation,
				"%s on %s resolves to another condition's meaning", info.Reason, info.ConditionType)
		}
		assert.Len(t, ExplainAnyReason(reason), len(infos),
			"ExplainAnyReason must return every meaning of %q so the caller can say it is ambiguous", reason)
	}

	require.Contains(t, ambiguous, "Pending",
		"Pending is ambiguous in this API; if it stopped being so, this test is no longer guarding anything")
}

// TestCatalogPairsReasonsWithTheConditionsThatCarryThem closes the gap
// TestCatalogCoversEveryInScopeAPIReason leaves open.
//
// That test proves every reason has an entry. It cannot prove the entry names
// the condition the controllers actually set the reason on, and a mispaired
// entry is invisible: ExplainReason simply misses, the walk falls back to the
// uncatalogued path, and the customer gets "Datum reported something this
// assistant does not have an explanation for" for a reason that is catalogued.
//
// The controllers are the authority, so this reads them. A reason assigned to a
// condition variable named programmedCondition belongs on Programmed.
func TestCatalogPairsReasonsWithTheConditionsThatCarryThem(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "controller", "httpproxy_controller.go"))
	require.NoError(t, err)

	// `<something>Condition.Reason = networkingv1alpha.<ReasonConst>`
	assignment := regexp.MustCompile(`(\w+)Condition\.Reason = networkingv1alpha\.(\w+)`)

	conditionTypeOf := map[string]string{
		"accepted":   networkingv1alpha.HTTPProxyConditionAccepted,
		"programmed": networkingv1alpha.HTTPProxyConditionProgrammed,
	}

	constToValue := map[string]string{}
	for value, constName := range apiReasons(t) {
		constToValue[constName] = value
	}

	checked := 0
	for _, m := range assignment.FindAllStringSubmatch(string(src), -1) {
		conditionType, ok := conditionTypeOf[strings.ToLower(m[1])]
		if !ok {
			continue
		}
		reason, ok := constToValue[m[2]]
		if !ok {
			continue
		}
		checked++
		if _, found := ExplainReason(conditionType, reason); !found {
			t.Errorf("the controller sets %s on %s, but the catalog has no entry for that pair; "+
				"a mispaired entry reads as an uncatalogued reason at runtime", reason, conditionType)
		}
	}
	require.Greater(t, checked, 5, "parsed almost no assignments; the pattern has drifted from the controller")
}
