//go:build prototype

// SPDX-License-Identifier: AGPL-3.0-only

package prototype

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// P2 status with one writer: controller-runtime's merge patch, no
// resourceVersion.
// ---------------------------------------------------------------------------

// statusController starts a controller-runtime controller that sets the
// Accepted condition's reason to the HTTPProxy's "desired" annotation. Its
// status save is computed from its informer cache. It has no generation filter,
// so its own status save brings it back.
func statusController(t *testing.T, e *env, ctx context.Context, mode string, n *counts, saves *atomic.Int64) {
	mgr, err := ctrl.NewManager(e.test.Config, ctrl.Options{
		Scheme: e.scheme, Metrics: metricsserver.Options{BindAddress: "0"},
	})
	require.NoError(t, err)
	r := reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		var p networkingv1alpha.HTTPProxy
		if err := mgr.GetClient().Get(ctx, req.NamespacedName, &p); err != nil {
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
		want := p.Annotations["desired"]
		if c := apimeta.FindStatusCondition(p.Status.Conditions, "Accepted"); c != nil && c.Reason == want {
			return reconcile.Result{}, nil
		}
		modified := p.DeepCopy()
		apimeta.SetStatusCondition(&modified.Status.Conditions,
			metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: want})
		saves.Add(1)
		if mode == "patch" {
			err = mgr.GetClient().Status().Patch(ctx, modified, client.MergeFrom(&p),
				client.FieldOwner(FieldManager("httpproxy")))
		} else {
			err = mgr.GetClient().Status().Update(ctx, modified)
		}
		if apierrors.IsConflict(err) {
			n.conflicts.Add(1)
		}
		return reconcile.Result{}, err
	})
	require.NoError(t, ctrl.NewControllerManagedBy(mgr).For(&networkingv1alpha.HTTPProxy{}).
		Named(fmt.Sprintf("status-%s-%d", mode, time.Now().UnixNano())).Complete(r))
	go func() { _ = mgr.Start(ctx) }()
}

func statusOneWriter(t *testing.T, e *env) {
	for _, mode := range []string{"update", "patch"} {
		ctx, cancel := context.WithCancel(context.Background())
		n := &counts{}
		var saves atomic.Int64
		statusController(t, e, ctx, mode, n, &saves)
		const objects, changes = 20, 10
		var wg sync.WaitGroup
		for i := range objects {
			wg.Go(func() { driveDesiredState(t, e, fmt.Sprintf("status-%s-%d", mode, i), changes) })
		}
		wg.Wait()
		cancel()
		t.Logf("RESULT P2 status save=%s objects=%d changes=%d saves=%d %s settled=all",
			mode, objects, changes, saves.Load(), n)
	}
}

// driveDesiredState creates an HTTPProxy and changes its desired state, while
// another controller keeps applying the object's labels.
func driveDesiredState(t *testing.T, e *env, name string, changes int) {
	ctx := context.Background()
	p := newProxy(name)
	p.Annotations = map[string]string{"desired": "R0"}
	require.NoError(t, e.c.Create(ctx, p))
	key := client.ObjectKeyFromObject(p)
	var labeller sync.WaitGroup
	labeller.Go(func() { churnLabels(t, e, key, 3*changes) })
	for f := 1; f <= changes; f++ {
		cur := &networkingv1alpha.HTTPProxy{}
		require.NoError(t, e.c.Get(ctx, key, cur))
		edited := cur.DeepCopy()
		edited.Annotations["desired"] = fmt.Sprintf("R%d", f)
		require.NoError(t, e.c.Patch(ctx, edited, client.MergeFrom(cur)))
	}
	labeller.Wait()
	requireReason(t, e, key, fmt.Sprintf("R%d", changes))
}

func requireReason(t *testing.T, e *env, key types.NamespacedName, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got := &networkingv1alpha.HTTPProxy{}
		if e.c.Get(context.Background(), key, got) != nil {
			return false
		}
		c := apimeta.FindStatusCondition(got.Status.Conditions, "Accepted")
		return c != nil && c.Reason == want
	}, 30*time.Second, 50*time.Millisecond, "the status did not settle on %q", want)
}

// statusListShrinks: a status saved under a legacy manager. The merge patch
// replaces the list, so an entry the controller drops goes, whoever recorded it.
func statusListShrinks(t *testing.T, e *env) {
	ctx := context.Background()
	p := newProxy("shrinks")
	require.NoError(t, e.c.Create(ctx, p))
	p.Status.HostnameStatuses = []networkingv1alpha.HostnameStatus{
		{Hostname: "a.example.test"}, {Hostname: "b.example.test"},
	}
	require.NoError(t, e.c.Status().Update(ctx, p, client.FieldOwner(legacyOld)))
	modified := p.DeepCopy()
	modified.Status.HostnameStatuses = modified.Status.HostnameStatuses[:1]
	require.NoError(t, e.c.Status().Patch(ctx, modified, client.MergeFrom(p),
		client.FieldOwner(FieldManager("httpproxy"))))
	got := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), got))
	require.Len(t, got.Status.HostnameStatuses, 1)
	t.Logf("RESULT P2 list shrinks: a status list saved under %q went from 2 entries to %d by merge patch",
		legacyOld, len(got.Status.HostnameStatuses))
}

