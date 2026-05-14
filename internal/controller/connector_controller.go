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
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	nsosource "go.datum.net/network-services-operator/internal/controller/source"
)

// ConnectorReconciler reconciles a Connector object
type ConnectorReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	// LeaseClient builds a typed Lease client for a given project cluster.
	// Production leaves this nil; the reconciler falls back to
	// coordinationv1client.NewForConfig(cl.GetConfig()). The typed client
	// encodes its own group/version, so it does not depend on the cluster's
	// REST mapper — that's the whole point of routing Lease access through
	// it rather than the controller-runtime client, which would fail with
	// `no matches for kind "Lease"` against project control planes that
	// omit coordination.k8s.io from discovery (see
	// network-services-operator#160). Tests inject a fake clientset's
	// CoordinationV1() so they don't need a real REST config.
	LeaseClient func(cluster.Cluster) (coordinationv1client.LeasesGetter, error)
}

// leases returns the typed Lease client for the supplied cluster.
func (r *ConnectorReconciler) leases(cl cluster.Cluster) (coordinationv1client.LeasesGetter, error) {
	if r.LeaseClient != nil {
		return r.LeaseClient(cl)
	}
	return coordinationv1client.NewForConfig(cl.GetConfig())
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

	if acceptedCondition.Status != metav1.ConditionTrue {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = networkingv1alpha1.ConnectorReasonNotReady
		readyCondition.Message = "Waiting for ConnectorClass to be resolved."
		apimeta.SetStatusCondition(&connector.Status.Conditions, *readyCondition)
		if !equality.Semantic.DeepEqual(*originalStatus, connector.Status) {
			if statusErr := cl.GetClient().Status().Update(ctx, &connector); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed updating connector status: %w", statusErr)
			}
		}
		return ctrl.Result{}, nil
	}

	leases, err := r.leases(cl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("build lease client: %w", err)
	}

	leaseDurationSeconds := r.connectorLeaseDurationSeconds()
	if connector.Status.LeaseRef == nil || connector.Status.LeaseRef.Name == "" {
		lease, err := r.ensureConnectorLease(ctx, cl, leases, &connector, leaseDurationSeconds)
		if err != nil {
			return ctrl.Result{}, err
		}
		connector.Status.LeaseRef = &corev1.LocalObjectReference{Name: lease.Name}
	}

	leaseStatus, err := r.connectorLeaseReady(ctx, leases, &connector)
	if err != nil {
		return ctrl.Result{}, err
	}
	if leaseStatus.ready {
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = networkingv1alpha1.ConnectorReasonReady
		readyCondition.Message = "The connector is ready to tunnel traffic."
	} else {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = networkingv1alpha1.ConnectorReasonNotReady
		readyCondition.Message = leaseStatus.message
	}

	apimeta.SetStatusCondition(&connector.Status.Conditions, *readyCondition)

	if !equality.Semantic.DeepEqual(*originalStatus, connector.Status) {
		if statusErr := cl.GetClient().Status().Update(ctx, &connector); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating connector status: %w", statusErr)
		}
	}

	if leaseStatus.requeueAfter != nil {
		return ctrl.Result{RequeueAfter: *leaseStatus.requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ConnectorReconciler) connectorLeaseDurationSeconds() int32 {
	if r.Config.Connector.LeaseDurationSeconds > 0 {
		return r.Config.Connector.LeaseDurationSeconds
	}
	return 30
}

type connectorLeaseStatus struct {
	ready        bool
	requeueAfter *time.Duration
	message      string
}

// ensureConnectorLease creates the per-Connector Lease if absent, or refreshes
// the owner reference / lease duration on an existing one. Goes through the
// typed Lease client to avoid the controller-runtime client's REST mapper,
// which has no mapping for coordination.k8s.io on project control planes that
// hide the group from discovery.
func (r *ConnectorReconciler) ensureConnectorLease(
	ctx context.Context,
	cl cluster.Cluster,
	leases coordinationv1client.LeasesGetter,
	connector *networkingv1alpha1.Connector,
	leaseDurationSeconds int32,
) (*coordinationv1.Lease, error) {
	existing, err := leases.Leases(connector.Namespace).Get(ctx, connector.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		lease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      connector.Name,
				Namespace: connector.Namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				LeaseDurationSeconds: &leaseDurationSeconds,
			},
		}
		if err := controllerutil.SetControllerReference(connector, lease, cl.GetScheme()); err != nil {
			return nil, fmt.Errorf("set owner ref on connector lease: %w", err)
		}
		created, err := leases.Leases(connector.Namespace).Create(ctx, lease, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			// A racing reconcile (or the agent) created it; re-fetch.
			return leases.Leases(connector.Namespace).Get(ctx, connector.Name, metav1.GetOptions{})
		}
		if err != nil {
			return nil, fmt.Errorf("create connector lease: %w", err)
		}
		return created, nil
	case err != nil:
		return nil, fmt.Errorf("load connector lease: %w", err)
	}

	mutated := existing.DeepCopy()
	if err := controllerutil.SetControllerReference(connector, mutated, cl.GetScheme()); err != nil {
		return nil, fmt.Errorf("set owner ref on connector lease: %w", err)
	}
	if mutated.Spec.LeaseDurationSeconds == nil || *mutated.Spec.LeaseDurationSeconds == 0 {
		mutated.Spec.LeaseDurationSeconds = &leaseDurationSeconds
	}
	if equality.Semantic.DeepEqual(existing, mutated) {
		return existing, nil
	}
	updated, err := leases.Leases(connector.Namespace).Update(ctx, mutated, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("update connector lease: %w", err)
	}
	return updated, nil
}

