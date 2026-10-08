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
// It is the projector's backstop rather than its cleanup path: the projector
// holds a published interface until its copy is collected, so the ordinary
// deletion never reaches here. What does reach here is a copy that outlived a
// deletion nothing was there to hold, and a copy somebody deleted by hand, both
// of which are answered from the copy itself.
//
// It asks only whether anything is published behind a copy. It deliberately does
// not compare what the copy was published from: it reads the hub through a cache
// that lags, and a copy that had just been replaced would look superseded to a
// lagging read, so identity is the writer's question and presence is this one's.
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
