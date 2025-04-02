// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

// GatewayClassReconciler reconciles a Network object
type GatewayClassReconciler struct {
	mgr mcmanager.Manager
	// controllerName is the domain-prefixed string that identifies this implementation
	controllerName gatewayv1.GatewayController
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/finalizers,verbs=update

func (r *GatewayClassReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var gatewayClass gatewayv1.GatewayClass
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &gatewayClass); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling gatewayclass")
	defer logger.Info("reconcile complete")

	// Check if this GatewayClass should be handled by this controller
	if gatewayClass.Spec.ControllerName != r.controllerName {
		return ctrl.Result{}, nil
	}

	// Check if the GatewayClass is compatible
	accepted := true
	reason := gatewayv1.GatewayClassReasonAccepted
	message := "The GatewayClass has been accepted by the controller"

	// We don't support parameters yet, so we reject any GatewayClass that has them
	if gatewayClass.Spec.ParametersRef != nil {
		accepted = false
		reason = gatewayv1.GatewayClassReasonInvalidParameters
		message = "The GatewayClass has invalid parameters"
	}

	// Update the Accepted condition
	acceptedCondition := metav1.Condition{
		Type:    string(gatewayv1.GatewayClassConditionStatusAccepted),
		Status:  metav1.ConditionTrue,
		Reason:  string(reason),
		Message: message,
	}

	if !accepted {
		acceptedCondition.Status = metav1.ConditionFalse
	}

	apimeta.SetStatusCondition(&gatewayClass.Status.Conditions, acceptedCondition)

	// Update the status
	if err := cl.GetClient().Status().Update(ctx, &gatewayClass); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayClassReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&gatewayv1.GatewayClass{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("gatewayclass").
		Complete(r)
}
