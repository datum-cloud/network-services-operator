// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// NetworkInterfaceProjectionGCReconciler removes an interface copy from a
// project control plane once nothing is published behind it.
//
// It is the projector's other half, and it exists because the projector is
// keyed on the published copy: an object that has gone carries no labels, so
// nothing on the hub can say which project a vanished copy belonged to. Keying
// on the copy instead answers that from the copy itself, and it also brings a
// copy back that somebody deleted by hand.
type NetworkInterfaceProjectionGCReconciler struct {
	Projects ProjectClusterResolver

	hub client.Client
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch;delete

func (r *NetworkInterfaceProjectionGCReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	projectClient, err := r.Projects.ClientForProject(ctx, string(req.ClusterName))
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.collect(ctx, projectClient, req.NamespacedName)
}

func (r *NetworkInterfaceProjectionGCReconciler) collect(
	ctx context.Context,
	projectClient client.Client,
	key client.ObjectKey,
) error {
	var copied networkingv1alpha.NetworkInterface
	if err := projectClient.Get(ctx, key, &copied); err != nil {
		return client.IgnoreNotFound(err)
	}

	if !isProjection(&copied) || !copied.DeletionTimestamp.IsZero() {
		return nil
	}

	source := client.ObjectKey{
		Namespace: copied.Labels[networkingv1alpha.NetworkInterfaceSourceNamespaceLabel],
		Name:      copied.Name,
	}
	if source.Namespace == "" {
		return errors.New("an interface copy names no namespace it was published from")
	}

	var published networkingv1alpha.NetworkInterface
	err := r.hub.Get(ctx, source, &published)
	if err == nil && published.DeletionTimestamp.IsZero() {
		return nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	log.FromContext(ctx).Info("collecting an interface copy with nothing published behind it",
		"namespace", key.Namespace, jsonKeyName, key.Name)

	return client.IgnoreNotFound(projectClient.Delete(ctx, &copied))
}

// SetupWithManager registers the collector against the project control planes
// the multicluster manager engages, and reads the hub through the manager's own
// cluster.
func (r *NetworkInterfaceProjectionGCReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.Projects == nil {
		r.Projects = NewProjectClusterResolver(mgr)
	}
	r.hub = mgr.GetLocalManager().GetClient()

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkinterface_projection_gc").
		Complete(r)
}
