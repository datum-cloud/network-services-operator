// SPDX-License-Identifier: AGPL-3.0-only

package finalizer

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	mine       = "example.com/mine"
	theirs     = "example.com/theirs"
	third      = "example.com/third"
	myOwner    = "test/mine"
	theirOwner = "test/theirs"
	thirdOwner = "test/third"
	// legacyOwner is the field manager that records the operator's Update
	// saves before this package.
	legacyOwner = "network-services"
)

func startAPIServer(t *testing.T) client.WithWatch {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	env := &envtest.Environment{}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme.Scheme})
	require.NoError(t, err)
	return c
}

func create(t *testing.T, c client.Client, finalizers ...string) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		GenerateName: "finalizer-", Namespace: metav1.NamespaceDefault, Finalizers: finalizers,
	}}
	require.NoError(t, c.Create(context.Background(), cm, client.FieldOwner(legacyOwner)))
	return cm
}

func read(t *testing.T, c client.Client, cm *corev1.ConfigMap) *corev1.ConfigMap {
	t.Helper()
	live := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cm), live))
	return live
}

// update changes the object the way the operator's other saves do: an Update
// that carries the resourceVersion it read.
func update(t *testing.T, c client.Client, cm *corev1.ConfigMap, change func(*corev1.ConfigMap)) {
	t.Helper()
	live := read(t, c, cm)
	change(live)
	require.NoError(t, c.Update(context.Background(), live, client.FieldOwner(legacyOwner)))
}

