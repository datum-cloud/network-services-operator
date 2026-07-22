package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// nolint:unparam
func oidcPolicy(namespace, name, secretName string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(testGVK)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	obj.SetFinalizers([]string{gatewayResourceReplicatorFinalizer})
	obj.Object["spec"] = map[string]any{
		"targetRefs": []any{
			map[string]any{
				"group": "gateway.networking.k8s.io",
				"kind":  "Gateway",
				"name":  "shared-gateway",
			},
		},
		"oidc": map[string]any{
			"clientSecret": map[string]any{"name": secretName},
		},
	}
	return obj
}

func propagateTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

// nolint:unparam
func getSecret(t *testing.T, c client.Client, namespace, name string) *corev1.Secret {
	t.Helper()
	var secret corev1.Secret
	assert.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, &secret))
	return &secret
}

func TestReplicatorLabelsReferencedSecretForPropagation(t *testing.T) {
	scheme := propagateTestScheme(t)
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: types.UID("ns-uid")}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "oidc-secret"}}
	policy := oidcPolicy("tenant", "p1", "oidc-secret")

	statusTemplate := &unstructured.Unstructured{}
	statusTemplate.SetGroupVersionKind(testGVK)
	upstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(statusTemplate).
		WithObjects(ns, secret, policy.DeepCopy()).
		Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForTest(upstream, downstream, scheme)

	_, err := reconciler.Reconcile(ctx, gvkRequestFor(policy))
	assert.NoError(t, err)

	got := getSecret(t, upstream, "tenant", "oidc-secret")
	_, labeled := got.Labels[gatewaySyncLabel]
	assert.True(t, labeled, "referenced secret must carry the gateway-sync label")
	assert.Equal(t, "true", got.Annotations[gatewaySyncManagedAnnotation],
		"operator-applied label must be marked managed")
}

func TestReplicatorUnlabelsSecretWhenNoLongerReferenced(t *testing.T) {
	scheme := propagateTestScheme(t)
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: types.UID("ns-uid")}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace:   "tenant",
		Name:        "oidc-secret",
		Labels:      map[string]string{gatewaySyncLabel: "true"},
		Annotations: map[string]string{gatewaySyncManagedAnnotation: "true"},
	}}
	policy := oidcPolicy("tenant", "p1", "oidc-secret")

	statusTemplate := &unstructured.Unstructured{}
	statusTemplate.SetGroupVersionKind(testGVK)
	upstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(statusTemplate).
		WithObjects(ns, secret, policy.DeepCopy()).
		Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForTest(upstream, downstream, scheme)

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(testGVK)
	assert.NoError(t, upstream.Get(ctx, client.ObjectKeyFromObject(policy), fetched))
	assert.NoError(t, unstructured.SetNestedMap(fetched.Object, map[string]any{}, "spec"))
	assert.NoError(t, unstructured.SetNestedSlice(fetched.Object, []any{}, "spec", "targetRefs"))
	assert.NoError(t, upstream.Update(ctx, fetched))

	_, err := reconciler.Reconcile(ctx, gvkRequestFor(policy))
	assert.NoError(t, err)

	got := getSecret(t, upstream, "tenant", "oidc-secret")
	_, labeled := got.Labels[gatewaySyncLabel]
	assert.False(t, labeled, "managed label must be removed once unreferenced")
	_, annotated := got.Annotations[gatewaySyncManagedAnnotation]
	assert.False(t, annotated, "managed annotation must be removed alongside the label")
}

func TestReplicatorKeepsSecretLabeledWhileAnotherPolicyReferences(t *testing.T) {
	scheme := propagateTestScheme(t)
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: types.UID("ns-uid")}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace:   "tenant",
		Name:        "shared-secret",
		Labels:      map[string]string{gatewaySyncLabel: "true"},
		Annotations: map[string]string{gatewaySyncManagedAnnotation: "true"},
	}}
	p1 := oidcPolicy("tenant", "p1", "shared-secret")
	p2 := oidcPolicy("tenant", "p2", "shared-secret")

	statusTemplate := &unstructured.Unstructured{}
	statusTemplate.SetGroupVersionKind(testGVK)
	upstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(statusTemplate).
		WithObjects(ns, secret, p1.DeepCopy(), p2.DeepCopy()).
		Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForTest(upstream, downstream, scheme)

	assert.NoError(t, upstream.Delete(ctx, p1.DeepCopy()))

	_, err := reconciler.Reconcile(ctx, gvkRequestFor(p1))
	assert.NoError(t, err)

	got := getSecret(t, upstream, "tenant", "shared-secret")
	_, labeled := got.Labels[gatewaySyncLabel]
	assert.True(t, labeled, "secret must stay labeled while another policy references it")
}

func TestReplicatorLeavesUserAppliedSecretLabel(t *testing.T) {
	scheme := propagateTestScheme(t)
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: types.UID("ns-uid")}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "tenant",
		Name:      "user-secret",
		Labels:    map[string]string{gatewaySyncLabel: "true"},
	}}
	policy := oidcPolicy("tenant", "p1", "other-secret")

	statusTemplate := &unstructured.Unstructured{}
	statusTemplate.SetGroupVersionKind(testGVK)
	upstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(statusTemplate).
		WithObjects(ns, secret, policy.DeepCopy()).
		Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForTest(upstream, downstream, scheme)

	_, err := reconciler.Reconcile(ctx, gvkRequestFor(policy))
	assert.NoError(t, err)

	got := getSecret(t, upstream, "tenant", "user-secret")
	_, labeled := got.Labels[gatewaySyncLabel]
	assert.True(t, labeled, "user-applied label must never be stripped by the operator")
}
