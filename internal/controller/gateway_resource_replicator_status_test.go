// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// BackendTLSPolicy is read at the version the replicator watches and its status is saved through the storage
// version, so the merge patch must be computed between two objects of that version.
func TestPatchUpstreamStatusThroughTheStorageVersion(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	env := &envtest.Environment{CRDInstallOptions: envtest.CRDInstallOptions{Paths: installedGatewayAPICRDPaths(t), ErrorIfPathMissing: true}}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	base, err := client.NewWithWatch(cfg, client.Options{Scheme: runtime.NewScheme()})
	require.NoError(t, err)

	var patches []map[string]any
	recording := interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			data, err := patch.Data(obj)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, json.Unmarshal(data, &body))
			patches = append(patches, body)
			return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	}
	ctx := context.Background()
	watched := schema.GroupVersionKind{Group: groupGatewayNetworking, Version: "v1alpha3", Kind: KindBackendTLSPolicy}
	resource := replicationResource{gvk: watched, replicationResourceConfig: defaultReplicationResourceConfigs[gvkKey(watched)]}
	require.NotNil(t, resource.replicationResourceConfig.statusGVK, "the BackendTLSPolicy config saves status through another version")

	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(watched)
	policy.SetNamespace(metav1.NamespaceDefault)
	policy.SetName("btp")
	require.NoError(t, unstructured.SetNestedSlice(policy.Object, []any{map[string]any{"group": "", "kind": "Service", "name": "svc"}}, "spec", "targetRefs"))
	require.NoError(t, unstructured.SetNestedField(policy.Object, map[string]any{"hostname": "svc.example.com", "wellKnownCACertificates": "System"}, "spec", "validation"))
	require.NoError(t, base.Create(ctx, policy))

	status := map[string]any{"ancestors": []any{map[string]any{
		"ancestorRef":    map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "gw"},
		"controllerName": "example.com/controller",
		"conditions": []any{map[string]any{
			"type": "Accepted", "status": "True", "reason": "Accepted", "message": "ok",
			"lastTransitionTime": "2026-10-10T00:00:00Z", "observedGeneration": int64(1),
		}},
	}}}
	recorder := interceptor.NewClient(base, recording)
	read := func() *unstructured.Unstructured {
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(watched)
		require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(policy), live))
		return live
	}

	require.NoError(t, patchUpstreamStatus(ctx, recorder, resource, read(), status))
	ancestors, _, _ := unstructured.NestedSlice(read().Object, "status", "ancestors")
	require.Len(t, ancestors, 1, "the status is saved")

	require.NoError(t, patchUpstreamStatus(ctx, recorder, resource, read(), nil))
	_, found, _ := unstructured.NestedFieldNoCopy(read().Object, "status", "ancestors")
	require.False(t, found, "an empty status is cleared")

	require.Len(t, patches, 2)
	for _, body := range patches {
		require.Equal(t, []string{"status"}, slices.Collect(maps.Keys(body)), "the patch carries only the status")
	}
}