func TestFinalizer(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()

	t.Run("a stale read still saves when another writer changed something else", func(t *testing.T) {
		added, removed := create(t, c, theirs), create(t, c, theirs, mine)
		staleAdded, staleRemoved := added.DeepCopy(), removed.DeepCopy()
		for _, cm := range []*corev1.ConfigMap{added, removed} {
			update(t, c, cm, func(o *corev1.ConfigMap) { o.Labels = map[string]string{"other": "writer"} })
		}

		require.NoError(t, Add(ctx, c, staleAdded, mine, myOwner))
		require.NoError(t, Remove(ctx, on(c, c), staleRemoved, mine, myOwner))

		require.Equal(t, []string{theirs, mine}, read(t, c, added).Finalizers)
		require.Equal(t, []string{theirs}, read(t, c, removed).Finalizers)
		require.Equal(t, "writer", read(t, c, added).Labels["other"])
		require.Equal(t, "writer", read(t, c, removed).Labels["other"])
	})

	t.Run("two controllers add to an absent list at the same moment", func(t *testing.T) {
		cm := create(t, c)
		var wg sync.WaitGroup
		for name, owner := range map[string]string{mine: myOwner, theirs: theirOwner} {
			stale := cm.DeepCopy()
			wg.Go(func() { assert.NoError(t, Add(ctx, c, stale, name, owner)) })
		}
		wg.Wait()
		require.ElementsMatch(t, []string{mine, theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("a finalizer already present is not written", func(t *testing.T) {
		cm := create(t, c, mine)
		require.NoError(t, Add(ctx, c, cm, mine, myOwner))
		require.Equal(t, cm.ResourceVersion, read(t, c, cm).ResourceVersion)
	})

	t.Run("an absent finalizer is not removed", func(t *testing.T) {
		cm := create(t, c, theirs)
		require.NoError(t, Remove(ctx, on(c, c), cm, mine, myOwner))
		require.Equal(t, cm.ResourceVersion, read(t, c, cm).ResourceVersion)
	})

	t.Run("an entry the legacy manager recorded is removed", func(t *testing.T) {
		cm := create(t, c, theirs)
		update(t, c, cm, func(o *corev1.ConfigMap) { o.Finalizers = append(o.Finalizers, mine) })
		require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner))
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("a remove from a stale copy takes the entry's index from the live read", func(t *testing.T) {
		cm := create(t, c, theirs, mine)
		stale := cm.DeepCopy()
		update(t, c, cm, func(o *corev1.ConfigMap) { o.Finalizers = []string{mine} })

		require.NoError(t, Remove(ctx, on(c, c), stale, mine, myOwner))
		require.Empty(t, read(t, c, cm).Finalizers)
	})

	t.Run("a remove whose list moves after the read removes nothing and succeeds after a fresh read", func(t *testing.T) {
		cm := create(t, c, theirs, mine)
		moving := interceptor.NewClient(c, interceptor.Funcs{
			Patch: func(ctx context.Context, w client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				update(t, w, cm, func(o *corev1.ConfigMap) { o.Finalizers = []string{mine} })
				return w.Patch(ctx, obj, patch, opts...)
			},
		})

		require.True(t, apierrors.IsInvalid(Remove(ctx, on(moving, c), read(t, c, cm), mine, myOwner)))
		require.Equal(t, []string{mine}, read(t, c, cm).Finalizers)
		require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner))
		require.Empty(t, read(t, c, cm).Finalizers)
	})

	t.Run("a terminating object takes no new finalizer", func(t *testing.T) {
		cm := create(t, c, theirs)
		require.NoError(t, c.Delete(ctx, cm))
		require.Error(t, Add(ctx, c, read(t, c, cm), mine, myOwner))
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("a deleted object is not created again", func(t *testing.T) {
		cm := create(t, c, mine)
		update(t, c, cm, func(o *corev1.ConfigMap) { o.Finalizers = nil })
		require.NoError(t, c.Delete(ctx, cm))

		require.Error(t, Add(ctx, c, cm, theirs, myOwner))
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})))
		require.NoError(t, Remove(ctx, on(c, c), cm, mine, myOwner), "an object already gone holds no finalizer")
	})

	t.Run("an object created again under the same name is not touched", func(t *testing.T) {
		old := create(t, c)
		require.NoError(t, c.Delete(ctx, old))
		again := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: old.Name, Namespace: old.Namespace}}
		require.NoError(t, c.Create(ctx, again))

		require.Error(t, Add(ctx, c, old, mine, myOwner))
		require.Empty(t, read(t, c, again).Finalizers)
	})

	t.Run("each remove takes one copy of a duplicate", func(t *testing.T) {
		cm := create(t, c, mine, theirs, mine)
		require.NoError(t, Remove(ctx, on(c, c), cm, mine, myOwner))
		require.Equal(t, []string{theirs, mine}, read(t, c, cm).Finalizers)
		require.NoError(t, Remove(ctx, on(c, c), cm, mine, myOwner))
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("removing the last finalizer releases a deleting object", func(t *testing.T) {
		cm := create(t, c, mine)
		require.NoError(t, c.Delete(ctx, cm))
		require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner))
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})))
	})
}

// held returns a ConfigMap whose finalizers each controller added by its own apply: theirs, then mine.
func held(t *testing.T, c client.Client) *corev1.ConfigMap {
	t.Helper()
	cm := create(t, c)
	require.NoError(t, Add(context.Background(), c, read(t, c, cm), theirs, theirOwner))
	require.NoError(t, Add(context.Background(), c, read(t, c, cm), mine, myOwner))
	return read(t, c, cm)
}

// on returns the Cluster Remove takes: it writes through c and reads through reader.
func on(c client.Client, reader client.Reader) Cluster { return testCluster{client: c, reader: reader} }

type testCluster struct {
	client client.Client
	reader client.Reader
}

func (t testCluster) GetClient() client.Client    { return t.client }
func (t testCluster) GetAPIReader() client.Reader { return t.reader }

func removed(outcome string) float64 {
	return testutil.ToFloat64(removals.WithLabelValues(myOwner, outcome))
}

// recreate deletes cm and creates another ConfigMap under its name, held by both controllers in the same order, so
// only the object's UID tells the two apart.
func recreate(t *testing.T, c client.Client, cm *corev1.ConfigMap) *corev1.ConfigMap {
	t.Helper()
	ctx := context.Background()
	live := read(t, c, cm)
	live.Finalizers = nil
	require.NoError(t, c.Update(ctx, live))
	require.NoError(t, c.Delete(ctx, live))
	again := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cm.Name, Namespace: cm.Namespace}}
	require.NoError(t, c.Create(ctx, again))
	require.NoError(t, Add(ctx, c, read(t, c, again), theirs, theirOwner))
	require.NoError(t, Add(ctx, c, read(t, c, again), mine, myOwner))
	return read(t, c, again)
}

