// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// TestConnectorAdvertisementRepoint covers issue #571: an advertisement whose
// connectorRef moves to another connector, as the desktop app does when a
// tunnel is edited from a second device.
//
// envtest runs no garbage collector, so the garbage collection cases assert
// what the collector reads — the advertisement's ownerReferences — through
// collectedWhenGone, which applies the collector's rule to them.
func TestConnectorAdvertisementRepoint(t *testing.T) {
	cl := startRepointEnv(t)
	r := reconcilerWith(cl)

	t.Run("re-pointing hands ownership to the new connector, and back", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		require.True(t, metav1.IsControlledBy(getAdvertisement(t, cl, ad), previous), "setup: the first connector must own the advertisement")

		repoint(t, cl, ad, next.Name)

		require.NoError(t, reconcileAdvertisement(ctx, r, ad), "the reconcile after a re-point must succeed")
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, next), "the new connector must control the advertisement")
		require.False(t, referencesUID(got, previous.UID), "no owner reference may name the previous connector")
		require.True(t, collectedWhenGone(got, next.UID), "the advertisement must go with the connector it now names")
		requireAcceptedForCurrentGeneration(t, got)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
		require.Contains(t, cond.Message, fmt.Sprintf("%q", next.Name), "Accepted must name the connector it now points at")

		repoint(t, cl, ad, previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad), "the reconcile after re-pointing back must succeed")
		got = getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, previous), "re-pointing back must hand ownership back")
		require.False(t, referencesUID(got, next.UID))
		requireAcceptedForCurrentGeneration(t, got)
	})

	t.Run("the first owner set on an advertisement without owner references", func(t *testing.T) {
		// The first set tests a missing ownerReferences against null, which
		// json-patch v4 accepts and RFC 6902 does not. This case fails if
		// the API server stops accepting it.
		ctx, ns := context.Background(), newNamespace(t, cl)
		connector := createConnector(t, cl, ns, "laptop")
		ad := createAdvertisement(t, cl, ns, "tunnel", connector.Name)
		require.Empty(t, getAdvertisement(t, cl, ad).GetOwnerReferences(), "setup: a new advertisement has no owner")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.Len(t, got.GetOwnerReferences(), 1)
		require.True(t, metav1.IsControlledBy(got, connector))
		requireAcceptedForCurrentGeneration(t, got)
	})

	t.Run("a connector re-created under the same name takes the advertisement over", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		first := createConnector(t, cl, ns, "laptop")
		ad := createAdvertisement(t, cl, ns, "tunnel", first.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		require.NoError(t, cl.Delete(ctx, first))
		again := createConnector(t, cl, ns, "laptop")
		require.NotEqual(t, first.UID, again.UID, "setup: the re-created connector is a new object")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, again))
		require.False(t, referencesUID(got, first.UID), "no owner reference may name the deleted connector")
	})

	t.Run("a re-point keeps owner references that are not controllers", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		other := createConnector(t, cl, ns, "spare") // a connector, so only the controller check keeps it
		ad := &networkingv1alpha1.ConnectorAdvertisement{
			ObjectMeta: metav1.ObjectMeta{Name: "tunnel", Namespace: ns},
			Spec:       networkingv1alpha1.ConnectorAdvertisementSpec{ConnectorRef: networkingv1alpha1.LocalConnectorReference{Name: previous.Name}},
		}
		require.NoError(t, controllerutil.SetOwnerReference(other, ad, cl.Scheme()))
		require.NoError(t, cl.Create(ctx, ad))
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))

		repoint(t, cl, ad, next.Name)

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, next))
		require.False(t, referencesUID(got, previous.UID))
		require.True(t, referencesUID(got, other.UID), "an owner reference that is not a controller must stay")
	})

	t.Run("an edit to another spec field during the reconcile fails neither write", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		repoint(t, cl, ad, next.Name)

		// The app edits layer4 before each of the reconcile's two writes.
		edits := 0
		racing := racedBy(cl, func(c client.Client) {
			edits++
			editLayer4(t, c, ad, edits)
		})

		require.NoError(t, reconcileAdvertisement(ctx, racing, ad), "an edit to layer4 must fail neither the hand-over nor the status save")
		require.Equal(t, 2, edits, "setup: the app edited before both writes")
		require.True(t, metav1.IsControlledBy(getAdvertisement(t, cl, ad), next))
	})

	t.Run("a second re-point during the hand-over is refused, and the next reconcile follows it", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		first, second, third := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop"), createConnector(t, cl, ns, "phone")
		ad := createAdvertisement(t, cl, ns, "tunnel", first.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		repoint(t, cl, ad, second.Name)

		racing := racedBy(cl, func(c client.Client) { repoint(t, c, ad, third.Name) })

		err := reconcileAdvertisement(ctx, racing, ad)
		require.True(t, apierrors.IsInvalid(err), "the hand-over to %q must be refused once the spec names %q: %v", second.Name, third.Name, err)
		require.True(t, metav1.IsControlledBy(getAdvertisement(t, cl, ad), first), "a refused hand-over writes nothing")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, third))
		require.False(t, referencesUID(got, first.UID))
		requireAcceptedForCurrentGeneration(t, got)
	})

	t.Run("a hand-over built from an old owner list writes nothing", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		other := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: ns}}
		require.NoError(t, cl.Create(ctx, other))
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		repoint(t, cl, ad, next.Name)

		// Another writer adds an owner after the reconcile read the list.
		racing := racedBy(cl, func(c client.Client) {
			live := getAdvertisement(t, c, ad)
			require.NoError(t, controllerutil.SetOwnerReference(other, live, c.Scheme()))
			require.NoError(t, c.Update(ctx, live))
		})

		err := reconcileAdvertisement(ctx, racing, ad)
		require.True(t, apierrors.IsInvalid(err), "a patch built from an old owner list must be refused: %v", err)
		require.True(t, metav1.IsControlledBy(getAdvertisement(t, cl, ad), previous), "a refused hand-over writes nothing")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, next))
		require.True(t, referencesUID(got, other.UID), "the owner added in between must stay")
	})

	t.Run("the reconcile after a hand-over writes nothing", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		repoint(t, cl, ad, next.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))

		// The hand-over's own write enqueues the advertisement again; that
		// reconcile must find nothing to do, or the two would loop.
		writes := 0
		counting := racedBy(cl, func(client.Client) { writes++ })
		require.NoError(t, reconcileAdvertisement(ctx, counting, ad))
		require.Zero(t, writes, "a settled advertisement must not be written again")
	})

	t.Run("an advertisement already stuck recovers on the first reconcile", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		// The state v0.32.5 leaves behind: owned by the previous connector,
		// pointing at the next one, Accepted for an older generation.
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		repoint(t, cl, ad, next.Name)
		require.Error(t, reconcileAdvertisementWith(ctx, legacyReconcile(cl), ad), "setup: the v0.32.5 path must refuse the re-point")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad), "the first reconcile after upgrading must succeed")
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, next))
		require.False(t, referencesUID(got, previous.UID))
		requireAcceptedForCurrentGeneration(t, got)
	})

	t.Run("deleting the previous connector leaves a re-pointed advertisement in place", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		repoint(t, cl, ad, next.Name)
		_ = reconcileAdvertisement(ctx, r, ad) // the stuck path returns an error; the owner references are what matter

		require.NoError(t, cl.Delete(ctx, previous))
		require.False(t, collectedWhenGone(getAdvertisement(t, cl, ad), previous.UID),
			"the garbage collector would delete the advertisement with its previous connector")
	})

	t.Run("control: an advertisement never re-pointed goes with its connector", func(t *testing.T) {
		// Proves collectedWhenGone can say yes: without it the case above
		// could pass on an instrument that never reports a collection.
		ctx, ns := context.Background(), newNamespace(t, cl)
		owner := createConnector(t, cl, ns, "laptop")
		ad := createAdvertisement(t, cl, ns, "tunnel", owner.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))

		require.NoError(t, cl.Delete(ctx, owner))
		require.True(t, collectedWhenGone(getAdvertisement(t, cl, ad), owner.UID))
	})

	t.Run("re-pointing to a connector that does not exist keeps the current owner", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous := createConnector(t, cl, ns, "laptop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))

		repoint(t, cl, ad, "not-created-yet")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, previous), "an unresolved reference must not drop the owner")
		cond := apimeta.FindStatusCondition(got.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
		require.NotNil(t, cond)
		require.Equal(t, networkingv1alpha1.ConnectorAdvertisementReasonConnectorNotFound, cond.Reason)
		require.Equal(t, got.Generation, cond.ObservedGeneration)
	})

	t.Run("a controller of another kind is not taken over", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		connector := createConnector(t, cl, ns, "laptop")
		foreign := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: ns}}
		require.NoError(t, cl.Create(ctx, foreign))
		ad := &networkingv1alpha1.ConnectorAdvertisement{
			ObjectMeta: metav1.ObjectMeta{Name: "tunnel", Namespace: ns},
			Spec:       networkingv1alpha1.ConnectorAdvertisementSpec{ConnectorRef: networkingv1alpha1.LocalConnectorReference{Name: connector.Name}},
		}
		require.NoError(t, controllerutil.SetControllerReference(foreign, ad, cl.Scheme()))
		require.NoError(t, cl.Create(ctx, ad))

		require.Error(t, reconcileAdvertisement(ctx, r, ad))
		require.True(t, metav1.IsControlledBy(getAdvertisement(t, cl, ad), foreign))
	})

	t.Run("a connector of another API group is not taken over", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		connector := createConnector(t, cl, ns, "laptop")
		foreign := metav1.OwnerReference{APIVersion: "connectors.example.com/v1", Kind: "Connector", Name: connector.Name,
			UID: "4a1c1d7e-0000-4000-8000-000000000001", Controller: ptr.To(true)}
		ad := &networkingv1alpha1.ConnectorAdvertisement{
			ObjectMeta: metav1.ObjectMeta{Name: "tunnel", Namespace: ns, OwnerReferences: []metav1.OwnerReference{foreign}},
			Spec:       networkingv1alpha1.ConnectorAdvertisementSpec{ConnectorRef: networkingv1alpha1.LocalConnectorReference{Name: connector.Name}},
		}
		require.NoError(t, cl.Create(ctx, ad))

		require.Error(t, reconcileAdvertisement(ctx, r, ad))
		require.Equal(t, []metav1.OwnerReference{foreign}, getAdvertisement(t, cl, ad).GetOwnerReferences())
	})

	t.Run("a connector that is being deleted does not take the advertisement over", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		holdInDeletion(t, cl, next)

		repoint(t, cl, ad, next.Name)

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, previous), "the current owner must stay")
		require.False(t, referencesUID(got, next.UID))
		cond := apimeta.FindStatusCondition(got.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
		require.NotNil(t, cond)
		require.Equal(t, metav1.ConditionFalse, cond.Status)
		require.Equal(t, networkingv1alpha1.ConnectorAdvertisementReasonConnectorNotFound, cond.Reason)
		require.Contains(t, cond.Message, "being deleted")
		require.Equal(t, got.Generation, cond.ObservedGeneration)
	})

	t.Run("a connector created again is not taken from a stale cache", func(t *testing.T) {
		ctx, ns := context.Background(), newNamespace(t, cl)
		previous, next := createConnector(t, cl, ns, "laptop"), createConnector(t, cl, ns, "desktop")
		ad := createAdvertisement(t, cl, ns, "tunnel", previous.Name)
		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		stale := next.DeepCopy()
		require.NoError(t, cl.Delete(ctx, next))
		again := createConnector(t, cl, ns, next.Name)
		repoint(t, cl, ad, next.Name)

		// The cache still holds the deleted connector, whole and as metadata;
		// the API reader does not.
		staleCache := &ConnectorAdvertisementReconciler{mgr: &fakeMockManager{reader: cl, cl: interceptor.NewClient(cl, interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if key != client.ObjectKeyFromObject(stale) {
					return c.Get(ctx, key, obj, opts...)
				}
				switch held := obj.(type) {
				case *networkingv1alpha1.Connector:
					stale.DeepCopyInto(held)
				case *metav1.PartialObjectMetadata:
					stale.ObjectMeta.DeepCopyInto(&held.ObjectMeta)
				default:
					return c.Get(ctx, key, obj, opts...)
				}
				return nil
			},
		})}}

		require.Error(t, reconcileAdvertisement(ctx, staleCache, ad), "a connector the cache holds under a dead UID must not take the advertisement")
		got := getAdvertisement(t, cl, ad)
		require.True(t, metav1.IsControlledBy(got, previous))
		require.False(t, referencesUID(got, stale.UID), "no owner reference may name a deleted connector")

		require.NoError(t, reconcileAdvertisement(ctx, r, ad))
		require.True(t, metav1.IsControlledBy(getAdvertisement(t, cl, ad), again))
	})
}

