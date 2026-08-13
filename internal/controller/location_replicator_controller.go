// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// LocationReplicator maintains a hub copy of every location Milo's platform
// control plane reports as ready, so the systems that place and run work can
// name a location and read its topology without reading the platform.
//
// The copy is a projection rather than a mirror. Propagation to a location
// carries spec and labels and nothing else, so status is neither copied nor
// worth reconstructing: a location that is not ready is not copied at all, and
// one that stops being ready is removed.
type LocationReplicator struct {
	// PropagationClusterName is stamped onto every copy as the label the
	// federation control plane's policy selects NSO resources by. A Location
	// belongs to no project, so unlike every other propagated kind there is
	// nothing to derive this from.
	PropagationClusterName string

	platform client.Client
	hub      client.Client
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=locations,verbs=get;list;watch;create;update;patch;delete

func (r *LocationReplicator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var location networkingv1alpha.Location
	if err := r.platform.Get(ctx, client.ObjectKey{Name: req.Name}, &location); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.remove(ctx, req.Name)
		}
		return ctrl.Result{}, fmt.Errorf("failed reading location %q: %w", req.Name, err)
	}

	if !location.DeletionTimestamp.IsZero() ||
		!apimeta.IsStatusConditionTrue(location.Status.Conditions, networkingv1alpha.LocationReady) {
		return ctrl.Result{}, r.remove(ctx, req.Name)
	}

	return ctrl.Result{}, r.project(ctx, &location)
}

func (r *LocationReplicator) project(ctx context.Context, location *networkingv1alpha.Location) error {
	copied := &networkingv1alpha.Location{}
	copied.Name = location.Name

	_, err := controllerutil.CreateOrUpdate(ctx, r.hub, copied, func() error {
		if copied.Labels == nil {
			copied.Labels = map[string]string{}
		}
		copied.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] = r.PropagationClusterName

		copied.Spec.LocationClassName = location.Spec.LocationClassName
		copied.Spec.Topology = maps.Clone(location.Spec.Topology)
		copied.Spec.Provider = *location.Spec.Provider.DeepCopy()
		copied.Spec.Coordinates = location.Spec.Coordinates.DeepCopy()
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed writing location %q: %w", location.Name, err)
	}
	return nil
}

func (r *LocationReplicator) remove(ctx context.Context, name string) error {
	var copied networkingv1alpha.Location
	if err := r.hub.Get(ctx, client.ObjectKey{Name: name}, &copied); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !copied.DeletionTimestamp.IsZero() {
		return nil
	}

	log.FromContext(ctx).Info("location is no longer ready in the platform control plane, removing the copy")
	return client.IgnoreNotFound(r.hub.Delete(ctx, &copied))
}

// SetupWithManager registers the replicator against the hub, reading locations
// from the platform control plane.
//
// This must be a manager that runs one replica. The sharded managers run three
// with leader election disabled, so registering there writes every copy in all
// three.
func (r *LocationReplicator) SetupWithManager(mgr manager.Manager, platform cluster.Cluster) error {
	if r.PropagationClusterName == "" {
		return errors.New("a propagation cluster name is required")
	}
	r.hub = mgr.GetClient()
	r.platform = platform.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.Location{}).
		WatchesRawSource(source.Kind(
			platform.GetCache(),
			&networkingv1alpha.Location{},
			&handler.TypedEnqueueRequestForObject[*networkingv1alpha.Location]{},
		)).
		Named("locationreplicator").
		Complete(r)
}