func TestRemoveReadsLive(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()

	t.Run("a removal already done ends without a write", func(t *testing.T) {
		cm := held(t, c)
		stale := cm.DeepCopy()
		require.NoError(t, Remove(ctx, on(c, c), cm.DeepCopy(), mine, myOwner))
		before, version := removed(outcomeAlreadyRemoved), read(t, c, cm).ResourceVersion
		require.NoError(t, Remove(ctx, on(c, c), stale, mine, myOwner))
		require.Equal(t, before+1, removed(outcomeAlreadyRemoved))
		require.Equal(t, version, read(t, c, cm).ResourceVersion)
	})

	t.Run("an entry another manager owns too is removed", func(t *testing.T) {
		cm := create(t, c, theirs, mine) // recorded by the legacy manager
		require.NoError(t, Add(ctx, c, read(t, c, cm), mine, myOwner))
		require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner))
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("an object created again before the read is left alone", func(t *testing.T) {
		cm := held(t, c)
		again := recreate(t, c, cm)
		before := removed(outcomeReplaced)
		require.NoError(t, Remove(ctx, on(c, c), cm, mine, myOwner))
		require.Equal(t, before+1, removed(outcomeReplaced))
		require.Equal(t, []string{theirs, mine}, read(t, c, again).Finalizers)
	})

	t.Run("an object created again between the read and the patch is a conflict, then left alone", func(t *testing.T) {
		cm := held(t, c)
		var again *corev1.ConfigMap
		replacing := interceptor.NewClient(c, interceptor.Funcs{
			Patch: func(ctx context.Context, w client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				again = recreate(t, w, cm)
				return w.Patch(ctx, obj, patch, opts...)
			},
		})
		conflicts := removed(outcomeConflict)
		require.True(t, apierrors.IsInvalid(Remove(ctx, on(replacing, c), cm.DeepCopy(), mine, myOwner)), "the UID test fails")
		require.Equal(t, conflicts+1, removed(outcomeConflict))
		require.Equal(t, []string{theirs, mine}, read(t, c, again).Finalizers)

		replaced := removed(outcomeReplaced)
		require.NoError(t, Remove(ctx, on(c, c), cm, mine, myOwner), "the requeue reads the new UID")
		require.Equal(t, replaced+1, removed(outcomeReplaced))
		require.Equal(t, []string{theirs, mine}, read(t, c, again).Finalizers)
	})

	t.Run("an object gone between the read and the patch is done", func(t *testing.T) {
		cm := held(t, c)
		vanishing := interceptor.NewClient(c, interceptor.Funcs{
			Patch: func(ctx context.Context, w client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				live := read(t, w, cm)
				live.Finalizers = nil
				require.NoError(t, w.Update(ctx, live))
				require.NoError(t, w.Delete(ctx, live))
				return w.Patch(ctx, obj, patch, opts...)
			},
		})
		before := removed(outcomeGone)
		require.NoError(t, Remove(ctx, on(vanishing, c), cm, mine, myOwner))
		require.Equal(t, before+1, removed(outcomeGone))
	})

	t.Run("a read that fails is returned, and the next attempt removes the entry", func(t *testing.T) {
		cm := held(t, c)
		var failed atomic.Bool
		failingOnce := interceptor.NewClient(c, interceptor.Funcs{
			Get: func(ctx context.Context, w client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if failed.CompareAndSwap(false, true) {
					return apierrors.NewInternalError(errors.New("read refused"))
				}
				return w.Get(ctx, key, obj, opts...)
			},
		})
		errs := removed(outcomeError)
		require.Error(t, Remove(ctx, on(c, failingOnce), cm.DeepCopy(), mine, myOwner))
		require.Equal(t, errs+1, removed(outcomeError))
		require.Equal(t, []string{theirs, mine}, read(t, c, cm).Finalizers)
		require.NoError(t, Remove(ctx, on(c, failingOnce), cm, mine, myOwner), "the requeue")
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("a list that moves between the read and the patch onto another entry is a conflict, and that entry stays", func(t *testing.T) {
		cm := held(t, c)
		require.NoError(t, Add(ctx, c, read(t, c, cm), third, thirdOwner))
		// The read finds [theirs, mine, third], so ours is at 1. Theirs leaves before our patch, which puts third at 1.
		moving := interceptor.NewClient(c, interceptor.Funcs{
			Patch: func(ctx context.Context, w client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				require.NoError(t, removeAsToday(ctx, w, read(t, w, cm), theirs, theirOwner))
				return w.Patch(ctx, obj, patch, opts...)
			},
		})
		conflicts := removed(outcomeConflict)
		require.True(t, apierrors.IsInvalid(Remove(ctx, on(moving, c), cm.DeepCopy(), mine, myOwner)))
		require.Equal(t, conflicts+1, removed(outcomeConflict))
		require.Equal(t, []string{mine, third}, read(t, c, cm).Finalizers)
	})

	t.Run("an object without a UID is an error, and nothing is written", func(t *testing.T) {
		cm := held(t, c)
		version, noUID := cm.ResourceVersion, cm.DeepCopy()
		noUID.UID = ""
		require.ErrorContains(t, Remove(ctx, on(c, c), noUID, mine, myOwner), "no UID")
		require.Equal(t, version, read(t, c, cm).ResourceVersion)
	})

	t.Run("the read takes the object's metadata only", func(t *testing.T) {
		cm := held(t, c)
		var got client.Object
		recording := interceptor.NewClient(c, interceptor.Funcs{
			Get: func(ctx context.Context, w client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				got = obj
				return w.Get(ctx, key, obj, opts...)
			},
		})
		require.NoError(t, Remove(ctx, on(c, recording), cm, mine, myOwner))
		require.IsType(t, &metav1.PartialObjectMetadata{}, got)
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})
}

// TestRemoveRefusedByAPolicyIsInvalid tells a removal the API server refuses on
// its merits from one it could not apply. A ValidatingAdmissionPolicy that
// refuses the update answers 422 with a cause, and its outcome is invalid, not
// conflict: a requeue does not cure it, so it must not read as the race.
func TestRemoveRefusedByAPolicyIsInvalid(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()
	const label = "finalizer-test/refuse-updates"
	cm := held(t, c)
	update(t, c, cm, func(o *corev1.ConfigMap) { o.Labels = map[string]string{label: "true"} })

	policy := &admissionregistrationv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "refuse-updates-"},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicySpec{
			MatchConstraints: &admissionregistrationv1.MatchResources{
				ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Update},
						Rule: admissionregistrationv1.Rule{
							APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"configmaps"},
						},
					},
				}},
			},
			Validations: []admissionregistrationv1.Validation{{
				Expression: "!has(object.metadata.labels) || !('" + label + "' in object.metadata.labels)",
				Message:    "updates are refused",
			}},
		},
	}
	require.NoError(t, c.Create(ctx, policy))
	binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "refuse-updates-"},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        policy.Name,
			ValidationActions: []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny},
		},
	}
	require.NoError(t, c.Create(ctx, binding))
	// The policy takes effect a moment after its binding is created.
	require.Eventually(t, func() bool {
		probe := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(cm), probe); err != nil {
			return false
		}
		probe.Data = map[string]string{"probe": "refused"}
		return apierrors.IsInvalid(c.Update(ctx, probe, client.DryRunAll))
	}, 30*time.Second, 200*time.Millisecond)

	invalids, conflicts := removed(outcomeInvalid), removed(outcomeConflict)
	err := Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner)
	require.True(t, apierrors.IsInvalid(err), "%v", err)
	require.Equal(t, invalids+1, removed(outcomeInvalid))
	require.Equal(t, conflicts, removed(outcomeConflict))
	require.Equal(t, []string{theirs, mine}, read(t, c, cm).Finalizers)
}