// statusOldCode: an old replica overwrites the status with Status().Update
// while the new controller runs; the new controller's next pass restores it.
func statusOldCode(t *testing.T, e *env) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := &counts{}
	var saves atomic.Int64
	statusController(t, e, ctx, "patch", n, &saves)
	p := newProxy("status-old-code")
	p.Annotations = map[string]string{"desired": "NEW"}
	require.NoError(t, e.c.Create(ctx, p))
	key := client.ObjectKeyFromObject(p)
	requireReason(t, e, key, "NEW")
	for range 5 {
		old := &networkingv1alpha.HTTPProxy{}
		require.NoError(t, e.c.Get(ctx, key, old))
		apimeta.SetStatusCondition(&old.Status.Conditions,
			metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "OLD"})
		require.NoError(t, client.IgnoreNotFound(e.c.Status().Update(ctx, old, client.FieldOwner(legacyPresent))))
		requireReason(t, e, key, "NEW")
	}
	t.Logf("RESULT P2 old code at once: 5 status overwrites by the old code; each time the new controller restored it")
}

// ---------------------------------------------------------------------------
// P3 conditions with two writers: server-side apply, one field manager per
// controller, force, only the controller's own condition types.
// ---------------------------------------------------------------------------

func connectorStatus(name string, uid types.UID, conds ...metav1.Condition) *unstructured.Unstructured {
	items := make([]any, 0, len(conds))
	for _, c := range conds {
		items = append(items, map[string]any{
			"type": c.Type, "status": string(c.Status), "reason": c.Reason, "message": c.Message,
			"lastTransitionTime": "2026-10-09T00:00:00Z",
		})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.datumapis.com/v1alpha1", "kind": "Connector",
		"metadata": map[string]any{"namespace": "default", "name": name, "uid": string(uid)},
		"status":   map[string]any{"conditions": items},
	}}
}

func cond(t string, s metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{Type: t, Status: s, Reason: reason}
}

func applyStatus(ctx context.Context, c client.Client, controller string, u *unstructured.Unstructured) error {
	return c.Status().Apply(ctx, client.ApplyConfigurationFromUnstructured(u),
		client.FieldOwner(FieldManager(controller)), client.ForceOwnership)
}

func conditionsOf(c *networkingv1alpha1.Connector) map[string]string {
	out := map[string]string{}
	for _, x := range c.Status.Conditions {
		out[x.Type] = fmt.Sprintf("%s/%s", x.Status, x.Reason)
	}
	return out
}

// legacyConnector saves a Connector whose conditions are recorded, as the
// operator records them, under one legacy manager, and whose connection details
// come from the connector agent.
func legacyConnector(t *testing.T, e *env, name string) *networkingv1alpha1.Connector {
	ctx := context.Background()
	cn := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec:       networkingv1alpha1.ConnectorSpec{ConnectorClassName: "datum-connect"},
	}
	require.NoError(t, e.c.Create(ctx, cn))
	now := metav1.Now()
	cn.Status.Conditions = []metav1.Condition{
		{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted", LastTransitionTime: now},
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: now},
		{Type: "IrohDNSPublished", Status: metav1.ConditionTrue, Reason: "Published", LastTransitionTime: now},
	}
	require.NoError(t, e.c.Status().Update(ctx, cn, client.FieldOwner(legacyPresent)))
	cn.Status.ConnectionDetails = &networkingv1alpha1.ConnectorConnectionDetails{
		Type: "PublicKey",
		PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
			Id: "agent-key", HomeRelay: "https://relay.example.test",
			Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{{Address: "192.0.2.10", Port: 4433}},
		},
	}
	require.NoError(t, e.c.Status().Update(ctx, cn, client.FieldOwner("datum-connect-agent")))
	return cn
}

func connectorConds(i int) []metav1.Condition {
	return []metav1.Condition{
		cond("Accepted", metav1.ConditionTrue, "Accepted"), cond("Ready", metav1.ConditionTrue, fmt.Sprintf("R%d", i)),
	}
}

func irohConds(i int) []metav1.Condition {
	return []metav1.Condition{cond("IrohDNSPublished", metav1.ConditionTrue, fmt.Sprintf("P%d", i))}
}

