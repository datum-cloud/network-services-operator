package controller

import (
	"context"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	"testing"
)

func TestReplicationPreservesConcurrentDownstreamWrites(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	gvk := schema.GroupVersionKind{Group: "networking.datumapis.com", Version: "v1alpha", Kind: "NetworkContext"}
	upstream := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"network": map[string]any{"name": "private"}}}}
	upstream.SetGroupVersionKind(gvk)
	upstream.SetName("context")
	upstream.SetNamespace("default")
	upstream.SetUID("context-uid")
	up := fake.NewClientBuilder().WithScheme(scheme).WithObjects(upstream, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: "ns-uid"}}).Build()
	template := &unstructured.Unstructured{}
	template.SetGroupVersionKind(gvk)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(template).Build()
	inject := false
	downstream := interceptor.NewClient(base, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if err := c.Get(ctx, key, obj, opts...); err != nil {
			return err
		}
		if inject && obj.GetObjectKind().GroupVersionKind() == gvk {
			inject = false
			latest := obj.DeepCopyObject().(*unstructured.Unstructured)
			latest.SetAnnotations(map[string]string{"placement.example/owner": "preserve"})
			if err := c.Update(ctx, latest); err != nil {
				return err
			}
			latest.Object["status"] = map[string]any{"allocated": true}
			return c.Status().Update(ctx, latest)
		}
		return nil
	}})
	r := newReplicatorForGVKTest(gvk, up, downstream, scheme)
	req := GVKRequest{GVK: gvk, Request: mcreconcile.Request{ClusterName: "upstream", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(upstream)}}}
	for i := 0; i < 2; i++ {
		_, err := r.Reconcile(ctx, req)
		require.NoError(t, err)
	}
	require.NoError(t, up.Get(ctx, client.ObjectKeyFromObject(upstream), upstream))
	upstream.Object["spec"].(map[string]any)["mtu"] = int64(1400)
	require.NoError(t, up.Update(ctx, upstream))
	inject = true
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.False(t, inject, "concurrent write must be exercised")
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(gvk)
	require.NoError(t, base.Get(ctx, client.ObjectKey{Name: "context", Namespace: "ns-ns-uid"}, got))
	require.Equal(t, "preserve", got.GetAnnotations()["placement.example/owner"])
	allocated, _, err := unstructured.NestedBool(got.Object, "status", "allocated")
	require.NoError(t, err)
	require.True(t, allocated)
	mtu, _, err := unstructured.NestedInt64(got.Object, "spec", "mtu")
	require.NoError(t, err)
	require.Equal(t, int64(1400), mtu)
}
