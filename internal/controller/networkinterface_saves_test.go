// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// laggingReads answers a read of a pinned interface with the version pinned, as
// a cache does until its watch catches up, and a nil pin with NotFound, as a
// cache that has not seen the interface yet; every other call goes to the API
// server.
type laggingReads struct {
	client.Client
	pinned map[client.ObjectKey]*networkingv1alpha.NetworkInterface
}

func (c *laggingReads) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if iface, ok := obj.(*networkingv1alpha.NetworkInterface); ok {
		if pinned, ok := c.pinned[key]; ok {
			if pinned == nil {
				return apierrors.NewNotFound(networkingv1alpha.GroupVersion.WithResource("networkinterfaces").GroupResource(), key.Name)
			}
			*iface = *pinned.DeepCopy()
			return nil
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// interveningReads lets another writer change an interface between a
// controller's read of it and its save.
type interveningReads struct {
	client.Reader
	intervene func()
}

func (r *interveningReads) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if _, ok := obj.(*networkingv1alpha.NetworkInterface); ok && r.intervene != nil {
		r.intervene()
		r.intervene = nil
	}
	return nil
}

// moveExternalAddress changes what the cell says about an interface: its spec
// and, with programmed set, its status.
func (v *visibility) moveExternalAddress(address string, programmed bool) {
	v.t.Helper()
	var iface networkingv1alpha.NetworkInterface
	key := client.ObjectKey{Namespace: v.cellNamespace, Name: boundInterfaceName}
	require.NoError(v.t, v.cell.Get(v.ctx, key, &iface))
	iface.Spec.ExternalAddresses[0].Address = address
	require.NoError(v.t, v.cell.Update(v.ctx, &iface))

	if programmed {
		meta.SetStatusCondition(&iface.Status.Conditions, metav1.Condition{
			Type: networkingv1alpha.NetworkInterfaceProgrammed, Status: metav1.ConditionTrue, Reason: "Programmed",
		})
	} else {
		meta.RemoveStatusCondition(&iface.Status.Conditions, networkingv1alpha.NetworkInterfaceProgrammed)
	}
	require.NoError(v.t, v.cell.Status().Update(v.ctx, &iface))
}

func requireCopyMatches(t *testing.T, copied *networkingv1alpha.NetworkInterface, address string, programmed bool) {
	t.Helper()
	require.Equal(t, address, copied.Spec.ExternalAddresses[0].Address)
	require.Equal(t, programmed,
		meta.IsStatusConditionTrue(copied.Status.Conditions, networkingv1alpha.NetworkInterfaceProgrammed))
}

func managersOf(obj metav1.Object, operation metav1.ManagedFieldsOperationType, subresource string) []string {
	var managers []string
	for _, entry := range obj.GetManagedFields() {
		if entry.Operation == operation && entry.Subresource == subresource {
			managers = append(managers, entry.Manager)
		}
	}
	return managers
}

func TestACopyFollowsItsSourceWhileTheCacheLags(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()
	v.publish()
	v.handToProject()

	first, found := v.projectCopy()
	require.True(t, found)
	v.projector.Projects = &staticProjectResolver{
		clients: map[string]client.Client{testProject: &laggingReads{
			Client: v.project,
			pinned: map[client.ObjectKey]*networkingv1alpha.NetworkInterface{client.ObjectKeyFromObject(first): first},
		}},
		readers: map[string]client.Reader{testProject: v.project},
	}

	for _, step := range []struct {
		address    string
		programmed bool
	}{
		{"198.51.100.29", true},
		{"198.51.100.11", false}, // back to what the cache still holds
		{"198.51.100.29", true},  // away from it again, while it holds an older version
	} {
		v.moveExternalAddress(step.address, step.programmed)
		v.publish()
		require.NoError(t, v.handToProjectErr(), "no save is made from the cache's older version")

		copied, found := v.projectCopy()
		require.True(t, found)
		requireCopyMatches(t, copied, step.address, step.programmed)
	}
}

func TestACopySaveDoesNotCarryTheVersionItWasReadAt(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()
	v.publish()
	v.handToProject()

	key := client.ObjectKey{Namespace: v.projectNamespace, Name: boundInterfaceName}
	anotherWriter := func(note string) func() {
		return func() {
			var copied networkingv1alpha.NetworkInterface
			require.NoError(t, v.project.Get(v.ctx, key, &copied))
			copied.Annotations = map[string]string{"example.com/note": note}
			require.NoError(t, v.project.Update(v.ctx, &copied))
		}
	}

	// The spec and the status change, then the status alone: a status saved
	// alone is saved from the version read, with no spec save to refresh it.
	for _, step := range []struct {
		address    string
		programmed bool
		note       string
	}{
		{"198.51.100.29", true, "first"},
		{"198.51.100.29", false, "second"},
	} {
		v.projector.Projects = &staticProjectResolver{
			clients: map[string]client.Client{testProject: v.project},
			readers: map[string]client.Reader{testProject: &interveningReads{
				Reader: v.project, intervene: anotherWriter(step.note),
			}},
		}
		v.moveExternalAddress(step.address, step.programmed)
		v.publish()
		require.NoError(t, v.handToProjectErr(), "a write between the read and the save does not reject the save")

		copied, found := v.projectCopy()
		require.True(t, found)
		requireCopyMatches(t, copied, step.address, step.programmed)
		require.Equal(t, step.note, copied.Annotations["example.com/note"], "another writer's field stays")
	}

	copied, found := v.projectCopy()
	require.True(t, found)
	require.Contains(t, managersOf(copied, metav1.ManagedFieldsOperationUpdate, ""),
		fieldOwner(networkInterfaceProjectorControllerName))
	require.Contains(t, managersOf(copied, metav1.ManagedFieldsOperationUpdate, "status"),
		fieldOwner(networkInterfaceProjectorControllerName))
}

func TestTheHoldOnAPublishedInterfaceIsAppliedByTheProjector(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()
	v.publish()

	require.NoError(t, v.reconcileHub())

	published, found := v.hubCopy(boundInterfaceName)
	require.True(t, found)
	require.Contains(t, published.Finalizers, networkInterfaceProjectionFinalizer)
	require.Contains(t, managersOf(published, metav1.ManagedFieldsOperationApply, ""),
		fieldOwner(networkInterfaceProjectorControllerName))
	require.Contains(t, managersOf(published, metav1.ManagedFieldsOperationUpdate, ""),
		fieldOwner(networkInterfaceWriteBackControllerName), "the write-back saves the hub copy under its own name")
	require.Contains(t, managersOf(published, metav1.ManagedFieldsOperationUpdate, "status"),
		fieldOwner(networkInterfaceWriteBackControllerName), "and its status")
}

func TestATeardownCollectsACopyTheCacheHasNotSeen(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()
	v.publish()
	require.NoError(t, v.reconcileHub())

	copied, found := v.projectCopy()
	require.True(t, found)
	v.projector.Projects = &staticProjectResolver{
		clients: map[string]client.Client{testProject: &laggingReads{
			Client: v.project,
			pinned: map[client.ObjectKey]*networkingv1alpha.NetworkInterface{client.ObjectKeyFromObject(copied): nil},
		}},
		readers: map[string]client.Reader{testProject: v.project},
	}

	held, found := v.hubCopy(boundInterfaceName)
	require.True(t, found)
	require.NoError(t, v.hub.Delete(v.ctx, held))
	require.NoError(t, v.reconcileHub())

	_, found = v.projectCopy()
	require.False(t, found, "the copy goes before the published interface is released")
}

func TestReleasingAPublishedInterfaceThatIsGoneIsDone(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()
	v.publish()
	require.NoError(t, v.reconcileHub())

	held, found := v.hubCopy(boundInterfaceName)
	require.True(t, found)
	require.NoError(t, v.hub.Delete(v.ctx, held))
	require.NoError(t, v.reconcileHub())
	_, found = v.hubCopy(boundInterfaceName)
	require.False(t, found)

	// A second reconcile queued before the first released the hold reads the
	// interface from a cache that still shows it held.
	stale := held.DeepCopy()
	stale.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	require.NoError(t, v.projector.teardown(v.ctx, stale))
}

func TestAHubCopyFollowsItsSourceWhileTheCacheLags(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()
	v.publish()

	first, found := v.hubCopy(boundInterfaceName)
	require.True(t, found)
	v.writeBack.HubCluster = &planeCluster{
		scheme: v.hub.Scheme(),
		client: &laggingReads{
			Client: v.hub,
			pinned: map[client.ObjectKey]*networkingv1alpha.NetworkInterface{client.ObjectKeyFromObject(first): first},
		},
		reader: v.hub,
	}

	for _, step := range []struct {
		address    string
		programmed bool
	}{
		{"198.51.100.29", true},
		{"198.51.100.11", false},
		{"198.51.100.29", true},
	} {
		v.moveExternalAddress(step.address, step.programmed)
		require.NoError(t, v.publishErr(), "no save is made from the cache's older version")

		copied, found := v.hubCopy(boundInterfaceName)
		require.True(t, found)
		requireCopyMatches(t, copied, step.address, step.programmed)
	}
}
