package interpreters

import (
	"testing"

	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/resourceinterpreter/customized/declarative/luavm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var ancestorRules = []string{
	"gateway.networking.k8s.io-backendtlspolicy",
	"gateway.envoyproxy.io-backendtrafficpolicy",
	"gateway.envoyproxy.io-securitypolicy",
}

func ancestorStatus(i int, accepted string) map[string]any {
	if accepted == "" {
		return map[string]any{}
	}
	return map[string]any{"ancestors": []any{map[string]any{
		"ancestorRef":    map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "gw-a", "namespace": "ns-a"},
		"controllerName": edgeController,
		"conditions":     []any{cond("Accepted", accepted, i)},
	}}}
}

func runRule(t *testing.T, name string, items []workv1alpha2.AggregatedStatusItem) *unstructured.Unstructured {
	t.Helper()
	rule := loadRules(t)[name]
	require.NotNil(t, rule, name)
	return aggregate(t, luavm.New(false, 1), rule, desired(rule), items)
}

func ancestorCondition(obj *unstructured.Unstructured, typ string) map[string]any {
	as, _, _ := unstructured.NestedSlice(obj.Object, "status", "ancestors")
	if len(as) == 0 {
		return nil
	}
	return condition(&unstructured.Unstructured{Object: map[string]any{"a": as[0]}}, []string{"a", "conditions"}, typ)
}

func TestAncestorPolicies_AllTrue(t *testing.T) {
	for _, name := range ancestorRules {
		t.Run(name, func(t *testing.T) {
			got := runRule(t, name, []workv1alpha2.AggregatedStatusItem{
				item("edge-a", ancestorStatus(1, "True")), item("edge-b", ancestorStatus(2, "True")),
			})
			c := ancestorCondition(got, "Accepted")
			require.NotNil(t, c)
			assert.Equal(t, "True", c["status"])
			assert.Equal(t, "Accepted on 2 of 2 clusters", c["message"])
		})
	}
}

func TestAncestorPolicies_OneFalse(t *testing.T) {
	for _, name := range ancestorRules {
		t.Run(name, func(t *testing.T) {
			got := runRule(t, name, []workv1alpha2.AggregatedStatusItem{
				item("edge-a", ancestorStatus(1, "True")), item("edge-b", ancestorStatus(2, "False")),
			})
			c := ancestorCondition(got, "Accepted")
			require.NotNil(t, c)
			assert.Equal(t, "False", c["status"])
			assert.Equal(t, "edge-b: from member", c["message"])
		})
	}
}

func TestAncestorPolicies_AncestorMissingOnOne(t *testing.T) {
	for _, name := range ancestorRules {
		t.Run(name, func(t *testing.T) {
			got := runRule(t, name, []workv1alpha2.AggregatedStatusItem{
				item("edge-a", ancestorStatus(1, "True")), item("edge-b", ancestorStatus(2, "")),
			})
			c := ancestorCondition(got, "Accepted")
			require.NotNil(t, c)
			assert.Equal(t, "Unknown", c["status"])
			assert.Equal(t, "Pending", c["reason"])
			assert.Equal(t, "Waiting on edge-b", c["message"])
		})
	}
}

const backendRule = "gateway.envoyproxy.io-backend"

func backendStatus(i int, accepted string) map[string]any {
	return map[string]any{"conditions": []any{cond("Accepted", accepted, i)}}
}

func TestBackend_AllTrue(t *testing.T) {
	got := runRule(t, backendRule, []workv1alpha2.AggregatedStatusItem{
		item("edge-a", backendStatus(1, "True")), item("edge-b", backendStatus(2, "True")),
	})
	c := condition(got, []string{"status", "conditions"}, "Accepted")
	require.NotNil(t, c)
	assert.Equal(t, "True", c["status"])
	assert.Equal(t, "Accepted on 2 of 2 clusters", c["message"])
}

func TestBackend_OneNoStatus(t *testing.T) {
	got := runRule(t, backendRule, []workv1alpha2.AggregatedStatusItem{
		item("edge-a", backendStatus(1, "True")), item("edge-b", nil),
	})
	c := condition(got, []string{"status", "conditions"}, "Accepted")
	require.NotNil(t, c)
	assert.Equal(t, "Unknown", c["status"])
	assert.Equal(t, "Waiting on edge-b", c["message"])
}
