// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

// ConnectorReconciler reconciles a Connector object
type ConnectorReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectorclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

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
	readyCondition := apimeta.FindStatusCondition(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionReady)
	if readyCondition == nil {
		readyCondition = &metav1.Condition{
			Type: networkingv1alpha1.ConnectorConditionReady,
		}
	}
	readyCondition.ObservedGeneration = connector.Generation

	acceptedCondition := apimeta.FindStatusCondition(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionAccepted)
	if acceptedCondition == nil {
		acceptedCondition = &metav1.Condition{
			Type: networkingv1alpha1.ConnectorConditionAccepted,
		}
	}
	acceptedCondition.ObservedGeneration = connector.Generation

	if connector.Spec.ConnectorClassName == "" {
		acceptedCondition.Status = metav1.ConditionFalse
		acceptedCondition.Reason = networkingv1alpha1.ConnectorReasonPending
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
	apimeta.SetStatusCondition(&connector.Status.Conditions, *readyCondition)

	leaseDurationSeconds := r.connectorLeaseDurationSeconds()
	if connector.Status.LeaseRef == nil || connector.Status.LeaseRef.Name == "" {
		lease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      connector.Name,
				Namespace: connector.Namespace,
			},
		}

		if _, err := controllerutil.CreateOrUpdate(ctx, cl.GetClient(), lease, func() error {
			if err := controllerutil.SetControllerReference(&connector, lease, cl.GetScheme()); err != nil {
				return err
			}
			if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds == 0 {
				lease.Spec.LeaseDurationSeconds = &leaseDurationSeconds
			}
			return nil
		}); err != nil {
			return ctrl.Result{}, err
		}

		connector.Status.LeaseRef = &corev1.LocalObjectReference{Name: lease.Name}
	}

	leaseReady, leaseMessage, requeueAfter := r.connectorLeaseReady(ctx, cl.GetClient(), &connector)
	if leaseReady {
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = networkingv1alpha1.ConnectorReasonReady
		readyCondition.Message = "The connector is ready to tunnel traffic."
	} else {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = networkingv1alpha1.ConnectorReasonNotReady
		readyCondition.Message = leaseMessage
	}

	apimeta.SetStatusCondition(&connector.Status.Conditions, *readyCondition)

	if !equality.Semantic.DeepEqual(*originalStatus, connector.Status) {
		if statusErr := cl.GetClient().Status().Update(ctx, &connector); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating connector status: %w", statusErr)
		}
	}

	if requeueAfter != nil {
		return ctrl.Result{RequeueAfter: *requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ConnectorReconciler) connectorLeaseDurationSeconds() int32 {
	if r.Config.Connector.LeaseDurationSeconds > 0 {
		return r.Config.Connector.LeaseDurationSeconds
	}
	return 30
}

func (r *ConnectorReconciler) connectorLeaseReady(ctx context.Context, cl client.Client, connector *networkingv1alpha1.Connector) (bool, string, *time.Duration) {
	if connector.Status.LeaseRef == nil || connector.Status.LeaseRef.Name == "" {
		return false, "Connector lease has not been created yet.", nil
	}

	var lease coordinationv1.Lease
	if err := cl.Get(ctx, client.ObjectKey{Namespace: connector.Namespace, Name: connector.Status.LeaseRef.Name}, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return false, "Connector lease not found. Agent may be offline.", nil
		}
		return false, fmt.Sprintf("Failed to load connector lease: %v", err), nil
	}

	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return false, "Connector lease has not been renewed yet.", nil
	}

	expiryDuration := time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	expiresAt := lease.Spec.RenewTime.Add(expiryDuration)
	if time.Now().After(expiresAt) {
		return false, "Connector lease has expired. Agent may be offline.", nil
	}

	requeueAfter := time.Until(expiresAt) + leaseJitter(expiryDuration)
	return true, "", &requeueAfter
}

func leaseJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	jitterMax := base / 10
	if jitterMax <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(jitterMax)))
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.Connector{}).
		Watches(
			&coordinationv1.Lease{},
			mchandler.EnqueueRequestForOwner(&networkingv1alpha1.Connector{}, handler.OnlyControllerOwner()),
		).
		Named("connector").
		Complete(r)
}
