// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	mcbuilder "github.com/multicluster-runtime/multicluster-runtime/pkg/builder"
	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	mcreconcile "github.com/multicluster-runtime/multicluster-runtime/pkg/reconcile"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// NetworkPolicyReconciler reconciles a NetworkPolicy object
type NetworkPolicyReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NetworkPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *NetworkPolicyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkPolicyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkPolicy{}).
		Named("networkpolicy").
		Complete(r)
}
