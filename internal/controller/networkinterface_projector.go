// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// NetworkInterfaceProjector hands a cell's published interfaces to the project
// that owns them, so a consumer can see the interface behind their instance in
// the control plane they already read everything else from.
//
// It runs where the Instance projection runs: on the hub, which is the only
// plane that both receives what cells publish and reaches project control
// planes. The copy it writes is a copy of a copy and is authoritative nowhere.
type NetworkInterfaceProjector struct {
	Projects ProjectClusterResolver

	hub client.Client
}

// networkInterfaceProjectionFinalizer holds a published interface on the hub
// until the copy it was handed to a project has been collected.
//
// Nothing replays a deletion. A published interface that simply vanished would
// take with it the labels naming the project its copy went to, leaving a copy
// that describes an interface that no longer exists and no event anywhere able
// to say where it is. Holding the original until its copy is gone means the one
// controller that knows both planes does the cleanup while it still can.
const networkInterfaceProjectionFinalizer = "networking.datumapis.com/networkinterface-projection"

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch

func (r *NetworkInterfaceProjector) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var published networkingv1alpha.NetworkInterface
	if err := r.hub.Get(ctx, req.NamespacedName, &published); err != nil {
		// A published interface that has finished going leaves nothing here to
		// route by. Its copy was collected while it was still held, under the
		// finalizer below.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !isProjection(&published) {
		return ctrl.Result{}, nil
	}

	if !published.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.teardown(ctx, &published)
	}

	if controllerutil.AddFinalizer(&published, networkInterfaceProjectionFinalizer) {
		if err := r.hub.Update(ctx, &published); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed holding the published interface for its copy: %w", err)
		}
	}

	// A name held by a copy that is being torn down is a state to come back to,
	// not a reconcile that failed.
	if err := r.project(ctx, &published); err != nil {
		var held *projectionSlotHeld
		if errors.As(err, &held) {
			log.FromContext(ctx).Info("waiting for the copy holding this name to finish being torn down",
				"copy", held.key.String())
			return ctrl.Result{RequeueAfter: projectionSlotRetry}, nil
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceProjector) project(
	ctx context.Context,
	published *networkingv1alpha.NetworkInterface,
) error {
	logger := log.FromContext(ctx)

	project := downstreamclient.UpstreamClusterNameFromLabel(
		published.Labels[downstreamclient.UpstreamOwnerClusterNameLabel])
	namespace := published.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]
	if project == "" || namespace == "" {
		return fmt.Errorf("published interface %s/%s names no project to hand it to",
			published.Namespace, published.Name)
	}

	projectClient, err := r.Projects.ClientForProject(ctx, project)
	if err != nil {
		// A project the manager no longer engages has no control plane to write
		// to, and nothing to see the copy if it did.
		if errors.Is(err, multicluster.ErrClusterNotFound) {
			logger.Info("not projecting an interface into a project that is no longer engaged",
				"project", project)
			return nil
		}
		return fmt.Errorf("failed reaching project %q: %w", project, err)
	}

	key := client.ObjectKey{Namespace: namespace, Name: published.Name}

	// An interface belongs to a network, and the network is the one object in the
	// project namespace whose life the interface's copy should follow. Owning the
	// copy from it means the apiserver collects the copy when the network or the
	// namespace goes, without anything cross-cluster having to notice.
	var network networkingv1alpha.Network
	networkKey := client.ObjectKey{Namespace: namespace, Name: published.Spec.Network.Name}
	if err := projectClient.Get(ctx, networkKey, &network); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("collecting an interface copy whose network is gone",
				"namespace", namespace, jsonKeyName, published.Name)
			return collectProjection(ctx, projectClient, key)
		}
		return fmt.Errorf("failed reading network %q: %w", networkKey, err)
	}

	desired := published.DeepCopy()
	desired.Labels[networkingv1alpha.NetworkInterfaceSourceNamespaceLabel] = published.Namespace

	owner := func(copied *networkingv1alpha.NetworkInterface) error {
		return controllerutil.SetOwnerReference(&network, copied, projectClient.Scheme())
	}

	return writeProjection(ctx, projectClient, namespace, desired, nil, owner)
}

// teardown collects the copy a published interface on its way out was handed to
// a project, then lets the original go.
func (r *NetworkInterfaceProjector) teardown(
	ctx context.Context,
	published *networkingv1alpha.NetworkInterface,
) error {
	if !controllerutil.ContainsFinalizer(published, networkInterfaceProjectionFinalizer) {
		return nil
	}

	project := downstreamclient.UpstreamClusterNameFromLabel(
		published.Labels[downstreamclient.UpstreamOwnerClusterNameLabel])
	namespace := published.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]

	// An interface naming no project was never handed to one, and a project the
	// manager no longer engages has no control plane left holding a copy. Neither
	// leaves anything to collect, and neither may hold the original open.
	if project != "" && namespace != "" {
		projectClient, err := r.Projects.ClientForProject(ctx, project)
		switch {
		case err == nil:
			key := client.ObjectKey{Namespace: namespace, Name: published.Name}
			if err := collectProjection(ctx, projectClient, key); err != nil {
				return err
			}
		case errors.Is(err, multicluster.ErrClusterNotFound):
			log.FromContext(ctx).Info("releasing a published interface whose project is no longer engaged",
				"project", project)
		default:
			return fmt.Errorf("failed reaching project %q: %w", project, err)
		}
	}

	if controllerutil.RemoveFinalizer(published, networkInterfaceProjectionFinalizer) {
		if err := r.hub.Update(ctx, published); err != nil {
			return fmt.Errorf("failed releasing the published interface: %w", err)
		}
	}

	return nil
}

// SetupWithManager registers the projector against the hub the manager runs in.
func (r *NetworkInterfaceProjector) SetupWithManager(mgr manager.Manager) error {
	r.hub = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}).
		Named("networkinterface_projector").
		Complete(r)
}