// TestConnectorAdvertisementRepointUnderConcurrency re-points many
// advertisements at once while the app also edits their layer4 services, and
// the reconciler runs against both, retrying a refused write as the work queue
// does. Every advertisement must end controlled by the connector its spec
// names and Accepted for its current generation. A watch records every stored
// version: none may have other than one connector as its controller, so no
// hand-over leaves an advertisement for the garbage collector to take.
func TestConnectorAdvertisementRepointUnderConcurrency(t *testing.T) {
	const advertisements, connectors, steps = 30, 4, 8
	cl := startRepointEnv(t)
	ctx, ns := context.Background(), newNamespace(t, cl)
	r := reconcilerWith(cl)

	devices := make([]string, connectors)
	for i := range devices {
		devices[i] = createConnector(t, cl, ns, fmt.Sprintf("device-%d", i)).Name
	}
	ads := make([]*networkingv1alpha1.ConnectorAdvertisement, advertisements)
	for i := range ads {
		ads[i] = createAdvertisement(t, cl, ns, fmt.Sprintf("tunnel-%d", i), devices[0])
		require.NoError(t, reconcileAdvertisement(ctx, r, ads[i]))
	}
	stored := recordStoredVersions(t, cl, ns)

	var refused atomic.Int64
	var wg sync.WaitGroup
	for i, ad := range ads {
		plan := newAppPlan(rand.New(rand.NewPCG(1, uint64(i))), devices, steps)
		wg.Go(func() { actAsApp(ctx, t, cl, ad, plan) })
		wg.Go(func() { // the reconciler, enqueued by the app's writes
			for _, pause := range plan.reconcilerPauses {
				if !settle(ctx, t, r, ad, &refused) {
					return
				}
				time.Sleep(pause)
			}
		})
	}
	wg.Wait()
	for _, ad := range ads { // the app's last write enqueues each once more
		settle(ctx, t, r, ad, &refused)
	}

	versions := stored(requireSettled(t, cl, ads))
	for _, version := range versions {
		require.Equal(t, 1, connectorControllers(version), "stored version %s of %s", version.ResourceVersion, version.Name)
	}
	t.Logf("%d advertisements, %d writes by the app each; %d stored versions checked; %d writes refused and retried",
		advertisements, steps, len(versions), refused.Load())
}

