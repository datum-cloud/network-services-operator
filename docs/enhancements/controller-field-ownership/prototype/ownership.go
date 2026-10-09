//go:build prototype

// SPDX-License-Identifier: AGPL-3.0-only

// Package prototype tests, against a kube-apiserver (envtest), the saves that
// ../README.md proposes. Each save reuses a pattern from another project; the
// only code of our own is the Patcher below, around the finalizer patches
// copied from external-attacher. The build tag keeps the package out of
// `make test`.
package prototype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// FieldManager names the field manager of one controller.
func FieldManager(controller string) string { return "network-services/" + controller }

// maxPatchAttempts bounds the patches sent when other writers keep changing the
// list between our read and our patch.
const maxPatchAttempts = 5

// Patcher saves one controller's list entries on one cluster.
type Patcher struct {
	// Client sends the patches. Reader reads the object again after a failed
	// test; it must bypass the cache.
	Client client.Client
	Reader client.Reader
	// Manager is the controller's field manager.
	Manager string
	// TestFailures counts patches the API server refused with 422 because
	// another writer changed the list first. Each one is read again and sent
	// again at once, without backoff.
	TestFailures *atomic.Int64
}

// AddFinalizer appends fin with external-attacher's JSON patch. It writes
// nothing when fin is present or the object is terminating, and never creates
// an object.
func (p Patcher) AddFinalizer(ctx context.Context, obj client.Object, fin string) (bool, error) {
	return p.patch(ctx, obj, func(o client.Object) ([]byte, error) {
		if !o.GetDeletionTimestamp().IsZero() || controllerutil.ContainsFinalizer(o, fin) {
			return nil, nil
		}
		return addFinalizerPatch(o.GetFinalizers(), fin)
	})
}

// RemoveFinalizer removes every copy of fin, one tested patch per copy. A
// deleted object or an absent finalizer is not an error.
func (p Patcher) RemoveFinalizer(ctx context.Context, obj client.Object, fin string) error {
	for controllerutil.ContainsFinalizer(obj, fin) {
		wrote, err := p.patch(ctx, obj, func(o client.Object) ([]byte, error) {
			if !controllerutil.ContainsFinalizer(o, fin) {
				return nil, nil
			}
			return removeFinalizerPatch(o.GetFinalizers(), fin)
		})
		if err != nil || !wrote {
			return err
		}
	}
	return nil
}

// RemoveListeners removes, by tested patches, every Gateway listener whose name
// keep does not hold. It runs before the controller's apply: a listener that a
// legacy manager recorded is not removed by leaving it out of the apply, and a
// stale one can make the applied spec fail validation.
func (p Patcher) RemoveListeners(ctx context.Context, gw *gatewayv1.Gateway, keep func(name string) bool) error {
	for {
		i := slices.IndexFunc(gw.Spec.Listeners, func(l gatewayv1.Listener) bool { return !keep(string(l.Name)) })
		if i < 0 {
			return nil
		}
		name := string(gw.Spec.Listeners[i].Name)
		wrote, err := p.patch(ctx, gw, func(o client.Object) ([]byte, error) {
			g, ok := o.(*gatewayv1.Gateway)
			if !ok {
				return nil, fmt.Errorf("%T is not a Gateway", o)
			}
			j := slices.IndexFunc(g.Spec.Listeners, func(l gatewayv1.Listener) bool { return string(l.Name) == name })
			if j < 0 {
				return nil, nil
			}
			path := fmt.Sprintf("/spec/listeners/%d", j)
			return json.Marshal([]map[string]any{
				{"op": "test", "path": path + "/name", "value": name},
				{"op": "remove", "path": path},
			})
		})
		if err != nil || !wrote {
			return err
		}
	}
}

// patch sends the patch that build makes from obj, and updates obj from the
// response. build returns nil when no write is needed. A 422 counts as a failed
// test only when a fresh read shows the object changed; then it reads again and
// builds again. A 422 on an unchanged object is a validation rejection and is
// returned at once. A deleted object ends the save without an error. After
// maxPatchAttempts failed tests it returns an error, and the caller's reconcile
// requeues.
func (p Patcher) patch(
	ctx context.Context, obj client.Object, build func(client.Object) ([]byte, error),
) (bool, error) {
	for range maxPatchAttempts {
		body, err := build(obj)
		if err != nil || body == nil {
			return false, err
		}
		err = p.Client.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, body), client.FieldOwner(p.Manager))
		switch {
		case err == nil:
			return true, nil
		case apierrors.IsNotFound(err):
			return false, nil
		case !isUnprocessable(err):
			return false, err
		}
		before := obj.GetResourceVersion()
		if getErr := p.Reader.Get(ctx, client.ObjectKeyFromObject(obj), obj); getErr != nil {
			return false, client.IgnoreNotFound(getErr)
		}
		if obj.GetResourceVersion() == before {
			return false, err
		}
		if p.TestFailures != nil {
			p.TestFailures.Add(1)
		}
	}
	return false, fmt.Errorf("%s: other writers kept changing the list after %d attempts",
		client.ObjectKeyFromObject(obj), maxPatchAttempts)
}

func isUnprocessable(err error) bool {
	var st apierrors.APIStatus
	return errors.As(err, &st) && st.Status().Code == 422
}

// Gone reports whether a failed apply that carried obj's UID failed because
// the object is gone. The API server answers 409 ("uid mismatch") when the
// object was deleted, and 422 ("metadata.uid: field is immutable") when it was
// deleted and created again. Other failures give the same codes, so the object
// is read again, bypassing the cache, to tell them apart.
func Gone(ctx context.Context, reader client.Reader, obj client.Object, err error) bool {
	if err == nil {
		return false
	}
	live, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return false
	}
	switch getErr := reader.Get(ctx, client.ObjectKeyFromObject(obj), live); {
	case apierrors.IsNotFound(getErr):
		return true
	case getErr != nil:
		return false
	}
	return live.GetUID() != obj.GetUID()
}
