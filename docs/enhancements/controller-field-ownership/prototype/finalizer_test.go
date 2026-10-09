//go:build prototype

// SPDX-License-Identifier: AGPL-3.0-only

package prototype

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// counts are the rejections one scenario sees.
type counts struct{ conflicts, testFailures atomic.Int64 }

func (n *counts) String() string {
	return fmt.Sprintf("conflicts409=%d testFailures422=%d", n.conflicts.Load(), n.testFailures.Load())
}

// updateAddFinalizer is the save the patch replaces: a full-object Update from
// the given read. On a conflict it reads again and retries at once, which is
// faster than the operator's requeue, so its count is a lower bound.
func updateAddFinalizer(
	ctx context.Context, c client.Client, obj *networkingv1alpha.HTTPProxy, fin, manager string, n *counts,
) error {
	key := client.ObjectKeyFromObject(obj)
	for {
		if !controllerutil.AddFinalizer(obj, fin) {
			return nil
		}
		err := c.Update(ctx, obj, client.FieldOwner(manager))
		if !apierrors.IsConflict(err) {
			return err
		}
		n.conflicts.Add(1)
		obj = &networkingv1alpha.HTTPProxy{}
		if err := c.Get(ctx, key, obj); err != nil {
			return err
		}
	}
}

func updateRemoveFinalizer(
	ctx context.Context, c client.Client, key types.NamespacedName, fin string, n *counts,
) error {
	for {
		obj := &networkingv1alpha.HTTPProxy{}
		if err := c.Get(ctx, key, obj); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !controllerutil.RemoveFinalizer(obj, fin) {
			return nil
		}
		err := c.Update(ctx, obj, client.FieldOwner(legacyPresent))
		if !apierrors.IsConflict(err) {
			return client.IgnoreNotFound(err)
		}
		n.conflicts.Add(1)
	}
}

// writer adds and removes one finalizer.
type writer struct {
	add    func(cached *networkingv1alpha.HTTPProxy, fin string) error
	remove func(key types.NamespacedName, fin string) error
}

func patching(e *env, controller string, n *counts) writer {
	ctx := context.Background()
	p := e.patcher(controller, &n.testFailures)
	return writer{
		add: func(cached *networkingv1alpha.HTTPProxy, fin string) error {
			_, err := p.AddFinalizer(ctx, cached, fin)
			return err
		},
		remove: func(key types.NamespacedName, fin string) error {
			live := &networkingv1alpha.HTTPProxy{}
			if err := e.c.Get(ctx, key, live); err != nil {
				return client.IgnoreNotFound(err)
			}
			return p.RemoveFinalizer(ctx, live, fin)
		},
	}
}

func updating(e *env, n *counts) writer {
	ctx := context.Background()
	return writer{
		add: func(cached *networkingv1alpha.HTTPProxy, fin string) error {
			return updateAddFinalizer(ctx, e.c, cached, fin, legacyPresent, n)
		},
		remove: func(key types.NamespacedName, fin string) error {
			return updateRemoveFinalizer(ctx, e.c, key, fin, n)
		},
	}
}

// lifecycle creates an HTTPProxy. The HTTPProxy controller and the replicator
// each add their finalizer at the same moment, from the object as created.
// Then it is deleted, and both remove their finalizer at the same moment.
func lifecycle(t *testing.T, e *env, name string, proxyWriter, replicatorWriter writer) {
	ctx := context.Background()
	p := newProxy(name)
	require.NoError(t, e.c.Create(ctx, p))
	key := client.ObjectKeyFromObject(p)
	var wg sync.WaitGroup
	wg.Go(func() { require.NoError(t, proxyWriter.add(p.DeepCopy(), finProxy)) })
	wg.Go(func() { require.NoError(t, replicatorWriter.add(p.DeepCopy(), finReplicator)) })
	wg.Wait()
	got := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, e.c.Get(ctx, key, got))
	require.ElementsMatch(t, []string{finProxy, finReplicator}, got.Finalizers, "a writer lost the other's finalizer")
	require.NoError(t, e.c.Delete(ctx, newProxy(name)))
	wg.Go(func() { require.NoError(t, proxyWriter.remove(key, finProxy)) })
	wg.Go(func() { require.NoError(t, replicatorWriter.remove(key, finReplicator)) })
	wg.Wait()
	requireGone(t, e.c, p)
}

// finalizerChurn: two controllers add and remove their finalizers on the same
// object at the same moment. This is the one case where the patch still meets
// another writer: both change the finalizer list.
func finalizerChurn(t *testing.T, e *env) {
	const objects = 100
	for run := 1; run <= 3; run++ {
		for _, mode := range []string{"update", "patch"} {
			n := &counts{}
			proxy, replicator := updating(e, n), updating(e, n)
			if mode == "patch" {
				proxy, replicator = patching(e, "httpproxy", n), patching(e, "gateway-resource-replicator", n)
			}
			var wg sync.WaitGroup
			for i := range objects {
				wg.Go(func() { lifecycle(t, e, fmt.Sprintf("churn-%s-%d-%d", mode, run, i), proxy, replicator) })
			}
			wg.Wait()
			t.Logf("RESULT P1 churn run=%d save=%s objects=%d %s lost=0 stuck=0", run, mode, objects, n)
		}
	}
}

