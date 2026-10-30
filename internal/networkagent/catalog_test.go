// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

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
)

var inScopeAPIFiles = []string{
	"network_types.go",
	"networkcontext_types.go",
	"networkbinding_types.go",
	"subnet_types.go",
	"subnetclaim_types.go",
	"networkinterface_types.go",
	"networkinterfaceclaim_types.go",
	"networkservice_types.go",
}

var outOfScopeAPIFiles = map[string]string{
	"portrange_types.go":                        "shared interval value type, no status reasons",
	"domain_types.go":                           "load balancer hostnames; the ALB agent's",
	"edgereachability_types.go":                 "hub-side record with no customer-facing status",
	"groupversion_info.go":                      "scheme registration, no reasons",
	"httpproxy_types.go":                        "load balancers; the ALB agent's",
	"internaldns_types.go":                      "internal DNS service configuration, no status reasons",
	"locationbinding_types.go":                  "which locations a project is offered; surfaces here only as LocationNotAvailable",
	"networkloadbalancer_types.go":              "Network Load Balancer has its own product status, separate from networks and network services",
	"networkloadbalancerbackendhealth_types.go": "cell-local NLB probe status, not a network-agent customer API",
	"networkloadbalancercellintent_types.go":    "hub-side NLB serving intent, not a network-agent customer API",
	"networkloadbalancerroute_types.go":         "Network Load Balancer route status belongs to the load balancer product",
	"networkpolicy_types.go":                    "declares no status reasons",
	"trafficprotectionpolicy_types.go":          "load balancer protection; the ALB agent's",
	"zz_generated.deepcopy.go":                  "generated",
}

var controllerFiles = []string{
	"network_controller.go",
	"networkcontext_controller.go",
	"networkbinding_controller.go",
	"networkpresence_controller.go",
	"subnet_controller.go",
	"subnetclaim_controller.go",
	"networkinterfaceclaim_controller.go",
	"networkservice_controller.go",
	"ipam_errors.go",
}

func repoPath(parts ...string) string {
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

func TestEveryAPIFileIsClassifiedInOrOutOfScope(t *testing.T) {
	entries, err := os.ReadDir(repoPath("api", "v1alpha"))
	require.NoError(t, err)

	in := map[string]bool{}
	for _, f := range inScopeAPIFiles {
		in[f] = true
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, out := outOfScopeAPIFiles[name]; !in[name] && !out {
			t.Errorf("api/v1alpha/%s is in neither inScopeAPIFiles nor outOfScopeAPIFiles: decide whether "+
				"a customer asking about networks ever sees its reasons", name)
		}
	}
}

func apiReasons(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, file := range inScopeAPIFiles {
		f, err := parser.ParseFile(token.NewFileSet(), repoPath("api", "v1alpha", file), nil, 0)
		require.NoError(t, err)
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
				if !ok || lit.Kind != token.STRING || !strings.Contains(name, "Reason") {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out[v] = name
			}
		}
	}
	require.NotEmpty(t, out)
	return out
}

func TestCatalogCoversEveryInScopeAPIReason(t *testing.T) {
	var missing []string
	for reason, constName := range apiReasons(t) {
		if len(ExplainAnyReason(reason)) == 0 {
			missing = append(missing, constName+" ("+reason+")")
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "these API reasons have no catalog entry")
}

var controllerReason = regexp.MustCompile(
	`(?:Reason:\s+"(\w+)",|\.Reason = "(\w+)"|reason:\s+"(\w+)",|allocationFailureReason = "(\w+)"|networkServiceReason\w+\s*=\s*"(\w+)")`)

func TestCatalogCoversEveryReasonTheControllersWrite(t *testing.T) {
	found := map[string]string{}
	for _, file := range controllerFiles {
		src, err := os.ReadFile(repoPath("internal", "controller", file))
		require.NoError(t, err)
		for _, m := range controllerReason.FindAllStringSubmatch(string(src), -1) {
			for _, g := range m[1:] {
				if g != "" {
					found[g] = file
				}
			}
		}
	}
	require.Greater(t, len(found), 15, "parsed almost no reasons; the pattern has drifted from the controllers")

	var missing []string
	for reason, file := range found {
		if len(ExplainAnyReason(reason)) == 0 {
			missing = append(missing, reason+" ("+file+")")
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "the controllers write these reasons and the catalog does not explain them")
}

func TestCatalogEntriesAreUnique(t *testing.T) {
	seen := map[Key]bool{}
	for _, info := range AllReasons() {
		k := keyOf(info)
		assert.False(t, seen[k], "two entries for %+v; one is unreachable", k)
		seen[k] = true
	}
}

func TestEveryEntryIsClassifiedAndExplained(t *testing.T) {
	for _, info := range AllReasons() {
		assert.NotEmpty(t, info.Actionability, "%+v", keyOf(info))
		assert.NotEmpty(t, info.Explanation, "%+v", keyOf(info))
		if info.Actionability != ActionabilityInformational {
			assert.NotEmpty(t, info.Remediation, "%+v says something is wrong and not what to do", keyOf(info))
			assert.NotEmpty(t, info.Scope, "%+v says something is wrong and not how much", keyOf(info))
		}
		if info.Actionability == ActionabilityTransient {
			assert.Positive(t, info.ExpectedDuration, "%+v says wait without saying how long", keyOf(info))
		}
	}
}

func TestEveryPointerHasAnEntry(t *testing.T) {
	for k := range pointerKeys {
		_, ok := ExplainReason(k.Kind, k.ConditionType, k.Reason)
		assert.True(t, ok, "pointer %+v has no catalog entry for when it cannot be followed", k)
	}
}

func TestSameReasonOnDifferentKindsIsExplainedPerKind(t *testing.T) {
	kinds := map[string]bool{}
	for _, info := range ExplainAnyReason("RangeOccupied") {
		kinds[info.Kind] = true
	}
	require.True(t, kinds[KindNetwork] && kinds[KindNetworkContext],
		"RangeOccupied is reported on a network and on a location; if that stopped, this test guards nothing")

	network, _ := ExplainReason(KindNetwork, "Ready", "RangeOccupied")
	location, _ := ExplainReason(KindNetworkContext, "Ready", "RangeOccupied")
	assert.Equal(t, ActionabilityUser, network.Actionability)
	assert.Equal(t, ActionabilityTransient, location.Actionability)
}
