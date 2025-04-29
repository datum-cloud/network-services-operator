// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// SubnetClaimReconciler reconciles a SubnetClaim object
type SubnetClaimReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnetclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnetclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnetclaims/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SubnetClaim object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *SubnetClaimReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var claim networkingv1alpha.SubnetClaim
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !claim.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling subnet claim")
	defer logger.Info("reconcile complete")

	// TODO(jreese) move to a network context level subnet allocator, instead of
	// the 1:1 SubnetClaim:Subnet that's here right now.

	var subnet networkingv1alpha.Subnet
	if err := cl.GetClient().Get(ctx, client.ObjectKeyFromObject(&claim), &subnet); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed fetching subnet: %w", err)
	}

	if subnet.CreationTimestamp.IsZero() {
		var networkContext networkingv1alpha.NetworkContext
		networkContextObjectKey := client.ObjectKey{
			Namespace: claim.Namespace,
			Name:      claim.Spec.NetworkContext.Name,
		}
		if err := cl.GetClient().Get(ctx, networkContextObjectKey, &networkContext); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed fetching network context: %w", err)
		}

		subnet = networkingv1alpha.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: claim.Namespace,
				Name:      claim.Name,
			},
			Spec: networkingv1alpha.SubnetSpec{
				IPFamily:       claim.Spec.IPFamily,
				SubnetClass:    claim.Spec.SubnetClass,
				NetworkContext: claim.Spec.NetworkContext,
				Location:       claim.Spec.Location,
			},
		}

		if err := controllerutil.SetControllerReference(&networkContext, &subnet, cl.GetScheme()); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set controller on subnet: %w", err)
		}

		if err := cl.GetClient().Create(ctx, &subnet); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed creating subnet: %w", err)
		}

		apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "SubnetNotReady",
			ObservedGeneration: claim.Generation,
			Message:            "Subnet is not ready",
		})

		if err := cl.GetClient().Status().Update(ctx, &claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating claim status")
		}

		return ctrl.Result{}, nil
	}

	if !apimeta.IsStatusConditionTrue(subnet.Status.Conditions, "Ready") {
		logger.Info("subnet is not ready")
		return ctrl.Result{}, nil
	}

	claim.Status.SubnetRef = &networkingv1alpha.LocalSubnetReference{
		Name: subnet.Name,
	}

	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "SubnetReady",
		ObservedGeneration: claim.Generation,
		Message:            "Subnet ready",
	})

	if err := cl.GetClient().Status().Update(ctx, &claim); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed updating claim status")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetClaimReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.SubnetClaim{},
			mcbuilder.WithPredicates(
				predicate.NewPredicateFuncs(func(object client.Object) bool {
					// Don't bother processing deployments that have been scheduled
					o := object.(*networkingv1alpha.SubnetClaim)
					return o.Status.SubnetRef == nil
				}),
			),
			mcbuilder.WithEngageWithLocalCluster(false),
		).
		// TODO(jreese) change when we don't have claims 1:1 with subnets
		Watches(&networkingv1alpha.Subnet{}, mchandler.EnqueueRequestForObject).
		Named("subnetclaim").
		Complete(r)
}
