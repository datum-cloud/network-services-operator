package interpreters

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	configv1alpha1 "github.com/karmada-io/karmada/pkg/apis/config/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/resourceinterpreter/customized/declarative/luavm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

const rulesFile = "../../config/federation/resourceinterpreters.yaml"

func loadRules(t *testing.T) map[string]*configv1alpha1.ResourceInterpreterCustomization {
	t.Helper()
	raw, err := os.ReadFile(rulesFile)
	require.NoError(t, err)
	rules := map[string]*configv1alpha1.ResourceInterpreterCustomization{}
	for _, doc := range strings.Split(string(raw), "\n---\n") {
		if strings.TrimSpace(stripComments(doc)) == "" {
			continue
		}
		rule := &configv1alpha1.ResourceInterpreterCustomization{}
		require.NoError(t, yaml.UnmarshalStrict([]byte(doc), rule))
		rules[rule.Name] = rule
	}
	return rules
}

func stripComments(doc string) string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func desired(rule *configv1alpha1.ResourceInterpreterCustomization) map[string]any {
	return map[string]any{
		"apiVersion": rule.Spec.Target.APIVersion,
		"kind":       rule.Spec.Target.Kind,
		"metadata":   map[string]any{"name": "x", "namespace": "ns-a", "generation": int64(1)},
	}
}

func item(cluster string, status map[string]any) workv1alpha2.AggregatedStatusItem {
	it := workv1alpha2.AggregatedStatusItem{ClusterName: cluster, Applied: true}
	if status != nil {
		raw, err := json.Marshal(status)
		if err != nil {
			panic(err)
		}
		it.Status = &runtime.RawExtension{Raw: raw}
	}
	return it
}

func aggregate(t *testing.T, vm *luavm.VM, rule *configv1alpha1.ResourceInterpreterCustomization, desired map[string]any, items []workv1alpha2.AggregatedStatusItem) *unstructured.Unstructured {
	t.Helper()
	got, err := vm.AggregateStatus(&unstructured.Unstructured{Object: desired}, items, rule.Spec.Customizations.StatusAggregation.LuaScript)
	require.NoError(t, err)
	return got
}

func condition(obj *unstructured.Unstructured, path []string, typ string) map[string]any {
	conds, _, _ := unstructured.NestedSlice(obj.Object, path...)
	for _, c := range conds {
		if cond, ok := c.(map[string]any); ok && cond["type"] == typ {
			return cond
		}
	}
	return nil
}

func TestEveryRuleLoadsAndAggregatesAnEmptyList(t *testing.T) {
	rules := loadRules(t)
	require.Len(t, rules, 6)
	vm := luavm.New(false, 1)
	for name, rule := range rules {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, rule.Spec.Customizations.StatusAggregation)
			got := aggregate(t, vm, rule, desired(rule), nil)
			assert.Equal(t, rule.Spec.Target.Kind, got.GetKind())
		})
	}
}
