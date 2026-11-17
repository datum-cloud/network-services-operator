package interpreters

import (
	"encoding/json"
	"fmt"
	"testing"

	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/resourceinterpreter/customized/declarative/luavm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const gatewayRule = "gateway.networking.k8s.io-gateway"

type listenerFix struct {
	name           string
	programmed     string
	attachedRoutes int
}

func cond(typ, status string, i int) map[string]any {
	reason := map[string]string{"True": "Programmed", "False": "NoResources", "Unknown": "Pending"}[status]
	if typ == "Accepted" {
		reason = "Accepted"
	}
	return map[string]any{
		"type": typ, "status": status, "reason": reason, "message": "from member",
		"observedGeneration": int64(1), "lastTransitionTime": fmt.Sprintf("2026-01-0%dT00:00:00Z", i),
	}
}

func gatewayStatus(i int, programmed string, listeners ...listenerFix) map[string]any {
	conds := []any{cond("Accepted", "True", i)}
	if programmed != "" {
		conds = append(conds, cond("Programmed", programmed, i))
	}
	status := map[string]any{
		"addresses":  []any{map[string]any{"type": "IPAddress", "value": "192.0.2.1"}},
		"conditions": conds,
	}
	var ls []any
	for _, l := range listeners {
		ls = append(ls, map[string]any{
			"name":           l.name,
			"supportedKinds": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute"}},
			"attachedRoutes": int64(l.attachedRoutes),
			"conditions":     []any{cond("Programmed", l.programmed, i)},
		})
	}
	if ls != nil {
		status["listeners"] = ls
	}
	return status
}

func gatewayItems(t *testing.T, programmed ...string) []workv1alpha2.AggregatedStatusItem {
	t.Helper()
	var out []workv1alpha2.AggregatedStatusItem
	for i, p := range programmed {
		out = append(out, item(fmt.Sprintf("edge-%c", 'a'+i), gatewayStatus(i+1, p)))
	}
	return out
}

func runGateway(t *testing.T, items []workv1alpha2.AggregatedStatusItem) *unstructured.Unstructured {
	t.Helper()
	rule := loadRules(t)[gatewayRule]
	return aggregate(t, luavm.New(false, 1), rule, desired(rule), items)
}

func programmed(obj *unstructured.Unstructured) map[string]any {
	return condition(obj, []string{"status", "conditions"}, "Programmed")
}

func TestGateway_AllTrue(t *testing.T) {
	got := runGateway(t, gatewayItems(t, "True", "True", "True"))
	c := programmed(got)
	require.NotNil(t, c)
	assert.Equal(t, "True", c["status"])
	assert.Equal(t, "Programmed", c["reason"])
	assert.Equal(t, "Programmed on 3 of 3 clusters", c["message"])
	assert.Equal(t, "2026-01-03T00:00:00Z", c["lastTransitionTime"])
	addrs, _, _ := unstructured.NestedSlice(got.Object, "status", "addresses")
	require.Len(t, addrs, 1)
	assert.Equal(t, "192.0.2.1", addrs[0].(map[string]any)["value"])
}

func TestGateway_OneFalse(t *testing.T) {
	c := programmed(runGateway(t, gatewayItems(t, "True", "False", "True")))
	require.NotNil(t, c)
	assert.Equal(t, "False", c["status"])
	assert.Equal(t, "NoResources", c["reason"])
	assert.Equal(t, "edge-b: from member", c["message"])
}

func TestGateway_TwoFalse(t *testing.T) {
	c := programmed(runGateway(t, gatewayItems(t, "True", "False", "False")))
	require.NotNil(t, c)
	assert.Equal(t, "False", c["status"])
	assert.Equal(t, "edge-b: from member (also edge-c)", c["message"])
}

func TestGateway_OneNoStatus(t *testing.T) {
	items := gatewayItems(t, "True", "True")
	items = append(items, item("edge-c", nil))
	c := programmed(runGateway(t, items))
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Pending", c["reason"])
	assert.Equal(t, "Waiting on edge-c", c["message"])
}

func TestGateway_OneEmptyStatus(t *testing.T) {
	items := gatewayItems(t, "True", "True")
	items = append(items, item("edge-c", map[string]any{}))
	c := programmed(runGateway(t, items))
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Pending", c["reason"])
	assert.Equal(t, "Waiting on edge-c", c["message"])
}

func TestGateway_OneUnknown(t *testing.T) {
	c := programmed(runGateway(t, gatewayItems(t, "True", "Unknown", "True")))
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Pending", c["reason"])
	assert.Equal(t, "Waiting on edge-b", c["message"])
}

func TestGateway_MissingConditionType(t *testing.T) {
	got := runGateway(t, gatewayItems(t, "True", "", "True"))
	c := programmed(got)
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Waiting on edge-b", c["message"])
	a := condition(got, []string{"status", "conditions"}, "Accepted")
	require.NotNil(t, a)
	assert.Equal(t, "True", a["status"])
	assert.Equal(t, "Accepted on 3 of 3 clusters", a["message"])
}

func TestGateway_SingleCluster(t *testing.T) {
	c := programmed(runGateway(t, gatewayItems(t, "True")))
	require.NotNil(t, c)
	assert.Equal(t, "True", c["status"])
	assert.Equal(t, "Programmed on 1 of 1 clusters", c["message"])
}

func listener(obj *unstructured.Unstructured, name string) map[string]any {
	ls, _, _ := unstructured.NestedSlice(obj.Object, "status", "listeners")
	for _, l := range ls {
		if m := l.(map[string]any); m["name"] == name {
			return m
		}
	}
	return nil
}

func listenerCondition(l map[string]any, typ string) map[string]any {
	return condition(&unstructured.Unstructured{Object: map[string]any{"l": l}}, []string{"l", "conditions"}, typ)
}

func TestGateway_ListenerMissingOnOne(t *testing.T) {
	items := []workv1alpha2.AggregatedStatusItem{
		item("edge-a", gatewayStatus(1, "True", listenerFix{"http", "True", 1}, listenerFix{"https", "True", 1})),
		item("edge-b", gatewayStatus(2, "True", listenerFix{"http", "True", 1})),
	}
	got := runGateway(t, items)
	https := listener(got, "https")
	require.NotNil(t, https)
	c := listenerCondition(https, "Programmed")
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Waiting on edge-b", c["message"])
	http := listener(got, "http")
	require.NotNil(t, http)
	assert.Equal(t, "True", listenerCondition(http, "Programmed")["status"])
	assert.Equal(t, "Programmed on 2 of 2 clusters", listenerCondition(http, "Programmed")["message"])
}

func TestGateway_ListenerAttachedRoutesFromFirst(t *testing.T) {
	items := []workv1alpha2.AggregatedStatusItem{
		item("edge-a", gatewayStatus(1, "True", listenerFix{"http", "True", 2})),
		item("edge-b", gatewayStatus(2, "True", listenerFix{"http", "True", 5})),
	}
	http := listener(runGateway(t, items), "http")
	require.NotNil(t, http)
	assert.EqualValues(t, 2, http["attachedRoutes"])
	assert.Len(t, http["supportedKinds"], 1)
}

func TestGateway_EmptyConditionsRoundTrip(t *testing.T) {
	items := []workv1alpha2.AggregatedStatusItem{
		item("edge-a", map[string]any{"conditions": []any{}}),
		item("edge-b", map[string]any{"conditions": []any{}}),
	}
	got := runGateway(t, items)
	raw, err := json.Marshal(got.Object)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"conditions":{}`)
	_, found, _ := unstructured.NestedFieldNoCopy(got.Object, "status", "conditions")
	assert.False(t, found, "an empty list is dropped by the VM, never emitted as an object")
}

func TestGateway_ObservedGenerationFromFirst(t *testing.T) {
	a := gatewayStatus(1, "True")
	a["conditions"].([]any)[1].(map[string]any)["observedGeneration"] = int64(4)
	b := gatewayStatus(2, "True")
	b["conditions"].([]any)[1].(map[string]any)["observedGeneration"] = int64(7)
	c := programmed(runGateway(t, []workv1alpha2.AggregatedStatusItem{item("edge-a", a), item("edge-b", b)}))
	require.NotNil(t, c)
	assert.EqualValues(t, 4, c["observedGeneration"])
}

func TestGateway_EmptyList(t *testing.T) {
	rule := loadRules(t)[gatewayRule]
	want := desired(rule)
	got := aggregate(t, luavm.New(false, 1), rule, desired(rule), nil)
	assert.Equal(t, want, got.Object)
}
