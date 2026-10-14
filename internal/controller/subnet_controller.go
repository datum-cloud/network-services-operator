// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	// DownstreamCluster is the federation hub. The replicator mirrors a Subnet
	// onto it, Karmada carries the copy to the cells serving the location, and
	// the cells' data plane reports on their copies; Karmada aggregates those
	// reports back onto the hub copy, which is the one place this controller
	// can read what the cells said. Nil, as in tests, means nothing reports and
	// Programmed is left as it is.
	DownstreamCluster cluster.Cluster

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets/finalizers,verbs=update
// +kubebuilder:rbac:groups=locations.miloapis.com,resources=locations,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Subnet object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *SubnetReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var subnet networkingv1alpha.Subnet
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &subnet); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !subnet.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("reconciling subnet")
	defer logger.V(1).Info("reconcile complete")

	programmed, err := r.programmedFromCells(ctx, req.ClusterName, cl.GetClient(), &subnet)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.reconcileSubnet(ctx, cl.GetClient(), &subnet, programmed)
}

// programmedFromCells reads the hub copy of the subnet and derives Programmed
// from what the cells serving its location reported there. A missing copy
// reads Pending. A read that fails for any other reason is returned, so the
// reconcile is retried rather than a status derived from a failed read.
//
// It returns nil when there is no hub to read, and Programmed is then left as
// it is.
func (r *SubnetReconciler) programmedFromCells(
	ctx context.Context,
	clusterName multicluster.ClusterName,
	upstreamClient client.Client,
	subnet *networkingv1alpha.Subnet,
) (*metav1.Condition, error) {
	if r.DownstreamCluster == nil {
		return nil, nil
	}

	strategy := downstreamclient.NewMappedNamespaceResourceStrategy(string(clusterName), upstreamClient, r.DownstreamCluster.GetClient())
	hubNamespace, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, subnet.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed resolving the hub namespace for subnet %s/%s: %w", subnet.Namespace, subnet.Name, err)
	}

	hubCopy := &networkingv1alpha.Subnet{}
	if err := r.DownstreamCluster.GetClient().Get(ctx, client.ObjectKey{Namespace: hubNamespace, Name: subnet.Name}, hubCopy); err != nil {
		if apierrors.IsNotFound(err) {
			hubCopy = nil
		} else {
			return nil, fmt.Errorf("failed reading the hub copy of subnet %s/%s: %w", subnet.Namespace, subnet.Name, err)
		}
	}

	condition := cellProgrammedCondition(hubCopy, subnet.Generation, subnet.Spec.Location.Name)
	return &condition, nil
}

// cellProgrammedCondition derives the subnet's Programmed condition from its hub
// copy, on which Karmada has aggregated what every cell serving the location
// reported.
//
// Each API server counts generation on its own, so a cell reports for the
// upstream generation the replicator stamped on the copy it was handed, and the
// report is taken only while that is still the generation here. The message is
// built from the reason and the location, never from the aggregated message:
// that one counts cells, which are the platform's.
func cellProgrammedCondition(hubCopy *networkingv1alpha.Subnet, upstreamGeneration int64, location string) metav1.Condition {
	condition := metav1.Condition{
		Type:               networkingv1alpha.SubnetProgrammed,
		Status:             metav1.ConditionUnknown,
		Reason:             networkingv1alpha.SubnetProgrammedReasonPending,
		ObservedGeneration: upstreamGeneration,
		Message:            fmt.Sprintf("Waiting for a cell serving %s to report on this address range", location),
	}
	if hubCopy == nil {
		return condition
	}

	copied, err := strconv.ParseInt(hubCopy.Annotations[networkingv1alpha1.UpstreamGenerationAnnotation], 10, 64)
	if err != nil || copied != upstreamGeneration {
		return condition
	}

	reported := apimeta.FindStatusCondition(hubCopy.Status.Conditions, networkingv1alpha.SubnetProgrammed)
	if reported == nil || reported.Status == metav1.ConditionUnknown || reported.ObservedGeneration != upstreamGeneration {
		return condition
	}

	switch {
	case reported.Status == metav1.ConditionTrue:
		condition.Status = metav1.ConditionTrue
		condition.Reason = networkingv1alpha.SubnetProgrammedReasonProgrammed
		condition.Message = fmt.Sprintf("Address range is in place on the network in %s", location)
	case reported.Reason == networkingv1alpha.SubnetProgrammedReasonProgrammingInProgress:
		condition.Status = metav1.ConditionFalse
		condition.Reason = networkingv1alpha.SubnetProgrammedReasonProgrammingInProgress
		condition.Message = fmt.Sprintf("Address range is being put in place on the network in %s", location)
	default:
		condition.Status = metav1.ConditionFalse
		condition.Reason = networkingv1alpha.SubnetProgrammedReasonNotProgrammed
		condition.Message = fmt.Sprintf("Address range is not in place on the network in %s", location)
	}
	return condition
}

