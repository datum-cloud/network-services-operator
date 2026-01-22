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
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// ConnectorAdvertisementReconciler reconciles a ConnectorAdvertisement object
type ConnectorAdvertisementReconciler struct {
	mgr mcmanager.Manager
}

const connectorAdvertisementConnectorRefIndex = "spec.connectorRef.name"

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectoradvertisements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectoradvertisements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectoradvertisements/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch

func (r *ConnectorAdvertisementReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var advertisement networkingv1alpha1.ConnectorAdvertisement
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &advertisement); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !advertisement.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling connectoradvertisement")
	defer logger.Info("reconcile complete")

	originalStatus := advertisement.Status.DeepCopy()

	acceptedCondition := apimeta.FindStatusCondition(advertisement.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
	if acceptedCondition == nil {
		acceptedCondition = &metav1.Condition{
			Type: networkingv1alpha1.ConnectorAdvertisementConditionAccepted,
		}
	}
	acceptedCondition.ObservedGeneration = advertisement.Generation

	if advertisement.Spec.ConnectorRef.Name == "" {
		acceptedCondition.Status = metav1.ConditionFalse
		acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonPending
		acceptedCondition.Message = "connectorRef.name is required"
		apimeta.SetStatusCondition(&advertisement.Status.Conditions, *acceptedCondition)
	} else {
		connector := &networkingv1alpha1.Connector{}
		connectorKey := client.ObjectKey{Namespace: advertisement.Namespace, Name: advertisement.Spec.ConnectorRef.Name}
		if err := cl.GetClient().Get(ctx, connectorKey, connector); err != nil {
			if apierrors.IsNotFound(err) {
				acceptedCondition.Status = metav1.ConditionFalse
				acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonConnectorNotFound
				acceptedCondition.Message = fmt.Sprintf("Connector %q not found", advertisement.Spec.ConnectorRef.Name)
				apimeta.SetStatusCondition(&advertisement.Status.Conditions, *acceptedCondition)
			} else {
				return ctrl.Result{}, err
			}
		} else {
			if !metav1.IsControlledBy(&advertisement, connector) {
				if err := controllerutil.SetControllerReference(connector, &advertisement, cl.GetScheme()); err != nil {
					return ctrl.Result{}, err
				}
				if err := cl.GetClient().Update(ctx, &advertisement); err != nil {
					return ctrl.Result{}, err
				}
			}
			acceptedCondition.Status = metav1.ConditionTrue
			acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonAccepted
			acceptedCondition.Message = "Connector reference resolved"
			apimeta.SetStatusCondition(&advertisement.Status.Conditions, *acceptedCondition)
		}
	}

	if !equality.Semantic.DeepEqual(*originalStatus, advertisement.Status) {
		if statusErr := cl.GetClient().Status().Update(ctx, &advertisement); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating connectoradvertisement status: %w", statusErr)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorAdvertisementReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&networkingv1alpha1.ConnectorAdvertisement{},
		connectorAdvertisementConnectorRefIndex,
		func(obj client.Object) []string {
			advertisement, ok := obj.(*networkingv1alpha1.ConnectorAdvertisement)
			if !ok || advertisement.Spec.ConnectorRef.Name == "" {
				return nil
			}
			return []string{advertisement.Spec.ConnectorRef.Name}
		},
	); err != nil {
		return err
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.ConnectorAdvertisement{}).
		Watches(
			&networkingv1alpha1.Connector{},
			func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					logger := log.FromContext(ctx)
					connector, ok := obj.(*networkingv1alpha1.Connector)
					if !ok {
						return nil
					}

					var ads networkingv1alpha1.ConnectorAdvertisementList
					if err := cl.GetClient().List(
						ctx,
						&ads,
						client.InNamespace(connector.Namespace),
						client.MatchingFields{connectorAdvertisementConnectorRefIndex: connector.Name},
					); err != nil {
						logger.Error(err, "failed to list connectoradvertisements", "connector", connector.Name)
						return nil
					}

					requests := make([]mcreconcile.Request, 0, len(ads.Items))
					for i := range ads.Items {
						requests = append(requests, mcreconcile.Request{
							ClusterName: clusterName,
							Request: ctrl.Request{
								NamespacedName: client.ObjectKeyFromObject(&ads.Items[i]),
							},
						})
					}

					return requests
				})
			},
		).
		Named("connectoradvertisement").
		Complete(r)
}