// TestRemoveLeavesEveryOtherFinalizer holds the removal to its own entry: the
// other controller's, a third manager's, and the API server's orphan and
// foregroundDeletion, which an apply would drop (ticket 13, F17).
func TestRemoveLeavesEveryOtherFinalizer(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()

	for _, policy := range []metav1.DeletionPropagation{metav1.DeletePropagationOrphan, metav1.DeletePropagationForeground} {
		t.Run("ours alone, deleted with "+string(policy), func(t *testing.T) {
			cm := create(t, c)
			require.NoError(t, Add(ctx, c, read(t, c, cm), mine, myOwner))
			require.NoError(t, c.Delete(ctx, read(t, c, cm), client.PropagationPolicy(policy)))
			server := metav1.FinalizerOrphanDependents
			if policy == metav1.DeletePropagationForeground {
				server = metav1.FinalizerDeleteDependents
			}
			require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner))
			require.Equal(t, []string{server}, read(t, c, cm).Finalizers)
		})
	}

	t.Run("both controllers and a third manager, deleted in the foreground", func(t *testing.T) {
		cm := held(t, c)
		require.NoError(t, Add(ctx, c, read(t, c, cm), third, thirdOwner))
		require.NoError(t, c.Delete(ctx, read(t, c, cm), client.PropagationPolicy(metav1.DeletePropagationForeground)))
		require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), mine, myOwner))
		require.ElementsMatch(t, []string{theirs, third, metav1.FinalizerDeleteDependents}, read(t, c, cm).Finalizers)
		require.NoError(t, Remove(ctx, on(c, c), read(t, c, cm), theirs, theirOwner))
		require.ElementsMatch(t, []string{third, metav1.FinalizerDeleteDependents}, read(t, c, cm).Finalizers)
	})
}