// appPlan is what the app does to one advertisement in the stress test: the
// connector each step re-points it to, and the pauses after the app's and the
// reconciler's steps. It is drawn up front, so no goroutine shares a random
// source.
type appPlan struct {
	devices                     []string
	appPauses, reconcilerPauses []time.Duration
}

func newAppPlan(random *rand.Rand, devices []string, steps int) appPlan {
	plan := appPlan{make([]string, steps), make([]time.Duration, steps), make([]time.Duration, steps)}
	for step := range steps {
		plan.devices[step] = devices[random.IntN(len(devices))]
		plan.appPauses[step] = time.Duration(random.IntN(3)) * time.Millisecond
		plan.reconcilerPauses[step] = time.Duration(random.IntN(3)) * time.Millisecond
	}
	return plan
}

// actAsApp writes to ad as the desktop app does: a re-point, then an edit to
// layer4, in turn. It runs off the test goroutine, so it asserts.
func actAsApp(ctx context.Context, t *testing.T, cl client.Client, ad *networkingv1alpha1.ConnectorAdvertisement, plan appPlan) {
	for step, device := range plan.devices {
		body := repointBody(device)
		if step%2 == 1 {
			body = layer4Body(step)
		}
		if !assert.NoError(t, cl.Patch(ctx, ad.DeepCopy(), client.RawPatch(types.MergePatchType, body))) {
			return
		}
		time.Sleep(plan.appPauses[step])
	}
}