func conditionsTwoWriters(t *testing.T, e *env) {
	ctx := context.Background()

	// Control: two controllers with merge patches from their own reads. Each
	// replaces the whole list, so the second undoes the first.
	cn := legacyConnector(t, e, "merge-control")
	connectorRead, irohRead := cn.DeepCopy(), cn.DeepCopy()
	ready := connectorRead.DeepCopy()
	apimeta.SetStatusCondition(&ready.Status.Conditions, cond("Ready", metav1.ConditionFalse, "Offline"))
	require.NoError(t, e.c.Status().Patch(ctx, ready, client.MergeFrom(connectorRead)))
	published := irohRead.DeepCopy()
	apimeta.SetStatusCondition(&published.Status.Conditions, cond("IrohDNSPublished", metav1.ConditionFalse, "Pending"))
	require.NoError(t, e.c.Status().Patch(ctx, published, client.MergeFrom(irohRead)))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(cn), cn))
	t.Logf("RESULT P3 control merge patch: the connector set Ready=False, then iroh-dns saved its own: Ready=%s",
		conditionsOf(cn)["Ready"])

	// The proposal: each controller applies only its own condition types.
	cn = legacyConnector(t, e, "apply")
	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			if applyStatus(ctx, e.c, "connector", connectorStatus(cn.Name, cn.UID, connectorConds(i)...)) != nil {
				failures.Add(1)
			}
		})
		wg.Go(func() {
			if applyStatus(ctx, e.c, "iroh-dns", connectorStatus(cn.Name, cn.UID, irohConds(i)...)) != nil {
				failures.Add(1)
			}
		})
	}
	wg.Wait()
	require.NoError(t, applyStatus(ctx, e.c, "connector", connectorStatus(cn.Name, cn.UID, connectorConds(100)...)))
	require.NoError(t, applyStatus(ctx, e.c, "iroh-dns", connectorStatus(cn.Name, cn.UID, irohConds(200)...)))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(cn), cn))
	got := conditionsOf(cn)
	want := map[string]string{"Accepted": "True/Accepted", "Ready": "True/R100", "IrohDNSPublished": "True/P200"}
	require.Equal(t, want, got)
	require.NotNil(t, cn.Status.ConnectionDetails, "the agent's field is gone")

	// A controller retires a condition by setting it False, not by leaving it out.
	retired := connectorStatus(cn.Name, cn.UID, cond("Accepted", metav1.ConditionTrue, "Accepted"),
		cond("Ready", metav1.ConditionFalse, "Offline"))
	require.NoError(t, applyStatus(ctx, e.c, "connector", retired))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(cn), cn))
	require.Equal(t, "False/Offline", conditionsOf(cn)["Ready"])
	t.Logf("RESULT P3 apply: 100 concurrent applies by two controllers, failures=%d; each controller's last "+
		"values kept; Ready retired as False/Offline; the agent's field kept", failures.Load())

	// Control: leaving a condition out does not remove the part a legacy manager
	// still shares; here the part left fails validation.
	err := applyStatus(ctx, e.c, "connector",
		connectorStatus(cn.Name, cn.UID, cond("Accepted", metav1.ConditionTrue, "Accepted")))
	require.Error(t, err)
	t.Logf("RESULT P3 control leave-out: the connector applies without Ready -> %v", err)

	// A status apply on a deleted object creates nothing.
	require.NoError(t, e.c.Delete(ctx, cn))
	err = applyStatus(ctx, e.c, "connector",
		connectorStatus(cn.Name, cn.UID, cond("Ready", metav1.ConditionTrue, "Ready")))
	require.Error(t, err)
	require.True(t, apierrors.IsNotFound(e.c.Get(ctx, client.ObjectKeyFromObject(cn), &networkingv1alpha1.Connector{})))
	t.Logf("RESULT P3 deleted object: status apply -> %v; nothing created", err)
}

// conditionsOldCode: during a rollout an old replica saves the whole status
// with Update; each controller's next apply restores its own conditions.
func conditionsOldCode(t *testing.T, e *env) {
	ctx := context.Background()
	cn := legacyConnector(t, e, "conditions-old-code")
	require.NoError(t, applyStatus(ctx, e.c, "connector", connectorStatus(cn.Name, cn.UID, connectorConds(1)...)))
	require.NoError(t, applyStatus(ctx, e.c, "iroh-dns", connectorStatus(cn.Name, cn.UID, irohConds(1)...)))
	old := &networkingv1alpha1.Connector{}
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(cn), old))
	for i := range old.Status.Conditions {
		old.Status.Conditions[i].Reason = "OLD"
	}
	require.NoError(t, e.c.Status().Update(ctx, old, client.FieldOwner(legacyPresent)))
	require.NoError(t, applyStatus(ctx, e.c, "connector", connectorStatus(cn.Name, cn.UID, connectorConds(2)...)))
	require.NoError(t, applyStatus(ctx, e.c, "iroh-dns", connectorStatus(cn.Name, cn.UID, irohConds(2)...)))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(cn), cn))
	require.Equal(t, map[string]string{"Accepted": "True/Accepted", "Ready": "True/R2", "IrohDNSPublished": "True/P2"},
		conditionsOf(cn))
	t.Logf("RESULT P3 old code at once: the old code overwrote all conditions; " +
		"each controller's next apply restored its own")
}