// reconcileSubnet publishes the subnet's range and derives Ready. programmed is
// what the cells said, or nil to leave Programmed as it is.
func (r *SubnetReconciler) reconcileSubnet(
	ctx context.Context,
	cl client.Client,
	subnet *networkingv1alpha.Subnet,
	programmed *metav1.Condition,
) error {
	// TODO(jreese) finalizer work

	needsStatusUpdate := false
	if subnet.Status.StartAddress == nil && subnet.Spec.StartAddress != "" {
		// A subnet whose spec already carries a range was allocated by whoever
		// wrote it, out of the space its network holds. Nothing is left to
		// allocate here, and the table below would answer with a range from a
		// different address family entirely.
		needsStatusUpdate = true
		subnet.Status.StartAddress = proto.String(subnet.Spec.StartAddress)
		subnet.Status.PrefixLength = proto.Int32(subnet.Spec.PrefixLength)
	}

	if subnet.Status.StartAddress == nil {
		needsStatusUpdate = true
		var networkContext networkingv1alpha.NetworkContext
		networkContextObjectKey := client.ObjectKey{
			Namespace: subnet.Namespace,
			Name:      subnet.Spec.NetworkContext.Name,
		}
		if err := cl.Get(ctx, networkContextObjectKey, &networkContext); err != nil {
			return fmt.Errorf("failed fetching network context: %w", err)
		}

		if !apimeta.IsStatusConditionTrue(networkContext.Status.Conditions, networkingv1alpha.NetworkContextReady) {
			return fmt.Errorf("network context is not ready")
		}

		var location locationsv1alpha1.Location
		locationObjectKey := client.ObjectKey{
			Name: networkContext.Spec.Location.Name,
		}
		if err := cl.Get(ctx, locationObjectKey, &location); err != nil {
			if locationsNotServed(err) {
				return fmt.Errorf("network context location %q is not available", locationObjectKey.Name)
			}
			return fmt.Errorf("failed fetching network context location: %w", err)
		}

		cityCode, ok := location.Spec.Topology[locationsv1alpha1.TopologyCityCodeKey]
		if !ok {
			return fmt.Errorf("unable to find topology key: %s", locationsv1alpha1.TopologyCityCodeKey)
		}

		// TODO(jreese) move to proper higher level subnet allocation logic, this is
		// for the rough POC! Pay attention to the subnet class, etc.
		//
		// GCP allocates a /20 per region. Distribution seems to be as new regions
		// come online, a /20 is allocated, but there appears to be at least a /15
		// between each region's /20. For example:
		//
		// 	europe-west9      10.200.0.0/20
		// 	us-east5          10.202.0.0/20
		// 	europe-southwest1 10.204.0.0/20
		// 	us-south1         10.206.0.0/20
		// 	me-west1          10.208.0.0/20
		//
		// There's a few scenarios where this isn't the case.

		var startAddress string
		switch cityCode {
		case "DFW":
			startAddress = "10.128.0.0"
		case "DLS":
			startAddress = "10.130.0.0"
		case "LHR":
			startAddress = "10.132.0.0"
		}

		subnet.Status.StartAddress = proto.String(startAddress)
		subnet.Status.PrefixLength = proto.Int32(20)

	}

	if apimeta.SetStatusCondition(&subnet.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.SubnetAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             "PrefixAllocated",
		ObservedGeneration: subnet.Generation,
		Message:            "Subnet has been allocated a prefix",
	}) {
		needsStatusUpdate = true
	}

	if programmed != nil && apimeta.SetStatusCondition(&subnet.Status.Conditions, *programmed) {
		needsStatusUpdate = true
	}

	subnetProgrammed := apimeta.IsStatusConditionTrue(subnet.Status.Conditions, networkingv1alpha.SubnetProgrammed)

	readyMessage := "Subnet is not yet programmed"
	readyStatus := metav1.ConditionFalse
	readyReason := networkingv1alpha.SubnetProgrammedReasonNotProgrammed
	if subnetProgrammed {
		readyStatus = metav1.ConditionTrue
		readyReason = networkingv1alpha.SubnetReadyReasonReady
		readyMessage = "Subnet is ready to use"
	}

	if apimeta.SetStatusCondition(&subnet.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.SubnetReady,
		Status:             readyStatus,
		Reason:             readyReason,
		ObservedGeneration: subnet.Generation,
		Message:            readyMessage,
	}) {
		needsStatusUpdate = true
	}

	if needsStatusUpdate {
		if err := cl.Status().Update(ctx, subnet); err != nil {
			return fmt.Errorf("failed updating subnet status")
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	builder := mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.Subnet{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("subnet")

	// A cell's report lands on the hub copy. Ready follows it, both ways: a
	// range a cell stops carrying is no longer ready here.
	if r.DownstreamCluster != nil {
		builder = builder.WatchesRawSource(source.TypedKind(
			r.DownstreamCluster.GetCache(),
			&networkingv1alpha.Subnet{},
			handler.TypedEnqueueRequestsFromMapFunc(enqueueSubnetForHubCopy),
		))
	}

	return builder.Complete(r)
}

// enqueueSubnetForHubCopy maps a hub copy back to the project subnet it
// mirrors, by the labels the replicator stamps on every copy.
func enqueueSubnetForHubCopy(_ context.Context, hubCopy *networkingv1alpha.Subnet) []mcreconcile.Request {
	labels := hubCopy.GetLabels()
	clusterLabel := labels[downstreamclient.UpstreamOwnerClusterNameLabel]
	namespace := labels[downstreamclient.UpstreamOwnerNamespaceLabel]
	name := labels[downstreamclient.UpstreamOwnerNameLabel]
	if clusterLabel == "" || namespace == "" || name == "" {
		return nil
	}
	return []mcreconcile.Request{{
		ClusterName: multicluster.ClusterName(downstreamclient.UpstreamClusterNameFromLabel(clusterLabel)),
		Request:     ctrl.Request{NamespacedName: client.ObjectKey{Namespace: namespace, Name: name}},
	}}
}