// settle reconciles ad until a reconcile succeeds, as the work queue retries
// it, and counts the refused writes. A refusal must be a 422, which a failed
// test of the spec or of the owner list gives.
func settle(ctx context.Context, t *testing.T, r *ConnectorAdvertisementReconciler, ad *networkingv1alpha1.ConnectorAdvertisement, refused *atomic.Int64) bool {
	for range 20 {
		err := reconcileAdvertisement(ctx, r, ad)
		if err == nil {
			return true
		}
		if !assert.True(t, apierrors.IsInvalid(err), "a refused write must be a 422: %v", err) {
			return false
		}
		refused.Add(1)
	}
	return assert.Fail(t, "never settled", ad.Name)
}

// requireSettled requires each of ads to be controlled by the connector its
// spec names, by no other, and Accepted for its generation. It returns them
// as stored.
func requireSettled(t *testing.T, cl client.Client, ads []*networkingv1alpha1.ConnectorAdvertisement) []*networkingv1alpha1.ConnectorAdvertisement {
	t.Helper()
	final := make([]*networkingv1alpha1.ConnectorAdvertisement, len(ads))
	for i, ad := range ads {
		final[i] = getAdvertisement(t, cl, ad)
		var named networkingv1alpha1.Connector
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: ad.Namespace, Name: final[i].Spec.ConnectorRef.Name}, &named))
		require.True(t, metav1.IsControlledBy(final[i], &named), "%s must be controlled by %s", ad.Name, named.Name)
		require.Len(t, final[i].GetOwnerReferences(), 1, "%s must reference no other connector", ad.Name)
		requireAcceptedForCurrentGeneration(t, final[i])
	}
	return final
}

