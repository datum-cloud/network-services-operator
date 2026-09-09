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

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch

func (r *NetworkInterfaceProjector) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var published networkingv1alpha.NetworkInterface
	if err := r.hub.Get(ctx, req.NamespacedName, &published); err != nil {
		// A published interface that has gone leaves nothing here to route by.
		// The project-plane collector is what removes what it left behind.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !isProjection(&published) || !published.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.project(ctx, &published)
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

// SetupWithManager registers the projector against the hub the manager runs in.
func (r *NetworkInterfaceProjector) SetupWithManager(mgr manager.Manager) error {
	r.hub = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}).
		Named("networkinterface_projector").
		Complete(r)
}
