// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	myOwner = "test/mine"
	// legacyOwner is the field manager that records the operator's Update
	// saves.
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

func create(t *testing.T, c client.Client) *appsv1.Deployment {
	t.Helper()
	labels := map[string]string{"app": "status"}
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "status-", Namespace: metav1.NamespaceDefault},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example.com/app"}}},
			},
		},
	}
	require.NoError(t, c.Create(context.Background(), d, client.FieldOwner(legacyOwner)))
	return d
}

func read(t *testing.T, c client.Client, d *appsv1.Deployment) *appsv1.Deployment {
	t.Helper()
	live := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(d), live))
	return live
}

// labelByAnotherWriter changes the object outside its status the way the
// operator's other saves do: an Update that carries the resourceVersion it read.
func labelByAnotherWriter(t *testing.T, c client.Client, d *appsv1.Deployment) {
	t.Helper()
	live := read(t, c, d)
	live.Labels = map[string]string{"other": "writer"}
	require.NoError(t, c.Update(context.Background(), live, client.FieldOwner(legacyOwner)))
}

func TestPatch(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()

	t.Run("a stale read still saves when another writer changed something else", func(t *testing.T) {
		d := create(t, c)
		stale := read(t, c, d)
		labelByAnotherWriter(t, c, d)

		desired := stale.DeepCopy()
		desired.Status.ObservedGeneration = 7
		require.NoError(t, Patch(ctx, c, desired, stale, myOwner))

		live := read(t, c, d)
		require.Equal(t, int64(7), live.Status.ObservedGeneration)
		require.Equal(t, "writer", live.Labels["other"])
		require.Equal(t, live.ResourceVersion, desired.ResourceVersion, "obj is updated from the response")
	})

	t.Run("only the status is saved when obj and stored differ elsewhere", func(t *testing.T) {
		d := create(t, c)
		older := read(t, c, d)
		labelByAnotherWriter(t, c, d)
		fresh := read(t, c, d)

		desired := older.DeepCopy()
		desired.Status.ObservedGeneration = 3
		require.NoError(t, Patch(ctx, c, desired, fresh, myOwner))

		live := read(t, c, d)
		require.Equal(t, int64(3), live.Status.ObservedGeneration)
		require.Equal(t, "writer", live.Labels["other"])
	})

	t.Run("a nil stored saves the whole status, and a field the next status leaves out is removed", func(t *testing.T) {
		d := create(t, c)
		desired := read(t, c, d)
		desired.Status.Replicas, desired.Status.ObservedGeneration = 2, 1
		require.NoError(t, Patch(ctx, c, desired, nil, myOwner))
		require.Equal(t, int32(2), read(t, c, d).Status.Replicas)

		stored := read(t, c, d)
		desired = stored.DeepCopy()
		desired.Status.Replicas = 0
		require.NoError(t, Patch(ctx, c, desired, stored, myOwner))

		live := read(t, c, d)
		require.Zero(t, live.Status.Replicas)
		require.Equal(t, int64(1), live.Status.ObservedGeneration)
	})

	t.Run("an unstructured object without a status removes the stored one", func(t *testing.T) {
		d := create(t, c)
		desired := read(t, c, d)
		desired.Status.ObservedGeneration = 5
		require.NoError(t, Patch(ctx, c, desired, nil, myOwner))

		stored := read(t, c, d)
		content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(stored)
		require.NoError(t, err)
		cleared := &unstructured.Unstructured{Object: content}
		cleared.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
		delete(cleared.Object, statusKey)
		require.NoError(t, Patch(ctx, c, cleared, stored, myOwner))

		require.Zero(t, read(t, c, d).Status.ObservedGeneration)
	})

	t.Run("the status is recorded under the field owner", func(t *testing.T) {
		d := create(t, c)
		stored := read(t, c, d)
		desired := stored.DeepCopy()
		desired.Status.ObservedGeneration = 9
		require.NoError(t, Patch(ctx, c, desired, stored, myOwner))

		var managers []string
		for _, entry := range read(t, c, d).ManagedFields {
			if entry.Subresource == statusKey && entry.Operation == metav1.ManagedFieldsOperationUpdate {
				managers = append(managers, entry.Manager)
			}
		}
		require.Contains(t, managers, myOwner)
	})
}