// finalizersBesideOtherSaves: one controller adds and removes its finalizer
// while two others keep saving the same object's status and labels.
func finalizersBesideOtherSaves(t *testing.T, e *env) {
	ctx := context.Background()
	const objects = 50
	for _, mode := range []string{"update", "patch"} {
		n := &counts{}
		w := updating(e, n)
		if mode == "patch" {
			w = patching(e, "httpproxy", n)
		}
		var wg sync.WaitGroup
		for i := range objects {
			wg.Go(func() {
				p := newProxy(fmt.Sprintf("beside-%s-%d", mode, i))
				require.NoError(t, e.c.Create(ctx, p))
				key := client.ObjectKeyFromObject(p)
				var others sync.WaitGroup
				others.Go(func() { churnStatus(t, e, key, 5) })
				others.Go(func() { churnLabels(t, e, key, 5) })
				require.NoError(t, w.add(p.DeepCopy(), finProxy))
				others.Wait()
				require.NoError(t, e.c.Delete(ctx, newProxy(p.Name)))
				others.Go(func() { churnLabels(t, e, key, 5) })
				require.NoError(t, w.remove(key, finProxy))
				others.Wait()
				requireGone(t, e.c, p)
			})
		}
		wg.Wait()
		t.Logf("RESULT P1 beside status and label saves save=%s objects=%d finalizer %s", mode, objects, n)
	}
}

// churnStatus is another controller saving the status by merge patch.
func churnStatus(t *testing.T, e *env, key types.NamespacedName, times int) {
	ctx := context.Background()
	for i := range times {
		cur := &networkingv1alpha.HTTPProxy{}
		if e.c.Get(ctx, key, cur) != nil {
			return
		}
		modified := cur.DeepCopy()
		apimeta.SetStatusCondition(&modified.Status.Conditions,
			metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: fmt.Sprintf("R%d", i)})
		require.NoError(t, client.IgnoreNotFound(e.c.Status().Patch(ctx, modified, client.MergeFrom(cur))))
	}
}

// churnLabels is another controller applying a label it owns.
func churnLabels(t *testing.T, e *env, key types.NamespacedName, times int) {
	ctx := context.Background()
	for i := range times {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(networkingv1alpha.GroupVersion.String())
		u.SetKind("HTTPProxy")
		u.SetNamespace(key.Namespace)
		u.SetName(key.Name)
		live := &networkingv1alpha.HTTPProxy{}
		if e.c.Get(ctx, key, live) != nil {
			return
		}
		u.SetUID(live.UID)
		u.SetLabels(map[string]string{"example.com/tick": fmt.Sprint(i)})
		err := e.c.Apply(ctx, client.ApplyConfigurationFromUnstructured(u),
			client.FieldOwner(FieldManager("labeller")), client.ForceOwnership)
		if err != nil && Gone(ctx, e.c, live, err) {
			return
		}
		require.NoError(t, err)
	}
}

func legacyFinalizers(t *testing.T, e *env) {
	ctx := context.Background()
	n := &counts{}
	p := newProxy("legacy")
	require.NoError(t, e.c.Create(ctx, p))
	// Recorded as production records them: one entry under each legacy manager.
	require.NoError(t, updateAddFinalizer(ctx, e.c, p, finProxy, legacyOld, n))
	require.NoError(t, updateAddFinalizer(ctx, e.c, p, finReplicator, legacyPresent, n))
	require.NoError(t, e.c.Delete(ctx, newProxy(p.Name)))
	w := patching(e, "httpproxy", n)
	require.NoError(t, w.remove(client.ObjectKeyFromObject(p), finProxy))
	require.NoError(t, w.remove(client.ObjectKeyFromObject(p), finReplicator))
	requireGone(t, e.c, p)
	t.Logf("RESULT P1 legacy: finalizers recorded under %q and %q removed by the patch; object deleted",
		legacyOld, legacyPresent)
}

func mixedRollout(t *testing.T, e *env) {
	const objects = 100
	old, patched := &counts{}, &counts{}
	var wg sync.WaitGroup
	for i := range objects {
		// The HTTPProxy controller runs the old code; the replicator the new.
		wg.Go(func() {
			lifecycle(t, e, fmt.Sprintf("mixed-%d", i), updating(e, old), patching(e, "gateway-resource-replicator", patched))
		})
	}
	wg.Wait()
	t.Logf("RESULT P1 mixed rollout objects=%d lost=0 stuck=0; old code %s; new code %s", objects, old, patched)
}

