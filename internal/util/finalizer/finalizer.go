// SPDX-License-Identifier: AGPL-3.0-only

// Package finalizer adds and removes one controller's finalizer without a
// resourceVersion, so a write to any other part of the object does not fail
// it. The add is cert-manager's apply of the finalizer with the object's UID
// (cert-manager/cert-manager#8519); the remove is external-attacher's JSON
// patch (kubernetes-csi/external-provisioner#1217), which works whoever owns
// the entry.
package finalizer

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Add makes name one of obj's finalizers by applying it under fieldOwner. The
// apply carries obj's UID, so it never creates an object that was deleted.
// fieldOwner must apply nothing else to obj: an apply that leaves the
// finalizer out removes it. obj is not updated.
func Add(ctx context.Context, c client.Client, obj client.Object, name, fieldOwner string) error {
	if controllerutil.ContainsFinalizer(obj, name) {
		return nil
	}
	gvk, err := c.GroupVersionKindFor(obj)
	if err != nil {
		return err
	}
	intent := &unstructured.Unstructured{}
	intent.SetGroupVersionKind(gvk)
	intent.SetNamespace(obj.GetNamespace())
	intent.SetName(obj.GetName())
	intent.SetUID(obj.GetUID())
	intent.SetFinalizers([]string{name})
	return c.Apply(ctx, client.ApplyConfigurationFromUnstructured(intent), client.FieldOwner(fieldOwner), client.ForceOwnership)
}

// Remove removes name from obj's finalizers if obj carries it, and updates obj
// from the response. The patch tests that the entry is still where obj has it;
// if another writer moved it, the API server rejects the patch and the caller
// reads again.
func Remove(ctx context.Context, c client.Client, obj client.Object, name, fieldOwner string) error {
	if !controllerutil.ContainsFinalizer(obj, name) {
		return nil
	}
	patch, err := removeFinalizerPatch(obj.GetFinalizers(), name)
	if err != nil {
		return err
	}
	return c.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, patch), client.FieldOwner(fieldOwner))
}
