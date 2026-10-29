// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Only patch changed fields, preserving unknown fields even inside named lists.
// The resourceVersion test makes the complete patch fail on a concurrent edit.
func patch(cmd *cobra.Command, c client.Client, before, after client.Object) error {
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	var old, new map[string]any
	if err := json.Unmarshal(a, &old); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &new); err != nil {
		return err
	}
	changes := diffJSON(old, new, "")
	ops := make([]map[string]any, 0, len(changes)+1)
	ops = append(ops, map[string]any{"op": "test", "path": "/metadata/resourceVersion", "value": before.GetResourceVersion()})
	ops = append(ops, changes...)
	if len(ops) == 1 {
		return nil
	}
	data, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	opts := []client.PatchOption{client.FieldOwner("datumctl-nlb")}
	if boolean(cmd, "dry-run") {
		opts = append(opts, client.DryRunAll)
	}
	return c.Patch(cmd.Context(), after, client.RawPatch(types.JSONPatchType, data), opts...)
}
func esc(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func diffJSON(a, b any, path string) []map[string]any {
	if reflect.DeepEqual(a, b) {
		return nil
	}
	ops := []map[string]any{}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := []string{}
		for k := range am {
			keys = append(keys, k)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			av, ao := am[k]
			bv, bo := bm[k]
			p := path + "/" + esc(k)
			switch {
			case !bo:
				ops = append(ops, map[string]any{"op": "remove", "path": p})
			case !ao:
				ops = append(ops, map[string]any{"op": "add", "path": p, "value": bv})
			default:
				ops = append(ops, diffJSON(av, bv, p)...)
			}
		}
		return ops
	}
	aa, aok := a.([]any)
	bb, bok := b.([]any)
	if aok && bok {
		named := func(list []any) bool {
			seen := map[string]bool{}
			for _, item := range list {
				m, ok := item.(map[string]any)
				if !ok {
					return false
				}
				name, ok := m["name"].(string)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
			}
			return true
		}
		if named(aa) && named(bb) {
			byName := map[string]any{}
			for _, item := range bb {
				byName[item.(map[string]any)["name"].(string)] = item
			}
			for i := len(aa) - 1; i >= 0; i-- {
				name := aa[i].(map[string]any)["name"].(string)
				p := fmt.Sprintf("%s/%d", path, i)
				if item, ok := byName[name]; ok {
					ops = append(ops, diffJSON(aa[i], item, p)...)
					delete(byName, name)
				} else {
					ops = append(ops, map[string]any{"op": "remove", "path": p})
				}
			}
			for _, item := range bb {
				if _, ok := byName[item.(map[string]any)["name"].(string)]; ok {
					ops = append(ops, map[string]any{"op": "add", "path": path + "/-", "value": item})
				}
			}
			return ops
		}
		if len(aa) == len(bb) {
			for i := range aa {
				ops = append(ops, diffJSON(aa[i], bb[i], fmt.Sprintf("%s/%d", path, i))...)
			}
			return ops
		}
	}
	return []map[string]any{{"op": "replace", "path": path, "value": b}}
}
