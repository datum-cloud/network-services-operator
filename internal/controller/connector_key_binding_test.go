// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
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

func TestReplicatorRecordsKeyBindingOnlyAfterDownstreamWrite(t *testing.T) {
	id := testBindingEndpointID()
	const uid = "connector-uid-write-fails"
	f := newKeyBindingReplicatorFixture(t, "connector-write-fails", uid, nil, connectorStatusWithBinding(id, testBindingSignature(uid, id)))
	engageCtx, disengage := context.WithCancel(context.Background())
	t.Cleanup(disengage)
	require.NoError(t, connectorKeyBindings.Engage(engageCtx, f.req.ClusterName, &replicatorFakeCluster{}))
	f.reconcile(t, 1)

	failing := interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return errors.New("downstream unavailable")
		},
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errors.New("downstream unavailable")
		},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	f.reconciler.DownstreamCluster = &replicatorFakeCluster{scheme: scheme, c: fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(failing).Build()}

	_, err := f.reconciler.Reconcile(context.Background(), f.req)
	require.Error(t, err)
	_, tracked := trackedVerdict(f.req.ClusterName, f.req.NamespacedName)
	assert.False(t, tracked, "a verdict must not be counted until the downstream copy carries it")

	f.reconciler.DownstreamCluster = &replicatorFakeCluster{scheme: scheme, c: f.downstream}
	f.reconcile(t, 1)
	verdict, tracked := trackedVerdict(f.req.ClusterName, f.req.NamespacedName)
	assert.True(t, tracked)
	assert.Equal(t, connectorbinding.Verified, verdict)
}

func trackedVerdict(clusterName multicluster.ClusterName, key types.NamespacedName) (connectorbinding.Verdict, bool) {
	connectorKeyBindings.mu.Lock()
	defer connectorKeyBindings.mu.Unlock()
	v, ok := connectorKeyBindings.verdicts[clusterName][key]
	return v, ok
}

func TestConnectorKeyBindingTrackerCountsConnectorsByVerdict(t *testing.T) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_connector_key_binding"}, []string{metricLabelVerdict})
	tracker := newConnectorKeyBindingTracker(gauge)
	count := func(v connectorbinding.Verdict) float64 {
		return testutil.ToFloat64(gauge.WithLabelValues(string(v)))
	}
	a := types.NamespacedName{Namespace: "default", Name: "a"}
	b := types.NamespacedName{Namespace: "default", Name: "b"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, tracker.Engage(ctx, "p1", &replicatorFakeCluster{}))
	require.NoError(t, tracker.Engage(ctx, "p2", &replicatorFakeCluster{}))

	for _, v := range connectorbinding.Verdicts {
		assert.Zero(t, count(v))
	}

	_, changed := tracker.record("p1", a, connectorbinding.Missing)
	assert.True(t, changed)
	_, changed = tracker.record("p1", a, connectorbinding.Missing)
	assert.False(t, changed)
	tracker.record("p1", b, connectorbinding.Invalid)
	tracker.record("p2", a, connectorbinding.Missing)
	assert.Equal(t, 2.0, count(connectorbinding.Missing))
	assert.Equal(t, 1.0, count(connectorbinding.Invalid))

	prev, changed := tracker.record("p1", a, connectorbinding.Verified)
	assert.True(t, changed)
	assert.Equal(t, connectorbinding.Missing, prev)
	assert.Equal(t, 1.0, count(connectorbinding.Missing))
	assert.Equal(t, 1.0, count(connectorbinding.Verified))

	tracker.forget("p1", b)
	tracker.forget("p1", b)
	tracker.forget("p3", a)
	_, changed = tracker.record("p3", a, connectorbinding.Invalid)
	assert.False(t, changed, "a cluster that was never engaged is not counted")
	assert.Zero(t, count(connectorbinding.Invalid))
	assert.Equal(t, 1.0, count(connectorbinding.Verified))
	assert.Equal(t, 1.0, count(connectorbinding.Missing))
}

func TestConnectorKeyBindingTrackerForgetsDisengagedCluster(t *testing.T) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_connector_key_binding_engage"}, []string{metricLabelVerdict})
	tracker := newConnectorKeyBindingTracker(gauge)
	count := func(v connectorbinding.Verdict) float64 {
		return testutil.ToFloat64(gauge.WithLabelValues(string(v)))
	}
	a := types.NamespacedName{Namespace: "default", Name: "a"}
	b := types.NamespacedName{Namespace: "default", Name: "b"}

	p1Ctx, disengageP1 := context.WithCancel(context.Background())
	p2Ctx, disengageP2 := context.WithCancel(context.Background())
	defer disengageP2()
	require.NoError(t, tracker.Engage(p1Ctx, "p1", &replicatorFakeCluster{}))
	require.NoError(t, tracker.Engage(p2Ctx, "p2", &replicatorFakeCluster{}))

	tracker.record("p1", a, connectorbinding.Verified)
	tracker.record("p1", b, connectorbinding.Missing)
	tracker.record("p2", a, connectorbinding.Missing)

	disengageP1()
	require.Eventually(t, func() bool { return count(connectorbinding.Verified) == 0 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1.0, count(connectorbinding.Missing), "only the disengaged cluster's Connectors are forgotten")

	reengagedCtx, disengageAgain := context.WithCancel(context.Background())
	staleCtx, cancelStale := context.WithCancel(context.Background())
	stale := &replicatorFakeCluster{}
	require.NoError(t, tracker.Engage(staleCtx, "p3", stale))
	require.NoError(t, tracker.Engage(reengagedCtx, "p3", &replicatorFakeCluster{}))
	tracker.record("p3", a, connectorbinding.Invalid)
	cancelStale()
	assert.Never(t, func() bool { return count(connectorbinding.Invalid) == 0 }, 200*time.Millisecond, 10*time.Millisecond,
		"an earlier engagement ending must not forget a cluster engaged again")
	disengageAgain()
	require.Eventually(t, func() bool { return count(connectorbinding.Invalid) == 0 }, 5*time.Second, 10*time.Millisecond)
}

func TestConnectorKeyBindingTrackerIgnoresRecordAfterDisengage(t *testing.T) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_connector_key_binding_late"}, []string{metricLabelVerdict})
	tracker := newConnectorKeyBindingTracker(gauge)
	a := types.NamespacedName{Namespace: "default", Name: "a"}

	ctx, disengage := context.WithCancel(context.Background())
	require.NoError(t, tracker.Engage(ctx, "p1", &replicatorFakeCluster{}))
	tracker.record("p1", a, connectorbinding.Missing)

	disengage()
	require.Eventually(t, func() bool {
		tracker.mu.Lock()
		defer tracker.mu.Unlock()
		_, engaged := tracker.engaged["p1"]
		return !engaged
	}, 5*time.Second, 10*time.Millisecond)

	_, changed := tracker.record("p1", a, connectorbinding.Verified)
	assert.False(t, changed, "a reconcile finishing after its cluster disengaged must not be counted")

	tracker.mu.Lock()
	_, leaked := tracker.verdicts["p1"]
	tracker.mu.Unlock()
	assert.False(t, leaked)
	for _, v := range connectorbinding.Verdicts {
		assert.Zero(t, testutil.ToFloat64(gauge.WithLabelValues(string(v))), "verdict %s", v)
	}
}
