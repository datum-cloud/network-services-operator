package interpreters

import (
	"testing"

	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/resourceinterpreter/customized/declarative/luavm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	httpRouteRule  = "gateway.networking.k8s.io-httproute"
	edgeController = "gateway.envoyproxy.io/datum-downstream-gateway"
)

type parentFix struct {
	name         string
	accepted     string
	resolvedRefs string
}

func routeStatus(i int, parents ...parentFix) map[string]any {
	var ps []any
	for _, p := range parents {
		accepted := cond("Accepted", p.accepted, i)
		resolved := cond("ResolvedRefs", p.resolvedRefs, i)
		resolved["reason"] = map[string]string{"True": "ResolvedRefs", "False": "BackendNotFound", "Unknown": "Pending"}[p.resolvedRefs]
		ps = append(ps, map[string]any{
			"parentRef":      map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": p.name, "namespace": "ns-a"},
			"controllerName": edgeController,
			"conditions":     []any{accepted, resolved},
		})
	}
	status := map[string]any{}
	if ps != nil {
		status["parents"] = ps
	}
	return status
}

func runRoute(t *testing.T, items []workv1alpha2.AggregatedStatusItem) *unstructured.Unstructured {
	t.Helper()
	rule := loadRules(t)[httpRouteRule]
	return aggregate(t, luavm.New(false, 1), rule, desired(rule), items)
}

func parent(obj *unstructured.Unstructured, name string) map[string]any {
	ps, _, _ := unstructured.NestedSlice(obj.Object, "status", "parents")
	for _, p := range ps {
		if m := p.(map[string]any); m["parentRef"].(map[string]any)["name"] == name {
			return m
		}
	}
	return nil
}

func parentCondition(p map[string]any, typ string) map[string]any {
	return condition(&unstructured.Unstructured{Object: map[string]any{"p": p}}, []string{"p", "conditions"}, typ)
}

func TestHTTPRoute_AllTrue(t *testing.T) {
	got := runRoute(t, []workv1alpha2.AggregatedStatusItem{
		item("edge-a", routeStatus(1, parentFix{"gw-a", "True", "True"})),
		item("edge-b", routeStatus(2, parentFix{"gw-a", "True", "True"})),
	})
	p := parent(got, "gw-a")
	require.NotNil(t, p)
	assert.Equal(t, edgeController, p["controllerName"])
	c := parentCondition(p, "Accepted")
	require.NotNil(t, c)
	assert.Equal(t, "True", c["status"])
	assert.Equal(t, "Accepted on 2 of 2 clusters", c["message"])
}

func TestHTTPRoute_OneFalse(t *testing.T) {
	got := runRoute(t, []workv1alpha2.AggregatedStatusItem{
		item("edge-a", routeStatus(1, parentFix{"gw-a", "True", "True"})),
		item("edge-b", routeStatus(2, parentFix{"gw-a", "True", "False"})),
	})
	c := parentCondition(parent(got, "gw-a"), "ResolvedRefs")
	require.NotNil(t, c)
	assert.Equal(t, "False", c["status"])
	assert.Equal(t, "BackendNotFound", c["reason"])
	assert.Equal(t, "edge-b: from member", c["message"])
}

func TestHTTPRoute_ParentMissingOnOne(t *testing.T) {
	got := runRoute(t, []workv1alpha2.AggregatedStatusItem{
		item("edge-a", routeStatus(1, parentFix{"gw-a", "True", "True"})),
		item("edge-b", routeStatus(2)),
	})
	p := parent(got, "gw-a")
	require.NotNil(t, p)
	c := parentCondition(p, "Accepted")
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Pending", c["reason"])
	assert.Equal(t, "Waiting on edge-b", c["message"])
}

func TestHTTPRoute_TwoParents(t *testing.T) {
	got := runRoute(t, []workv1alpha2.AggregatedStatusItem{
		item("edge-a", routeStatus(1, parentFix{"gw-a", "True", "True"}, parentFix{"gw-b", "False", "True"})),
		item("edge-b", routeStatus(2, parentFix{"gw-a", "True", "True"}, parentFix{"gw-b", "True", "True"})),
	})
	ps, _, _ := unstructured.NestedSlice(got.Object, "status", "parents")
	require.Len(t, ps, 2)
	assert.Equal(t, "gw-a", ps[0].(map[string]any)["parentRef"].(map[string]any)["name"])
	assert.Equal(t, "gw-b", ps[1].(map[string]any)["parentRef"].(map[string]any)["name"])
	assert.Equal(t, "True", parentCondition(parent(got, "gw-a"), "Accepted")["status"])
	assert.Equal(t, "False", parentCondition(parent(got, "gw-b"), "Accepted")["status"])
	assert.Equal(t, "edge-a: from member", parentCondition(parent(got, "gw-b"), "Accepted")["message"])
}

func TestHTTPRoute_EmptyList(t *testing.T) {
	rule := loadRules(t)[httpRouteRule]
	got := aggregate(t, luavm.New(false, 1), rule, desired(rule), nil)
	assert.Equal(t, desired(rule), got.Object)
}
