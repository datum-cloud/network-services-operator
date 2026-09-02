// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/locations/pkg/locationidentity"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// networkInterfaceSweepInterval paces the collection of copies whose original
// went while the cell could not see the hub. Nothing replays a deletion, so a
// copy that outlives its original has no event to remove it and a periodic
// comparison is the only thing that will.
const networkInterfaceSweepInterval = 10 * time.Minute

// NetworkInterfaceWriteBackReconciler publishes a cell's interfaces to the
// federation hub, which is the only plane both a cell and the control planes
// serving projects can reach. It follows the path an Instance already takes out
// of a cell: the cell writes a copy to the hub, and a controller on the hub
// hands it to the project that owns it.
//
// The copy is never read back. The cell stays the only writer of an interface,
// and nothing here participates in allocation or in the interface's lifecycle.
type NetworkInterfaceWriteBackReconciler struct {
	Location config.LocationConfig

	// HubCluster is the federation hub the copies are written to.
	HubCluster cluster.Cluster

	mgr         mcmanager.Manager
	localReader client.Reader
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=locations.miloapis.com,resources=servinglocations,verbs=get;list;watch

func (r *NetworkInterfaceWriteBackReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.publish(ctx, cl.GetClient(), req.NamespacedName)
}

func (r *NetworkInterfaceWriteBackReconciler) publish(
	ctx context.Context,
	cl client.Client,
	key client.ObjectKey,
) error {
	hub := r.HubCluster.GetClient()

	var iface networkingv1alpha.NetworkInterface
	if err := cl.Get(ctx, key, &iface); err != nil {
		if apierrors.IsNotFound(err) {
			return collectProjection(ctx, hub, key)
		}
		return err
	}

	// A copy of an interface on its way out is already wrong. Removing it now
	// rather than when the original finishes releasing keeps a consumer from
	// reading an address the cell has started handing back.
	if !iface.DeletionTimestamp.IsZero() {
		return collectProjection(ctx, hub, key)
	}

	// A copy exists to be handed to a project. An interface whose namespace
	// names none has nowhere to go, and publishing it to the hub would leave an
	// object nothing collects.
	routing, err := resolveProjectRouting(ctx, cl, iface.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			log.FromContext(ctx).Info("not publishing an interface whose namespace names no project",
				"namespace", iface.Namespace, jsonKeyName, iface.Name)
			return nil
		}
		return err
	}

	location, err := r.location(ctx)
	if err != nil {
		return err
	}

	desired := projectedInterface(&iface, location)
	routingLabels := map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: routing.clusterNameLabel,
		downstreamclient.UpstreamOwnerNamespaceLabel:   routing.projectNamespace,
	}

	if err := writeProjection(ctx, hub, iface.Namespace, desired, routingLabels, nil); err != nil {
		// The hub namespace is made by the federation that carries a project's
		// work to this cell. Nothing here should invent one.
		if apierrors.IsNotFound(err) {
			log.FromContext(ctx).Info("the hub has no namespace to publish this interface into yet",
				"namespace", iface.Namespace)
			return nil
		}
		return err
	}

	return nil
}

// sweep compares every copy this location published against the interfaces the
// cell still holds, and collects the ones with nothing behind them. It is what
// closes the gap a missed deletion opens: the cell being unreachable when an
// interface goes, or the process restarting across the event.
func (r *NetworkInterfaceWriteBackReconciler) sweep(ctx context.Context) error {
	logger := log.FromContext(ctx)

	location, err := r.location(ctx)
	if err != nil {
		return err
	}
	if location == "" {
		return nil
	}

	var published networkingv1alpha.NetworkInterfaceList
	if err := r.HubCluster.GetClient().List(ctx, &published, client.MatchingLabels{
		networkingv1alpha.NetworkInterfaceProjectionLabel: "true",
		networkingv1alpha.NetworkInterfaceLocationLabel:   location,
	}); err != nil {
		return fmt.Errorf("failed listing published interfaces: %w", err)
	}

	var errs []error
	for i := range published.Items {
		copied := &published.Items[i]
		key := client.ObjectKey{Namespace: copied.Namespace, Name: copied.Name}

		var iface networkingv1alpha.NetworkInterface
		err := r.localReader.Get(ctx, key, &iface)
		if err == nil && iface.DeletionTimestamp.IsZero() {
			continue
		}
		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, err)
			continue
		}

		logger.Info("collecting a published interface with no interface behind it",
			"namespace", key.Namespace, jsonKeyName, key.Name)
		if err := collectProjection(ctx, r.HubCluster.GetClient(), key); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (r *NetworkInterfaceWriteBackReconciler) location(ctx context.Context) (string, error) {
	identity, err := resolveLocationIdentity(ctx, r.localReader, r.Location)
	if err != nil {
		var unresolved *locationidentity.LocationUnresolved
		if errors.As(err, &unresolved) {
			return "", nil
		}
		return "", err
	}
	return identity.Reference.Name, nil
}

// Start runs the sweep on a timer for as long as the manager runs.
func (r *NetworkInterfaceWriteBackReconciler) Start(ctx context.Context) error {
	ticker := time.NewTicker(networkInterfaceSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.sweep(ctx); err != nil {
				log.FromContext(ctx).Error(err, "failed sweeping published interfaces")
			}
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkInterfaceWriteBackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.HubCluster == nil {
		return errors.New("a federation hub cluster is required")
	}

	r.mgr = mgr
	r.localReader = mgr.GetLocalManager().GetClient()

	if err := mgr.GetLocalManager().Add(r); err != nil {
		return fmt.Errorf("unable to add the interface sweep: %w", err)
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkinterface_writeback").
		Complete(r)
}
