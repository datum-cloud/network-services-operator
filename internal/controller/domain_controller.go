// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	datumapisv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// DomainReconciler reconciles a Domain object
type DomainReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=datumapis.com,resources=domains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=datumapis.com,resources=domains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=datumapis.com,resources=domains/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DomainReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	// Get the cluster client
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Fetch the Domain instance
	domain := &datumapisv1alpha.Domain{}
	if err := cl.GetClient().Get(ctx, req.NamespacedName, domain); err != nil {
		// Handle the case where the Domain resource is not found
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// TODO: Implement domain verification logic
	// 1. Check if domain ownership is verified
	// 2. If not verified, generate and set verification TXT record
	// 3. Check DNS for verification record
	// 4. Update domain status with verification results
	// 5. If verified, fetch WHOIS data and update status

	logger.Info("Reconciling Domain", "domain", domain.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&datumapisv1alpha.Domain{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("domain").
		Complete(r)
}
