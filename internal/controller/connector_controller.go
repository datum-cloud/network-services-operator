// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// ConnectorReconciler reconciles a Connector object
type ConnectorReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectorclasses,verbs=get;list;watch

func (r *ConnectorReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var connector networkingv1alpha1.Connector
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &connector); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !connector.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling connector")
	defer logger.Info("reconcile complete")

	originalStatus := connector.Status.DeepCopy()

	acceptedCondition := apimeta.FindStatusCondition(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionAccepted)
	if acceptedCondition == nil {
		acceptedCondition = &metav1.Condition{
			Type: networkingv1alpha1.ConnectorConditionAccepted,
		}
	}
	acceptedCondition.ObservedGeneration = connector.Generation

	if connector.Spec.ConnectorClassName == "" {
		acceptedCondition.Status = metav1.ConditionFalse
		acceptedCondition.Reason = networkingv1alpha1.ConnectorReasonConnectorClassNotSpecified
		acceptedCondition.Message = "connectorClassName is required"
	} else {
		var connectorClass networkingv1alpha1.ConnectorClass
		if err := cl.GetClient().Get(ctx, client.ObjectKey{Name: connector.Spec.ConnectorClassName}, &connectorClass); err != nil {
			if apierrors.IsNotFound(err) {
				acceptedCondition.Status = metav1.ConditionFalse
				acceptedCondition.Reason = networkingv1alpha1.ConnectorReasonConnectorClassNotFound
				acceptedCondition.Message = fmt.Sprintf("ConnectorClass %q not found", connector.Spec.ConnectorClassName)
			} else {
				return ctrl.Result{}, err
			}
		} else {
			acceptedCondition.Status = metav1.ConditionTrue
			acceptedCondition.Reason = networkingv1alpha1.ConnectorReasonAccepted
			acceptedCondition.Message = "Connector class resolved"
		}
	}

	apimeta.SetStatusCondition(&connector.Status.Conditions, *acceptedCondition)

	if !equality.Semantic.DeepEqual(*originalStatus, connector.Status) {
		if statusErr := cl.GetClient().Status().Update(ctx, &connector); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating connector status: %w", statusErr)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.Connector{}).
		Named("connector").
		Complete(r)
}