func (r *ConnectorReconciler) connectorLeaseReady(ctx context.Context, leases coordinationv1client.LeasesGetter, connector *networkingv1alpha1.Connector) (connectorLeaseStatus, error) {
	// Fallback poll interval for the not-ready paths. Used when discovery on
	// this project control plane is missing coordination.k8s.io, so the Lease
	// watch is no-op'd by nsosource.OptionalKind and only this RequeueAfter
	// keeps the Connector converging. Matches the per-Connector lease cadence
	// so a freshly-renewed Lease shows up Ready within one duration.
	leaseDuration := time.Duration(r.connectorLeaseDurationSeconds()) * time.Second
	pollAfter := leaseDuration + leaseJitter(leaseDuration)

	if connector.Status.LeaseRef == nil || connector.Status.LeaseRef.Name == "" {
		return connectorLeaseStatus{message: "Connector lease has not been created yet.", requeueAfter: &pollAfter}, nil
	}

	lease, err := leases.Leases(connector.Namespace).Get(ctx, connector.Status.LeaseRef.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return connectorLeaseStatus{message: "Connector lease not found. Agent may be offline.", requeueAfter: &pollAfter}, nil
		}
		return connectorLeaseStatus{}, fmt.Errorf("failed to load connector lease: %w", err)
	}

	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return connectorLeaseStatus{message: "Connector lease has not been renewed yet.", requeueAfter: &pollAfter}, nil
	}

	expiryDuration := time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	expiresAt := lease.Spec.RenewTime.Add(expiryDuration)
	if time.Now().After(expiresAt) {
		return connectorLeaseStatus{message: "Connector lease has expired. Agent may be offline.", requeueAfter: &pollAfter}, nil
	}

	requeueAfter := time.Until(expiresAt) + leaseJitter(expiryDuration)
	return connectorLeaseStatus{ready: true, requeueAfter: &requeueAfter}, nil
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

	c, err := mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.Connector{}).
		Watches(
			&networkingv1alpha1.ConnectorClass{},
			func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					logger := log.FromContext(ctx)

					connectorClass, ok := obj.(*networkingv1alpha1.ConnectorClass)
					if !ok {
						return nil
					}

					var connectors networkingv1alpha1.ConnectorList
					if err := cl.GetClient().List(ctx, &connectors); err != nil {
						logger.Error(err, "failed to list Connectors for ConnectorClass watch", "connectorClass", connectorClass.Name)
						return nil
					}

					var requests []mcreconcile.Request
					for i := range connectors.Items {
						connector := &connectors.Items[i]
						if connector.Spec.ConnectorClassName != connectorClass.Name {
							continue
						}
						requests = append(requests, mcreconcile.Request{
							ClusterName: clusterName,
							Request: ctrl.Request{
								NamespacedName: client.ObjectKeyFromObject(connector),
							},
						})
					}

					return requests
				})
			},
		).
		Named("connector").
		Build(r)
	if err != nil {
		return err
	}

	// Lease watch is best-effort: some project control planes omit
	// coordination.k8s.io from API discovery even though Lease itself is
	// served (see network-services-operator#160). nsosource.OptionalKind
	// keeps the per-cluster engagement alive when the GVK is missing from
	// discovery; reconcile latency on those clusters falls back to the
	// connectorLeaseReady RequeueAfter cadence above. The builder's
	// .Watches() always wires mcsource.Kind, so we bypass it for Lease and
	// call MultiClusterWatch directly with the optional wrapper.
	return c.MultiClusterWatch(
		nsosource.OptionalKind(
			&coordinationv1.Lease{},
			mchandler.EnqueueRequestForOwner(&networkingv1alpha1.Connector{}, handler.OnlyControllerOwner()),
		),
	)
}