// TestRemoveBesideTodaysRemoval runs this removal and the one before it on one
// deleting object, as a rollout or a rollback does when the two releases'
// leaders hand over between the controllers' removals (ticket 13, A9). Each
// case removes the first entry first, so the second removal's stale copy holds
// the wrong index.
func TestRemoveBesideTodaysRemoval(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()

	t.Run("a rollout: the previous release removes first, this one from a stale copy", func(t *testing.T) {
		stale := deleting(t, c)
		require.NoError(t, removeAsToday(ctx, c, stale.DeepCopy(), theirs, theirOwner))
		patched := removed(outcomePatch)
		require.NoError(t, Remove(ctx, on(c, c), stale.DeepCopy(), mine, myOwner))
		require.Equal(t, patched+1, removed(outcomePatch), "one patch, from the live index")
		requireGone(t, c, stale)
	})

	t.Run("a rollback: this release removes first, the previous one after its requeue", func(t *testing.T) {
		stale := deleting(t, c)
		require.NoError(t, Remove(ctx, on(c, c), stale.DeepCopy(), theirs, theirOwner))
		require.True(t, apierrors.IsInvalid(removeAsToday(ctx, c, stale.DeepCopy(), mine, myOwner)),
			"the stale index is rejected, as before this change")
		require.NoError(t, removeAsToday(ctx, c, read(t, c, stale), mine, myOwner))
		requireGone(t, c, stale)
	})
}

// deleting returns a ConfigMap held by both controllers, theirs first, as the
// copy each controller holds right after its deletion.
func deleting(t *testing.T, c client.Client) *corev1.ConfigMap {
	t.Helper()
	cm := held(t, c)
	require.NoError(t, c.Delete(context.Background(), cm))
	return read(t, c, cm)
}

func requireGone(t *testing.T, c client.Client, cm *corev1.ConfigMap) {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
	require.True(t, apierrors.IsNotFound(err), "the object is released: %v", err)
}

