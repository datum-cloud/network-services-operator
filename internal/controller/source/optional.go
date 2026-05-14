// SPDX-License-Identifier: AGPL-3.0-only

// Package source provides controller-runtime [source.Source] wrappers tailored
// for multi-cluster reconcilers running against API servers with non-uniform
// discovery.
package source

import (
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crsource "sigs.k8s.io/controller-runtime/pkg/source"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"
)

// OptionalKind wraps [mcsource.Kind] so that a per-cluster engagement does not
// fail when the watched GVK is missing from that cluster's discovery. The
// underlying multicluster controller engages each source against each cluster
// at runtime; if any source returns an error (or a cache informer for a
// missing kind fails to start), the whole controller/cluster pair is rejected
// and never reconciles.
//
// In practice this happens against Datum project control planes whose API
// servers serve coordination.k8s.io/v1.Lease at the raw URL but omit the group
// from discovery. controller-runtime's REST mapper then returns
// [apimeta.NoKindMatchError], which aborts the engagement and leaves both the
// Lease watch and every other source on that cluster offline.
//
// OptionalKind defers to [mcsource.Kind] for clusters where the GVK is
// discoverable. When [apimeta.IsNoMatchError] is true at engagement time, it
// returns shouldEngage=false so the multicluster controller skips that source
// for that cluster and proceeds with the rest of the engagement. Reconcilers
// that previously relied on this watch must compensate with a periodic
// [ctrl.Result.RequeueAfter] so the object's state still converges.
func OptionalKind(
	obj client.Object,
	handler mchandler.EventHandlerFunc,
	predicates ...predicate.Predicate,
) mcsource.Source {
	return &optionalKind{
		obj:        obj,
		inner:      mcsource.Kind(obj, handler, predicates...),
		predicates: predicates,
	}
}

type optionalKind struct {
	obj        client.Object
	inner      mcsource.SyncingSource[client.Object]
	predicates []predicate.Predicate
}

// ForCluster probes the cluster's REST mapper for the wrapped GVK. If the
// mapper has no match, the source disengages from this cluster (engagement
// succeeds, no events) and logs once at V(1). Any other mapper error is
// propagated so the manager retries — only the no-match case is treated as
// "stay offline."
func (o *optionalKind) ForCluster(name string, cl cluster.Cluster) (crsource.TypedSource[mcreconcile.Request], bool, error) {
	gvk, err := gvkForObject(o.obj, cl)
	if err != nil {
		return nil, false, fmt.Errorf("determine GVK for optional kind: %w", err)
	}

	if _, err := cl.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
		if apimeta.IsNoMatchError(err) {
			log.Log.WithName("optional-kind").V(1).Info(
				"optional watch unavailable on cluster, degrading to no-op",
				"cluster", name,
				"gvk", gvk.String(),
			)
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("rest mapping for %s on cluster %q: %w", gvk, name, err)
	}

	return o.inner.ForCluster(name, cl)
}

// gvkForObject resolves the object's GVK from the cluster's scheme. The cluster
// scheme is preferred over the manager's so this works correctly for cluster
// adapters that augment the scheme on a per-cluster basis.
func gvkForObject(obj client.Object, cl cluster.Cluster) (schema.GroupVersionKind, error) {
	// Honor an explicit GVK if the caller already set one (e.g. via
	// PartialObjectMetadata). Otherwise resolve via the scheme.
	if gvk := obj.GetObjectKind().GroupVersionKind(); gvk.Kind != "" {
		return gvk, nil
	}
	gvks, _, err := cl.GetScheme().ObjectKinds(obj)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	if len(gvks) == 0 {
		return schema.GroupVersionKind{}, fmt.Errorf("no GVK registered for %T", obj)
	}
	return gvks[0], nil
}