// recordStoredVersions watches the advertisements in ns. It returns a function
// that waits until the watch has seen each of final at its resourceVersion,
// then stops the watch and returns every version it saw.
func recordStoredVersions(t *testing.T, cl client.WithWatch, ns string) func(final []*networkingv1alpha1.ConnectorAdvertisement) []networkingv1alpha1.ConnectorAdvertisement {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var list networkingv1alpha1.ConnectorAdvertisementList
	require.NoError(t, cl.List(ctx, &list, client.InNamespace(ns)))
	// The API server closes a watch that falls behind, as this one does under
	// -race; the retry watcher resumes it from the last version it delivered.
	w, err := watchtools.NewRetryWatcherWithContext(ctx, list.ResourceVersion, &cache.ListWatch{
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return cl.Watch(ctx, &networkingv1alpha1.ConnectorAdvertisementList{}, client.InNamespace(ns), &client.ListOptions{Raw: &options})
		},
	})
	require.NoError(t, err)
	var (
		mu       sync.Mutex
		versions []networkingv1alpha1.ConnectorAdvertisement
		seen     = map[string]bool{}
		failures []string
	)
	go func() {
		for event := range w.ResultChan() {
			mu.Lock()
			switch ad, ok := event.Object.(*networkingv1alpha1.ConnectorAdvertisement); {
			case event.Type == watch.Bookmark: // carries a resourceVersion and nothing else
			case !ok || event.Type == watch.Error:
				failures = append(failures, fmt.Sprintf("%s: %v", event.Type, event.Object))
			default:
				versions = append(versions, *ad)
				seen[ad.Name+"@"+ad.ResourceVersion] = true
			}
			mu.Unlock()
		}
	}()
	return func(final []*networkingv1alpha1.ConnectorAdvertisement) []networkingv1alpha1.ConnectorAdvertisement {
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			for _, ad := range final {
				if !seen[ad.Name+"@"+ad.ResourceVersion] {
					return false
				}
			}
			return true
		}, 10*time.Second, 10*time.Millisecond, "the watch must see every final version")
		w.Stop()
		<-w.Done()
		require.Empty(t, failures, "the watch must deliver every version, or the check is blind")
		return versions
	}
}

// connectorControllers counts ad's controller references to a Connector.
func connectorControllers(ad networkingv1alpha1.ConnectorAdvertisement) int {
	count := 0
	for _, ref := range ad.GetOwnerReferences() {
		if ref.Kind == "Connector" && ptr.Deref(ref.Controller, false) {
			count++
		}
	}
	return count
}

// startRepointEnv starts an API server with NSO's CRDs and returns a client
// for it.
func startRepointEnv(t *testing.T) client.WithWatch {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))
	cl, err := client.NewWithWatch(cfg, client.Options{Scheme: testScheme})
	require.NoError(t, err)
	return cl
}