// removesOnlyItsOwn reports whether every remove in a finalizer patch follows a
// test of the caller's own finalizer at the same path. The caller is named by
// the patch's field manager, as each controller names itself.
func removesOnlyItsOwn(patch client.Patch, obj client.Object, opts []client.PatchOption) bool {
	own := map[string]string{myOwner: mine, theirOwner: theirs}
	options := &client.PatchOptions{}
	options.ApplyOptions(opts)
	data, err := patch.Data(obj)
	if err != nil {
		return false
	}
	var ops []struct {
		Op, Path string
		Value    any
	}
	if err := json.Unmarshal(data, &ops); err != nil {
		return false
	}
	for i, op := range ops {
		if op.Op != "remove" {
			continue
		}
		if i == 0 || ops[i-1].Op != "test" || ops[i-1].Path != op.Path || ops[i-1].Value != own[options.FieldManager] {
			return false
		}
	}
	return true
}

// removeAsToday is the removal before this change: the tested patch, its index
// read from the copy the controller holds. The stress run compares against it.
func removeAsToday(ctx context.Context, c client.Client, obj client.Object, name, fieldOwner string) error {
	if !controllerutil.ContainsFinalizer(obj, name) {
		return nil
	}
	patch, err := removeFinalizerPatch(obj.GetFinalizers(), name)
	if err != nil {
		return err
	}
	return client.IgnoreNotFound(c.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, patch), client.FieldOwner(fieldOwner)))
}

// The stress load: how many objects each round deletes, and how many rounds.
const stressObjects, stressIterations = 50, 20

// TestRemovalUnderConcurrency deletes many objects at once, each held by both
// controllers and some also by a third manager or the garbage collector's
// foregroundDeletion, while the two controllers remove their entries in random
// order from stale copies, repeating themselves. Every object must end with
// only what neither controller owns: none stuck, none removed wrongly. It
// reports the 422s of today's removal and of this one on the same load.
func TestRemovalUnderConcurrency(t *testing.T) {
	base := startAPIServer(t)
	ctx := context.Background()

	alreadyRemoved := removed(outcomeAlreadyRemoved)
	today := newStressLoad(base).run(ctx, t, func(c client.Client, obj client.Object, name, owner string) error {
		return removeAsToday(ctx, c, obj, name, owner)
	})
	live := newStressLoad(base).run(ctx, t, func(c client.Client, obj client.Object, name, owner string) error {
		return Remove(ctx, on(c, base), obj, name, owner)
	})
	t.Logf("422s on the same load: today's removal %d; with the live read %d (concurrent residual); "+
		"stale-copy removals the live read ended without a write (already_removed, test/mine): %v",
		today, live, removed(outcomeAlreadyRemoved)-alreadyRemoved)
	require.Less(t, live, today, "the live read removes the stale-copy 422s")
}

// remover is one controller's removal under test: today's, or this package's.
type remover func(c client.Client, obj client.Object, name, owner string) error

// stressLoad is one run of the stress load against an API server: the client
// whose patches are checked and counted, and the random order, the same for
// every run.
type stressLoad struct {
	base            client.WithWatch
	c               client.Client
	random          *rand.Rand
	rejected, wrong atomic.Int64
}

func newStressLoad(base client.WithWatch) *stressLoad {
	l := &stressLoad{base: base, random: rand.New(rand.NewPCG(1, 2))}
	l.c = interceptor.NewClient(base, interceptor.Funcs{Patch: l.patch})
	return l
}

// patch counts a patch that removes an entry its caller does not own, and a
// patch the API server rejects (422).
func (l *stressLoad) patch(ctx context.Context, w client.WithWatch, obj client.Object, patch client.Patch,
	opts ...client.PatchOption) error {
	if !removesOnlyItsOwn(patch, obj, opts) {
		l.wrong.Add(1)
	}
	err := w.Patch(ctx, obj, patch, opts...)
	if apierrors.IsInvalid(err) {
		l.rejected.Add(1)
	}
	return err
}

