// SPDX-License-Identifier: AGPL-3.0-only

// Package finalizer adds and removes one controller's finalizer without a
// resourceVersion, so a write to any other part of the object does not fail
// it. The add is cert-manager's apply of the finalizer with the object's UID
// (cert-manager/cert-manager#8519); the remove is external-attacher's JSON
// patch (kubernetes-csi/external-provisioner#1217), which works whoever owns
// the entry and leaves every other entry, the API server's own included.
package finalizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"go.miloapis.com/instrumentation/labels"
	"go.miloapis.com/instrumentation/metrics"
)

// Cluster is what Remove needs of the cluster that holds the object: the client
// that writes it and the reader that reads the API server rather than a cache.
// Taking both from one cluster keeps a caller from passing the cache as the
// reader. controller-runtime's cluster.Cluster satisfies it.
type Cluster interface {
	GetClient() client.Client
	GetAPIReader() client.Reader
}

// The outcome of a removal, counted in nso_finalizer_removals_total.
const (
	outcomePatch          = "patch"
	outcomeAlreadyRemoved = "already_removed"
	outcomeGone           = "gone"
	outcomeReplaced       = "replaced"
	// outcomeConflict is a patch the API server could not apply: another writer
	// moved the list or replaced the object after the read. A requeue reads again.
	outcomeConflict = "conflict"
	// outcomeInvalid is a change the API server refused on its merits, by a
	// validation rule or an admission policy. A requeue does not cure it.
	outcomeInvalid = "invalid"
	outcomeError   = "error"
)

var removals = metrics.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nso_finalizer_removals_total",
		Help: "Finalizer removals by field manager and outcome: patched, already removed, object gone, " +
			"object replaced under its name, conflict (the list or the object changed after the read), " +
			"invalid (refused by validation or admission), or another error.",
	},
	[]string{"field_manager", labels.Outcome},
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

// Remove removes name from obj's finalizers if obj carries it. It first reads
// the object's metadata through cl's API reader, as the garbage collector does
// before it removes a finalizer: an object already gone, an entry a removal
// before already took, and another object created again under obj's name all
// end it without a write. It then removes the entry with a JSON patch that
// tests the object's UID and the entry's value at its index in that read; if
// another writer changed either in between, the API server rejects the patch
// and the caller reads again. obj is updated from the response.
func Remove(ctx context.Context, cl Cluster, obj client.Object, name, fieldOwner string) error {
	if !controllerutil.ContainsFinalizer(obj, name) {
		return nil
	}
	key := client.ObjectKeyFromObject(obj)
	if obj.GetUID() == "" {
		// Without a UID the removal cannot tell obj from another object created under its name.
		removals.WithLabelValues(fieldOwner, outcomeError).Inc()
		return fmt.Errorf("remove finalizer %q from %s: the object carries no UID", name, key)
	}
	live, err := readMetadata(ctx, cl, obj)
	switch {
	case apierrors.IsNotFound(err):
		removals.WithLabelValues(fieldOwner, outcomeGone).Inc()
		return nil
	case err != nil:
		removals.WithLabelValues(fieldOwner, outcomeError).Inc()
		return fmt.Errorf("read %s before removing finalizer %q: %w", key, name, err)
	case live.GetUID() != obj.GetUID():
		removals.WithLabelValues(fieldOwner, outcomeReplaced).Inc()
		return nil
	case !controllerutil.ContainsFinalizer(live, name):
		removals.WithLabelValues(fieldOwner, outcomeAlreadyRemoved).Inc()
		return nil
	}
	patch, err := removeFinalizerPatch(live.GetFinalizers(), name)
	if err != nil {
		return err
	}
	if patch, err = withUIDTest(patch, live.GetUID()); err != nil {
		return err
	}
	err = cl.GetClient().Patch(ctx, obj, client.RawPatch(types.JSONPatchType, patch), client.FieldOwner(fieldOwner))
	outcome := outcomeOf(err)
	removals.WithLabelValues(fieldOwner, outcome).Inc()
	if outcome == outcomePatch || outcome == outcomeGone {
		return nil
	}
	return fmt.Errorf("remove finalizer %q at index %d from %s (UID %s), %s: %w",
		name, slices.Index(live.GetFinalizers(), name), key, live.GetUID(), outcome, err)
}

// readMetadata reads obj's metadata from the API server.
func readMetadata(ctx context.Context, cl Cluster, obj client.Object) (*metav1.PartialObjectMetadata, error) {
	gvk, err := cl.GetClient().GroupVersionKindFor(obj)
	if err != nil {
		return nil, err
	}
	live := &metav1.PartialObjectMetadata{}
	live.SetGroupVersionKind(gvk)
	return live, cl.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(obj), live)
}

// outcomeOf classifies the API server's answer to the removal's patch. A JSON
// patch that cannot be applied, a failed test or an index past the end, is
// answered 422 with no causes (k8s.io/apiserver, endpoints/handlers/patch.go);
// a validation rule or an admission policy answers 422 with at least one.
func outcomeOf(err error) string {
	var status apierrors.APIStatus
	switch {
	case err == nil:
		return outcomePatch
	case apierrors.IsNotFound(err):
		return outcomeGone
	case !apierrors.IsInvalid(err):
		return outcomeError
	case errors.As(err, &status) && status.Status().Details != nil && len(status.Status().Details.Causes) > 0:
		return outcomeInvalid
	default:
		return outcomeConflict
	}
}

// withUIDTest puts a test of the object's UID before patch's operations, so
// the patch cannot apply to another object created under the same name. It
// wraps removeFinalizerPatch's result so that the copied function stays
// identical to its source in external-attacher.
func withUIDTest(patch []byte, uid types.UID) ([]byte, error) {
	var ops []map[string]any
	if err := json.Unmarshal(patch, &ops); err != nil {
		return nil, err
	}
	test := map[string]any{"op": "test", "path": "/metadata/uid", "value": string(uid)}
	return json.Marshal(append([]map[string]any{test}, ops...))
}