func TestAdvertisementHandOverPatch(t *testing.T) {
	owner := metav1.OwnerReference{APIVersion: "networking.datumapis.com/v1alpha1", Kind: "Connector", Name: "desktop", UID: "b", Controller: ptr.To(true)}
	previous := metav1.OwnerReference{APIVersion: "networking.datumapis.com/v1alpha1", Kind: "Connector", Name: "laptop", UID: "a", Controller: ptr.To(true)}

	t.Run("an absent list is tested as null and added", func(t *testing.T) {
		patch, err := advertisementHandOverPatch("desktop", nil, []metav1.OwnerReference{owner})
		require.NoError(t, err)
		require.JSONEq(t, `[
			{"op":"test","path":"/spec/connectorRef/name","value":"desktop"},
			{"op":"test","path":"/metadata/ownerReferences","value":null},
			{"op":"add","path":"/metadata/ownerReferences","value":[{"apiVersion":"networking.datumapis.com/v1alpha1","kind":"Connector","name":"desktop","uid":"b","controller":true}]}
		]`, string(patch))
	})

	t.Run("a present list is tested as read and set", func(t *testing.T) {
		patch, err := advertisementHandOverPatch("desktop", []metav1.OwnerReference{previous}, []metav1.OwnerReference{owner})
		require.NoError(t, err)
		require.JSONEq(t, `[
			{"op":"test","path":"/spec/connectorRef/name","value":"desktop"},
			{"op":"test","path":"/metadata/ownerReferences","value":[{"apiVersion":"networking.datumapis.com/v1alpha1","kind":"Connector","name":"laptop","uid":"a","controller":true}]},
			{"op":"add","path":"/metadata/ownerReferences","value":[{"apiVersion":"networking.datumapis.com/v1alpha1","kind":"Connector","name":"desktop","uid":"b","controller":true}]}
		]`, string(patch))
	})
}

// collectedWhenGone reports whether the Kubernetes garbage collector deletes
// ad once the owner with goneUID is gone. The collector deletes a dependent
// when none of its owner references names an owner that still exists, and
// otherwise only drops the dangling references (attemptToDeleteItem in
// k8s.io/kubernetes/pkg/controller/garbagecollector, which Milo runs as a
// copy of its own). Only goneUID is gone here, so any other owner reference
// keeps ad.
func collectedWhenGone(ad *networkingv1alpha1.ConnectorAdvertisement, goneUID types.UID) bool {
	refs := ad.GetOwnerReferences()
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if ref.UID != goneUID {
			return false
		}
	}
	return true
}

func referencesUID(obj metav1.Object, uid types.UID) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

func requireAcceptedForCurrentGeneration(t *testing.T, ad *networkingv1alpha1.ConnectorAdvertisement) {
	t.Helper()
	cond := apimeta.FindStatusCondition(ad.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
	require.NotNil(t, cond)
	require.Equal(t, metav1.ConditionTrue, cond.Status)
	require.Equal(t, networkingv1alpha1.ConnectorAdvertisementReasonAccepted, cond.Reason)
	require.Equal(t, ad.Generation, cond.ObservedGeneration, "Accepted must describe the spec that names the current connector")
}

var namespaceSeq int

func newNamespace(t *testing.T, cl client.Client) string {
	t.Helper()
	namespaceSeq++
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("repoint-%d", namespaceSeq)}}
	require.NoError(t, cl.Create(context.Background(), ns))
	return ns.Name
}

func createConnector(t *testing.T, cl client.Client, ns, name string) *networkingv1alpha1.Connector {
	t.Helper()
	c := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       networkingv1alpha1.ConnectorSpec{ConnectorClassName: "datum-connect"},
	}
	require.NoError(t, cl.Create(context.Background(), c))
	return c
}

// holdInDeletion deletes connector and keeps it in deletion with a finalizer
// of the test's own, which the test removes when it ends.
func holdInDeletion(t *testing.T, cl client.Client, connector *networkingv1alpha1.Connector) {
	t.Helper()
	const hold = "test.networking.datumapis.com/hold"
	ctx := context.Background()
	controllerutil.AddFinalizer(connector, hold)
	require.NoError(t, cl.Update(ctx, connector))
	require.NoError(t, cl.Delete(ctx, connector))
	t.Cleanup(func() {
		live := &networkingv1alpha1.Connector{}
		if cl.Get(ctx, client.ObjectKeyFromObject(connector), live) == nil {
			controllerutil.RemoveFinalizer(live, hold)
			_ = cl.Update(ctx, live)
		}
	})
}

