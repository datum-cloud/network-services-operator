// SPDX-License-Identifier: AGPL-3.0-only

package finalizer

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	mine       = "example.com/mine"
	theirs     = "example.com/theirs"
	myOwner    = "test/mine"
	theirOwner = "test/theirs"
	// legacyOwner is the field manager that records the operator's Update
	// saves before this package.
	legacyOwner = "network-services"
)

func startAPIServer(t *testing.T) client.Client {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	env := &envtest.Environment{}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
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
		require.NoError(t, Remove(ctx, c, staleRemoved, mine, myOwner))

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
			wg.Go(func() { require.NoError(t, Add(ctx, c, stale, name, owner)) })
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
		require.NoError(t, Remove(ctx, c, cm, mine, myOwner))
		require.Equal(t, cm.ResourceVersion, read(t, c, cm).ResourceVersion)
	})

	t.Run("an entry the legacy manager recorded is removed", func(t *testing.T) {
		cm := create(t, c, theirs)
		update(t, c, cm, func(o *corev1.ConfigMap) { o.Finalizers = append(o.Finalizers, mine) })
		require.NoError(t, Remove(ctx, c, read(t, c, cm), mine, myOwner))
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("a remove that loses a race removes nothing and succeeds after a fresh read", func(t *testing.T) {
		cm := create(t, c, theirs, mine)
		stale := cm.DeepCopy()
		update(t, c, cm, func(o *corev1.ConfigMap) { o.Finalizers = []string{mine} })

		require.True(t, apierrors.IsInvalid(Remove(ctx, c, stale, mine, myOwner)))
		require.Equal(t, []string{mine}, read(t, c, cm).Finalizers)
		require.NoError(t, Remove(ctx, c, read(t, c, cm), mine, myOwner))
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
		require.NoError(t, Remove(ctx, c, cm, mine, myOwner), "an object already gone holds no finalizer")
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
		require.NoError(t, Remove(ctx, c, cm, mine, myOwner))
		require.Equal(t, []string{theirs, mine}, read(t, c, cm).Finalizers)
		require.NoError(t, Remove(ctx, c, cm, mine, myOwner))
		require.Equal(t, []string{theirs}, read(t, c, cm).Finalizers)
	})

	t.Run("removing the last finalizer releases a deleting object", func(t *testing.T) {
		cm := create(t, c, mine)
		require.NoError(t, c.Delete(ctx, cm))
		require.NoError(t, Remove(ctx, c, read(t, c, cm), mine, myOwner))
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})))
	})
}