// duplicateFinalizer: two adds from the same cached copy, by two replicas or by
// one replica whose cache lags, append the finalizer twice. One removal call
// removes every copy.
func duplicateFinalizer(t *testing.T, e *env) {
	ctx := context.Background()
	n := &counts{}
	p := newProxy("duplicate")
	require.NoError(t, e.c.Create(ctx, p))
	require.NoError(t, patching(e, "gateway-resource-replicator", n).add(p.DeepCopy(), finReplicator))
	cached := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), cached))
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { require.NoError(t, patching(e, "httpproxy", n).add(cached.DeepCopy(), finProxy)) })
	}
	wg.Wait()
	got := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), got))
	copies := len(slices.DeleteFunc(slices.Clone(got.Finalizers), func(f string) bool { return f != finProxy }))
	require.NoError(t, e.c.Delete(ctx, newProxy(p.Name)))
	require.NoError(t, patching(e, "httpproxy", n).remove(client.ObjectKeyFromObject(p), finProxy))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), got))
	require.NotContains(t, got.Finalizers, finProxy)
	require.NoError(t, patching(e, "gateway-resource-replicator", n).remove(client.ObjectKeyFromObject(p), finReplicator))
	requireGone(t, e.c, p)
	t.Logf("RESULT P1 duplicates: two adds from one cached copy left %d copies; one removal call removed all", copies)
}

func rollback(t *testing.T, e *env) {
	ctx := context.Background()
	n := &counts{}
	p := newProxy("rollback")
	require.NoError(t, e.c.Create(ctx, p))
	require.NoError(t, patching(e, "httpproxy", n).add(p.DeepCopy(), finProxy))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), p))
	require.NoError(t, patching(e, "gateway-resource-replicator", n).add(p.DeepCopy(), finReplicator))
	require.NoError(t, e.c.Delete(ctx, newProxy(p.Name)))
	old := updating(e, n)
	require.NoError(t, old.remove(client.ObjectKeyFromObject(p), finProxy))
	require.NoError(t, old.remove(client.ObjectKeyFromObject(p), finReplicator))
	requireGone(t, e.c, p)
	t.Logf("RESULT P1 rollback: finalizers the patch added were removed by the old Update code")
}

func finalizerEdges(t *testing.T, e *env) {
	ctx := context.Background()
	var failures atomic.Int64
	pr := e.patcher("httpproxy", &failures)
	p := newProxy("edges")
	require.NoError(t, e.c.Create(ctx, p))
	require.Nil(t, p.Finalizers)
	wrote, err := pr.AddFinalizer(ctx, p, finProxy) // absent list: test null, then add
	require.NoError(t, err)
	require.True(t, wrote)
	wrote, err = pr.AddFinalizer(ctx, p, finProxy)
	require.NoError(t, err)
	require.False(t, wrote, "a present finalizer costs no save")

	// A stale read: the cached copy holds a finalizer list the live object no
	// longer has, so the copied patch's test fails, and the Patcher reads again.
	stale := p.DeepCopy()
	require.NoError(t, pr.RemoveFinalizer(ctx, p, finProxy))
	stale.Finalizers = []string{}
	before := failures.Load()
	wrote, err = pr.AddFinalizer(ctx, stale, finReplicator)
	require.NoError(t, err)
	require.True(t, wrote)
	require.Greater(t, failures.Load(), before, "the stale read should have failed the test once")

	// A validation rejection is returned at once: an invalid finalizer name.
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), p))
	_, err = pr.AddFinalizer(ctx, p, "not a valid finalizer!")
	require.True(t, isUnprocessable(err), "want the API's 422, got %v", err)
	t.Logf("RESULT P1 edges: a validation rejection is returned: %v", err)

	require.NoError(t, e.c.Delete(ctx, newProxy(p.Name)))
	require.NoError(t, e.c.Get(ctx, client.ObjectKeyFromObject(p), p))
	wrote, err = pr.AddFinalizer(ctx, p, finProxy)
	require.NoError(t, err)
	require.False(t, wrote, "a terminating object gets no new finalizer")
	require.NoError(t, pr.RemoveFinalizer(ctx, p, finProxy), "an absent finalizer is a no-op")
	require.NoError(t, pr.RemoveFinalizer(ctx, p, finReplicator))
	requireGone(t, e.c, p)

	// A deleted object: the patch gets 404 and creates nothing.
	wrote, err = pr.AddFinalizer(ctx, stale, finProxy)
	require.NoError(t, err)
	require.False(t, wrote)
	require.True(t, apierrors.IsNotFound(e.c.Get(ctx, client.ObjectKeyFromObject(p), &networkingv1alpha.HTTPProxy{})))
	t.Logf("RESULT P1 edges: absent list, present finalizer, stale read (one 422, sent again), terminating object, " +
		"absent finalizer, deleted object: each handled; nothing created")
}
