// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/connectorbinding"
)

var testBindingKey = ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))

func testBindingEndpointID() string {
	return hex.EncodeToString(testBindingKey.Public().(ed25519.PublicKey))
}

func testBindingSignature(uid, endpointID string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(testBindingKey, connectorbinding.Message(uid, endpointID)))
}

func connectorStatusWithBinding(endpointID, signature string) map[string]any {
	publicKey := map[string]any{"id": endpointID}
	if signature != "" {
		publicKey["binding"] = map[string]any{"signature": signature}
	}
	return map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "Ready"},
		},
		"connectionDetails": map[string]any{
			"type":      "PublicKey",
			"publicKey": publicKey,
		},
	}
}

type keyBindingReplicatorFixture struct {
	upstream   client.Client
	downstream client.Client
	reconciler *GatewayResourceReplicatorReconciler
	req        GVKRequest
	gvk        schema.GroupVersionKind
}

func newKeyBindingReplicatorFixture(t *testing.T, name, uid string, annotations map[string]string, status map[string]any) *keyBindingReplicatorFixture {
	t.Helper()
	gvk := schema.GroupVersionKind{Group: "networking.datumapis.com", Version: "v1alpha1", Kind: "Connector"}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: types.UID("ns-uid")}}
	statusTemplate := &unstructured.Unstructured{}
	statusTemplate.SetGroupVersionKind(gvk)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(ns.Name)
	obj.SetName(name)
	obj.SetUID(types.UID(uid))
	obj.SetAnnotations(annotations)
	obj.Object["spec"] = map[string]any{"connectorClassName": "iroh"}
	if status != nil {
		obj.Object["status"] = status
	}

	upstream := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(statusTemplate).WithObjects(ns, obj).Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()
	return &keyBindingReplicatorFixture{
		upstream:   upstream,
		downstream: downstream,
		reconciler: newReplicatorForGVKTest(gvk, upstream, downstream, scheme),
		gvk:        gvk,
		req: GVKRequest{
			GVK: gvk,
			Request: mcreconcile.Request{
				ClusterName: "upstream",
				Request:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns.Name, Name: name}},
			},
		},
	}
}

func (f *keyBindingReplicatorFixture) reconcile(t *testing.T, times int) {
	t.Helper()
	for range times {
		_, err := f.reconciler.Reconcile(context.Background(), f.req)
		require.NoError(t, err)
	}
}

func (f *keyBindingReplicatorFixture) downstreamVerdict(t *testing.T) (string, bool) {
	t.Helper()
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(f.gvk)
	require.NoError(t, f.downstream.Get(context.Background(),
		client.ObjectKey{Namespace: "ns-ns-uid", Name: f.req.Name}, &obj))
	v, ok := obj.GetAnnotations()[networkingv1alpha1.ConnectorKeyBindingAnnotation]
	return v, ok
}

func TestReplicatorStampsConnectorKeyBinding(t *testing.T) {
	id := testBindingEndpointID()
	const uid = "connector-uid-a"

	tests := []struct {
		name        string
		annotations map[string]string
		status      map[string]any
		want        connectorbinding.Verdict
	}{
		{name: "signed for this connector", status: connectorStatusWithBinding(id, testBindingSignature(uid, id)), want: connectorbinding.Verified},
		{name: "no binding", status: connectorStatusWithBinding(id, ""), want: connectorbinding.Missing},
		{name: "no status", status: nil, want: connectorbinding.Missing},
		{name: "signature copied from another connector", status: connectorStatusWithBinding(id, testBindingSignature("connector-uid-b", id)), want: connectorbinding.Invalid},
		{name: "malformed signature", status: connectorStatusWithBinding(id, "%%%"), want: connectorbinding.Invalid},
		{
			name:        "upstream annotation does not override the verdict",
			annotations: map[string]string{networkingv1alpha1.ConnectorKeyBindingAnnotation: string(connectorbinding.Verified)},
			status:      connectorStatusWithBinding(id, ""),
			want:        connectorbinding.Missing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newKeyBindingReplicatorFixture(t, "connector-1", uid, tt.annotations, tt.status)
			f.reconcile(t, 2)
			got, ok := f.downstreamVerdict(t)
			require.True(t, ok, "downstream copy must carry the key binding annotation")
			assert.Equal(t, string(tt.want), got)
		})
	}
}

func TestReplicatorRestampsConnectorKeyBindingWhenStatusChanges(t *testing.T) {
	id := testBindingEndpointID()
	const uid = "connector-uid-restamp"
	f := newKeyBindingReplicatorFixture(t, "connector-restamp", uid, nil, connectorStatusWithBinding(id, ""))
	f.reconcile(t, 2)
	got, _ := f.downstreamVerdict(t)
	require.Equal(t, string(connectorbinding.Missing), got)

	ctx := context.Background()
	var upstream unstructured.Unstructured
	upstream.SetGroupVersionKind(f.gvk)
	require.NoError(t, f.upstream.Get(ctx, f.req.NamespacedName, &upstream))
	upstream.Object["status"] = connectorStatusWithBinding(id, testBindingSignature(uid, id))
	require.NoError(t, f.upstream.Status().Update(ctx, &upstream))

	f.reconcile(t, 1)
	got, _ = f.downstreamVerdict(t)
	assert.Equal(t, string(connectorbinding.Verified), got)

	require.NoError(t, f.upstream.Get(ctx, f.req.NamespacedName, &upstream))
	upstream.Object["status"] = connectorStatusWithBinding(id, testBindingSignature("another-uid", id))
	require.NoError(t, f.upstream.Status().Update(ctx, &upstream))

	f.reconcile(t, 1)
	got, _ = f.downstreamVerdict(t)
	assert.Equal(t, string(connectorbinding.Invalid), got)
}

func TestConnectorKeyBindingTrackerCountsConnectorsByVerdict(t *testing.T) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_connector_key_binding"}, []string{metricLabelVerdict})
	tracker := newConnectorKeyBindingTracker(gauge)
	count := func(v connectorbinding.Verdict) float64 {
		return testutil.ToFloat64(gauge.WithLabelValues(string(v)))
	}

	for _, v := range connectorbinding.Verdicts {
		assert.Zero(t, count(v))
	}

	_, changed := tracker.record("a", connectorbinding.Missing)
	assert.True(t, changed)
	_, changed = tracker.record("a", connectorbinding.Missing)
	assert.False(t, changed)
	tracker.record("b", connectorbinding.Invalid)
	assert.Equal(t, 1.0, count(connectorbinding.Missing))
	assert.Equal(t, 1.0, count(connectorbinding.Invalid))

	prev, changed := tracker.record("a", connectorbinding.Verified)
	assert.True(t, changed)
	assert.Equal(t, connectorbinding.Missing, prev)
	assert.Zero(t, count(connectorbinding.Missing))
	assert.Equal(t, 1.0, count(connectorbinding.Verified))

	tracker.forget("b")
	tracker.forget("b")
	tracker.forget("unknown")
	assert.Zero(t, count(connectorbinding.Invalid))
	assert.Equal(t, 1.0, count(connectorbinding.Verified))
}