func createAdvertisement(t *testing.T, cl client.Client, ns, name, connector string) *networkingv1alpha1.ConnectorAdvertisement {
	t.Helper()
	ad := &networkingv1alpha1.ConnectorAdvertisement{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       networkingv1alpha1.ConnectorAdvertisementSpec{ConnectorRef: networkingv1alpha1.LocalConnectorReference{Name: connector}},
	}
	require.NoError(t, cl.Create(context.Background(), ad))
	return ad
}

func getAdvertisement(t *testing.T, cl client.Client, ad *networkingv1alpha1.ConnectorAdvertisement) *networkingv1alpha1.ConnectorAdvertisement {
	t.Helper()
	var got networkingv1alpha1.ConnectorAdvertisement
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(ad), &got))
	return &got
}

// repoint changes the advertisement's connectorRef with a JSON merge patch
// that carries only the spec, as the desktop app sends it.
func repoint(t *testing.T, cl client.Client, ad *networkingv1alpha1.ConnectorAdvertisement, connector string) {
	t.Helper()
	require.NoError(t, cl.Patch(context.Background(), ad.DeepCopy(), client.RawPatch(types.MergePatchType, repointBody(connector))))
}

func repointBody(connector string) []byte {
	return fmt.Appendf(nil, `{"spec":{"connectorRef":{"name":%q}}}`, connector)
}

// editLayer4 changes the advertisement's layer4 services with a JSON merge
// patch that carries only the spec, as the desktop app sends it. Each edit
// sets another port, so each changes the object.
func editLayer4(t *testing.T, cl client.Client, ad *networkingv1alpha1.ConnectorAdvertisement, edit int) {
	t.Helper()
	require.NoError(t, cl.Patch(context.Background(), ad.DeepCopy(), client.RawPatch(types.MergePatchType, layer4Body(edit))))
}

func layer4Body(edit int) []byte {
	return fmt.Appendf(nil, `{"spec":{"layer4":[{"name":"ssh","services":[{"address":"127.0.0.1","ports":[{"name":"ssh","port":%d,"protocol":"TCP"}]}]}]}}`, 2200+edit)
}

// reconcilerWith returns a reconciler that reads and writes through cl.
func reconcilerWith(cl client.Client) *ConnectorAdvertisementReconciler {
	return &ConnectorAdvertisementReconciler{mgr: &fakeMockManager{cl: cl}}
}

// racedBy returns a reconciler that reads and writes through cl, and calls
// other before each of its writes: another writer that acts between the
// reconcile's read and its write. other gets cl itself, unraced.
func racedBy(cl client.WithWatch, other func(c client.Client)) *ConnectorAdvertisementReconciler {
	return reconcilerWith(interceptor.NewClient(cl, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			other(c)
			return c.Patch(ctx, obj, patch, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			other(c)
			return c.Update(ctx, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			other(c)
			return c.SubResource(sub).Update(ctx, obj, opts...)
		},
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			other(c)
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	}))
}

func reconcileAdvertisement(ctx context.Context, r *ConnectorAdvertisementReconciler, ad *networkingv1alpha1.ConnectorAdvertisement) error {
	return reconcileAdvertisementWith(ctx, r.Reconcile, ad)
}

func reconcileAdvertisementWith(ctx context.Context, reconcileFn func(context.Context, mcreconcile.Request) (reconcile.Result, error), ad *networkingv1alpha1.ConnectorAdvertisement) error {
	_, err := reconcileFn(ctx, mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(ad)},
		ClusterName: "single",
	})
	return err
}

// legacyReconcile reproduces the v0.32.5 ownership step: claim the
// advertisement with SetControllerReference and fail when another controller
// holds it. It sets up the stuck state independently of the code under test.
func legacyReconcile(cl client.Client) func(context.Context, mcreconcile.Request) (reconcile.Result, error) {
	return func(ctx context.Context, req mcreconcile.Request) (reconcile.Result, error) {
		var ad networkingv1alpha1.ConnectorAdvertisement
		if err := cl.Get(ctx, req.NamespacedName, &ad); err != nil {
			return reconcile.Result{}, err
		}
		var connector networkingv1alpha1.Connector
		if err := cl.Get(ctx, client.ObjectKey{Namespace: ad.Namespace, Name: ad.Spec.ConnectorRef.Name}, &connector); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, controllerutil.SetControllerReference(&connector, &ad, cl.Scheme())
	}
}
