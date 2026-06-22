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
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// connectorReadyAnnotationKey is patched onto downstream Gateways when a
// Connector's Ready condition flips online↔offline. Envoy Gateway's
// AnnotationChangedPredicate on Gateway fires a full re-translation so the
// extension server can re-apply the correct routing config from its cache.
//
// This is the Mode-B (eppEmissionEnabled:false) replacement for the trigger
// that EnvoyPatchPolicy objects provided in Mode A: the EPP itself was the
// EG-watched resource; in Mode B we touch a Gateway annotation instead.
// Only used when r.DownstreamCluster is set and EPP emission is disabled.
const connectorReadyAnnotationKey = "networking.datumapis.com/connector-ready-generation"

// ConnectorReconciler reconciles a Connector object
type ConnectorReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	// DownstreamCluster is the edge/infra cluster where Gateways are
	// materialized and where Envoy Gateway runs. When non-nil and EPP
	// emission is disabled (Mode B), the reconciler touches a trigger
	// annotation on affected downstream Gateways whenever a Connector's
	// Ready condition flips, causing EG to re-translate via its
	// AnnotationChangedPredicate on the Gateway resource.
	DownstreamCluster cluster.Cluster
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
	// Capture the Ready condition BEFORE any mutations so we can detect a
	// status→Ready flip at the end of this reconcile (Mode B only).
	prevReadyCondition := apimeta.FindStatusCondition(originalStatus.Conditions, networkingv1alpha1.ConnectorConditionReady)

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

	leaseStatus, err := r.connectorLeaseReady(ctx, cl.GetClient(), &connector)
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

	// Mode B (extension server): when the Connector's Ready condition flips
	// (Lease expiry → offline, Lease renewal → online), touch a trigger
	// annotation on each affected downstream Gateway so EG's
	// AnnotationChangedPredicate fires a full re-translation. This replaces
	// the trigger that EPP objects provided in Mode A.
	//
	// The extensionManager.resources watch (TPP + Connector) already handles
	// spec-change triggers via GenerationChangedPredicate; status-only
	// transitions (this path) require the Gateway annotation touch because
	// status writes do NOT increment metadata.generation.
	if !r.Config.Gateway.IsEPPEmissionEnabled() && r.DownstreamCluster != nil {
		newReadyCondition := apimeta.FindStatusCondition(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionReady)
		if connectorReadyStatusChanged(prevReadyCondition, newReadyCondition) {
			if annotationErr := r.touchDownstreamGatewayAnnotations(ctx, cl.GetClient(), string(req.ClusterName), &connector); annotationErr != nil {
				// Log and continue — annotation touch is best-effort; a
				// missed touch means Envoy holds last-known-good config
				// until the next EG rebuild rather than crashing.
				logger.Error(annotationErr, "failed to touch downstream gateway annotations for connector ready state transition")
			}
		}
	}

	if leaseStatus.requeueAfter != nil {
		return ctrl.Result{RequeueAfter: *leaseStatus.requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

// connectorReadyStatusChanged returns true when the Ready condition's Status
// has changed between the previous and current condition. Nil → non-nil (first
// time the condition is set) is also treated as a change.
func connectorReadyStatusChanged(prev, next *metav1.Condition) bool {
	if prev == nil && next == nil {
		return false
	}
	if prev == nil || next == nil {
		return true
	}
	return prev.Status != next.Status
}

// connectorReadyAnnotationValue returns a deterministic string encoding of
// the Connector's Ready condition for use as the annotation value.
// The value changes only when the Ready Status changes (True ↔ False), so
// patching it onto a downstream Gateway is idempotent across reconciles that
// leave Ready in the same state.
func connectorReadyAnnotationValue(readyCondition *metav1.Condition) string {
	if readyCondition == nil {
		return "Unknown/Unknown"
	}
	return fmt.Sprintf("%s/%s", readyCondition.Status, readyCondition.Reason)
}

// touchDownstreamGatewayAnnotations patches a trigger annotation on every
// downstream Gateway backed by an HTTPProxy that references this Connector.
// The annotation change fires EG's AnnotationChangedPredicate on the Gateway,
// which enqueues a full re-translation so the extension server can re-apply
// the correct online/offline routing config from its informer cache.
func (r *ConnectorReconciler) touchDownstreamGatewayAnnotations(
	ctx context.Context,
	upstreamClient client.Client,
	clusterName string,
	connector *networkingv1alpha1.Connector,
) error {
	logger := log.FromContext(ctx)

	// List all HTTPProxies in the Connector's namespace; filter to those that
	// reference this Connector. The HTTPProxy controller creates one Gateway
	// per HTTPProxy with the same name and namespace, so Gateway affinity
	// follows directly from the HTTPProxy→Connector reference.
	var httpProxies networkingv1alpha.HTTPProxyList
	if err := upstreamClient.List(ctx, &httpProxies, client.InNamespace(connector.Namespace)); err != nil {
		return fmt.Errorf("list HTTPProxies in %s: %w", connector.Namespace, err)
	}

	// Resolve the downstream namespace once for the whole batch.
	strategy := downstreamclient.NewMappedNamespaceResourceStrategy(
		clusterName,
		upstreamClient,
		r.DownstreamCluster.GetClient(),
	)
	downstreamNS, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, connector.Namespace)
	if err != nil {
		return fmt.Errorf("get downstream namespace for %s: %w", connector.Namespace, err)
	}

	readyCondition := apimeta.FindStatusCondition(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionReady)
	annotationValue := connectorReadyAnnotationValue(readyCondition)
	downstreamCl := r.DownstreamCluster.GetClient()

	for i := range httpProxies.Items {
		hp := &httpProxies.Items[i]
		if !httpProxyReferencesConnector(hp, connector.Name) {
			continue
		}

		// Gateway name == HTTPProxy name (see HTTPProxyReconciler.collectDesiredResources).
		gwKey := client.ObjectKey{Namespace: downstreamNS, Name: hp.Name}
		var gw gatewayv1.Gateway
		if err := downstreamCl.Get(ctx, gwKey, &gw); err != nil {
			if apierrors.IsNotFound(err) {
				// Gateway not yet created or already deleted — skip.
				logger.V(1).Info("downstream gateway not found; skipping annotation touch",
					"gateway", gwKey)
				continue
			}
			return fmt.Errorf("get downstream gateway %s: %w", gwKey, err)
		}

		// Idempotent: skip Patch if the value hasn't changed.
		if gw.Annotations[connectorReadyAnnotationKey] == annotationValue {
			continue
		}

		patch := client.MergeFrom(gw.DeepCopy())
		if gw.Annotations == nil {
			gw.Annotations = make(map[string]string)
		}
		gw.Annotations[connectorReadyAnnotationKey] = annotationValue
		if err := downstreamCl.Patch(ctx, &gw, patch); err != nil {
			return fmt.Errorf("patch downstream gateway %s annotation: %w", gwKey, err)
		}
		logger.Info("touched downstream gateway annotation for connector ready state change",
			"gateway", gwKey,
			"connector", connector.Name,
			"annotationValue", annotationValue)
	}

	return nil
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

func (r *ConnectorReconciler) connectorLeaseReady(ctx context.Context, cl client.Client, connector *networkingv1alpha1.Connector) (connectorLeaseStatus, error) {
	if connector.Status.LeaseRef == nil || connector.Status.LeaseRef.Name == "" {
		return connectorLeaseStatus{message: "Connector lease has not been created yet."}, nil
	}

	var lease coordinationv1.Lease
	if err := cl.Get(ctx, client.ObjectKey{Namespace: connector.Namespace, Name: connector.Status.LeaseRef.Name}, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return connectorLeaseStatus{message: "Connector lease not found. Agent may be offline."}, nil
		}
		return connectorLeaseStatus{}, fmt.Errorf("failed to load connector lease: %w", err)
	}

	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return connectorLeaseStatus{message: "Connector lease has not been renewed yet."}, nil
	}

	expiryDuration := time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	expiresAt := lease.Spec.RenewTime.Add(expiryDuration)
	if time.Now().After(expiresAt) {
		return connectorLeaseStatus{message: "Connector lease has expired. Agent may be offline."}, nil
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

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.Connector{}).
		Watches(
			&networkingv1alpha1.ConnectorClass{},
			func(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
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
		Watches(
			&coordinationv1.Lease{},
			mchandler.EnqueueRequestForOwner(&networkingv1alpha1.Connector{}, handler.OnlyControllerOwner()),
		).
		Named("connector").
		Complete(r)
}