// run deletes the stress load's objects round by round, with remove as both
// controllers' removal, checks every object after each round, and returns the
// 422s.
func (l *stressLoad) run(ctx context.Context, t *testing.T, remove remover) int64 {
	for iteration := range stressIterations {
		objs, keeps := seed(ctx, t, l.base)
		l.removeConcurrently(ctx, t, objs, remove)
		require.Zero(t, l.wrong.Load(), "iteration %d: a patch removed an entry its caller does not own", iteration)
		checkLists(ctx, t, l.base, iteration, objs, keeps)
	}
	return l.rejected.Load()
}

// seed creates the round's objects, each held by both controllers; every third
// also by a third manager, every fourth deleted in the foreground. It deletes
// them, and returns the copies the controllers hold and what each object must
// keep.
func seed(ctx context.Context, t *testing.T, base client.WithWatch) ([]*corev1.ConfigMap, [][]string) {
	objs := make([]*corev1.ConfigMap, stressObjects)
	keeps := make([][]string, stressObjects)
	for i := range objs {
		cm := held(t, base)
		if i%3 == 0 {
			require.NoError(t, Add(ctx, base, read(t, base, cm), third, thirdOwner))
			keeps[i] = append(keeps[i], third)
		}
		policy := metav1.DeletePropagationBackground
		if i%4 == 0 {
			policy = metav1.DeletePropagationForeground
			keeps[i] = append(keeps[i], metav1.FinalizerDeleteDependents)
		}
		require.NoError(t, base.Delete(ctx, read(t, base, cm), client.PropagationPolicy(policy)))
		objs[i] = read(t, base, cm)
	}
	return objs, keeps
}

// removeConcurrently has both controllers remove their entries from every
// object at once, in random order, each from its stale copy and after a random
// pause, repeating itself a random number of times.
func (l *stressLoad) removeConcurrently(ctx context.Context, t *testing.T, objs []*corev1.ConfigMap, remove remover) {
	var wg sync.WaitGroup
	for _, cm := range objs {
		for _, who := range l.random.Perm(2) {
			name, owner := mine, myOwner
			if who == 1 {
				name, owner = theirs, theirOwner
			}
			stale, repeats := cm.DeepCopy(), 1+l.random.IntN(3)
			pause := time.Duration(l.random.IntN(3)) * time.Millisecond
			wg.Go(func() {
				time.Sleep(pause)
				l.removeRepeatedly(ctx, t, stale, name, owner, repeats, remove)
			})
		}
	}
	wg.Wait()
}

// removeRepeatedly is one controller's removal, repeated as requeues repeat it:
// a refused removal reads its copy again and tries up to five times, and an
// object already gone ends the attempts.
func (l *stressLoad) removeRepeatedly(ctx context.Context, t *testing.T, stale *corev1.ConfigMap,
	name, owner string, repeats int, remove remover) {
	for range repeats {
		for attempt := 0; attempt < 5; attempt++ {
			err := remove(l.c, stale.DeepCopy(), name, owner)
			if err == nil {
				break
			}
			if !assert.True(t, apierrors.IsInvalid(err), "only a moved list may refuse a removal: %v", err) {
				return
			}
			if apierrors.IsNotFound(l.base.Get(ctx, client.ObjectKeyFromObject(stale), stale)) {
				break
			}
		}
	}
}

// checkLists requires every object to end with exactly what it must keep, and
// an object that keeps nothing to be gone.
func checkLists(ctx context.Context, t *testing.T, base client.WithWatch, iteration int,
	objs []*corev1.ConfigMap, keeps [][]string) {
	for i, cm := range objs {
		live := &corev1.ConfigMap{}
		err := base.Get(ctx, client.ObjectKeyFromObject(cm), live)
		if len(keeps[i]) == 0 {
			require.True(t, apierrors.IsNotFound(err), "iteration %d, object %d is stuck: %v", iteration, i, live.Finalizers)
			continue
		}
		require.NoError(t, err, "iteration %d, object %d should still hold %v", iteration, i, keeps[i])
		require.ElementsMatch(t, keeps[i], live.Finalizers, "iteration %d, object %d", iteration, i)
	}
}
