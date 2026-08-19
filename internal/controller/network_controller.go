// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/finalizer"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const networkControllerFinalizer = "networking.datumapis.com/network-controller"

const (
	// routingIdentityClassName is platform-unique by construction: the class
	// sets no poolPer and no uniqueWithin, so one address space serves every
	// network.
	routingIdentityClassName = "datum-vpc-identity"

	routingIdentityClaimPrefix = "network-routing-identity-"

	// Nothing watches IPAM, so an allocation refused for a condition that later
	// clears has no other way back.
	routingIdentityRetryInterval = time.Minute
)

// NetworkReconciler reconciles a Network object
type NetworkReconciler struct {
	// IPAM allocates the network's routing identity. A nil factory leaves the
	// identity unallocated and every other part of the reconciler unchanged.
	IPAM IPAMClientFactory

	mgr        mcmanager.Manager
	finalizers finalizer.Finalizers
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks/finalizers,verbs=update

func (r *NetworkReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	ctx = mccontext.WithCluster(ctx, req.ClusterName)

	var network networkingv1alpha.Network
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &network); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling network")
	defer logger.Info("reconcile complete")

	finalizationResult, err := r.finalizers.Finalize(ctx, &network)
	if err != nil {
		if v, ok := err.(kerrors.Aggregate); ok && v.Is(errNetworkContextsExist) {
			// Don't produce an error in this case and let the watch on network contexts
			// result in another reconcile schedule.
			logger.Info("network still has network contexts, waiting until removal")
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to finalize: %w", err)
		}
	}
	if finalizationResult.Updated {
		if err = cl.GetClient().Update(ctx, &network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update based on finalization result: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if !network.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return r.reconcileRoutingIdentity(ctx, cl.GetClient(), string(req.ClusterName), &network)
}

func (r *NetworkReconciler) reconcileRoutingIdentity(
	ctx context.Context,
	cl client.Client,
	project string,
	network *networkingv1alpha.Network,
) (ctrl.Result, error) {
	if r.IPAM == nil {
		return ctrl.Result{}, nil
	}

	if network.Status.RoutingIdentity != nil {
		return ctrl.Result{}, r.publishRoutingIdentity(ctx, cl, network, network.Status.RoutingIdentity)
	}

	ipamClient, err := r.IPAM.ClientForProject(project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed building IPAM client for project %q: %w", project, err)
	}

	identity, err := claimRoutingIdentity(ctx, ipamClient, project, network)
	if err != nil {
		var failure *allocationFailure
		if errors.As(err, &failure) {
			return ctrl.Result{RequeueAfter: routingIdentityRetryInterval},
				r.refuseRoutingIdentity(ctx, cl, network, failure)
		}
		return ctrl.Result{}, err
	}

	log.FromContext(ctx).Info("allocated a routing identity",
		"prefix", identity.Prefix, "ipclaim", identity.ClaimRef.Name, "project", project)

	return ctrl.Result{}, r.publishRoutingIdentity(ctx, cl, network, identity)
}

func routingIdentityClaimName(network *networkingv1alpha.Network) string {
	return routingIdentityClaimPrefix + string(network.UID)
}

func routingIdentityRequest(network *networkingv1alpha.Network) allocationRequest {
	return allocationRequest{
		className:   routingIdentityClassName,
		family:      networkingv1alpha.IPv6Protocol,
		description: fmt.Sprintf("a routing identity for network %q", network.Name),
	}
}

// claimRoutingIdentity allocates the identity, or finds the one an earlier
// attempt already allocated. IPAM binds on create, so the response carries the
// prefix and nothing has to be waited for; it refuses a duplicate name rather
// than returning the existing claim, so the read comes first.
func claimRoutingIdentity(
	ctx context.Context,
	ipamClient client.Client,
	project string,
	network *networkingv1alpha.Network,
) (*networkingv1alpha.NetworkRoutingIdentity, error) {
	request := routingIdentityRequest(network)

	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = network.Namespace
	ipClaim.Name = routingIdentityClaimName(network)
	ipClaim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName: routingIdentityClassName,
		// An identity still installed in a remote location's forwarding tables
		// must not be handed to another network, so the allocation outlives the
		// claim releasing it.
		ReclaimPolicy: ipamv1alpha1.ReclaimRetain,
		OwnerRef: &ipamv1alpha1.ObjectRef{
			APIGroup:  datumNetworkingAPIGroup,
			Kind:      "Network",
			Namespace: network.Namespace,
			Name:      network.Name,
		},
	}

	existing := &ipamv1alpha1.IPClaim{}
	getErr := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), existing)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return nil, fmt.Errorf("failed reading IPClaim %q: %w", ipClaim.Name, getErr)
	}

	if getErr == nil {
		ipClaim = existing
	} else if createErr := ipamClient.Create(ctx, ipClaim); createErr != nil {
		// The platform provisions this namespace with the project, so a missing
		// one is not a race to wait out. Retrying it as an error would spin
		// forever with nothing said on the network.
		if isNamespaceNotFound(createErr, ipClaim.Namespace) {
			return nil, &allocationFailure{
				reason: networkingv1alpha.NetworkReasonProjectNamespaceNotFound,
				message: fmt.Sprintf(
					"Project %q has no namespace %q in its control plane, so no routing identity can be allocated for network %q",
					project, ipClaim.Namespace, network.Name),
			}
		}

		raced := &ipamv1alpha1.IPClaim{}
		if err := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), raced); err != nil {
			reason := classifyAllocationFailure(createErr)
			return nil, &allocationFailure{
				reason:  reason,
				message: allocationFailureMessage(reason, request, createErr),
			}
		}
		ipClaim = raced
	}

	if ipClaim.Status.AllocatedCIDR == "" {
		return nil, &allocationFailure{
			reason: allocationFailureUnknown,
			message: fmt.Sprintf("IPAM reported no prefix for %s (phase %q)",
				request.describe(), ipClaim.Status.Phase),
		}
	}

	return &networkingv1alpha.NetworkRoutingIdentity{
		Prefix: ipClaim.Status.AllocatedCIDR,
		ClaimRef: networkingv1alpha.IPClaimRef{
			Namespace: ipClaim.Namespace,
			Name:      ipClaim.Name,
		},
	}, nil
}

func (r *NetworkReconciler) publishRoutingIdentity(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	identity *networkingv1alpha.NetworkRoutingIdentity,
) error {
	before := network.DeepCopy()

	network.Status.RoutingIdentity = identity
	apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             "Allocated",
		ObservedGeneration: network.Generation,
		Message:            fmt.Sprintf("The network holds routing identity %s", identity.Prefix),
	})
	setNetworkReady(network)

	if equality.Semantic.DeepEqual(before.Status, network.Status) {
		return nil
	}

	if err := cl.Status().Update(ctx, network); err != nil {
		return fmt.Errorf("failed updating network status: %w", err)
	}
	return nil
}

func (r *NetworkReconciler) refuseRoutingIdentity(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	failure *allocationFailure,
) error {
	log.FromContext(ctx).Info("no routing identity could be allocated",
		"reason", failure.reason, "message", failure.message)

	before := network.DeepCopy()

	apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkAllocated,
		Status:             metav1.ConditionFalse,
		Reason:             string(failure.reason),
		ObservedGeneration: network.Generation,
		Message:            failure.message,
	})
	setNetworkReady(network)

	if equality.Semantic.DeepEqual(before.Status, network.Status) {
		return nil
	}

	if err := cl.Status().Update(ctx, network); err != nil {
		return fmt.Errorf("failed updating network status: %w", err)
	}
	return nil
}

func setNetworkReady(network *networkingv1alpha.Network) {
	allocated := apimeta.FindStatusCondition(network.Status.Conditions, networkingv1alpha.NetworkAllocated)
	if allocated != nil && allocated.Status == metav1.ConditionTrue {
		apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.NetworkReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			ObservedGeneration: network.Generation,
			Message:            "The network is allocated and can be placed into a location",
		})
		return
	}

	condition := metav1.Condition{
		Type:               networkingv1alpha.NetworkReady,
		Status:             metav1.ConditionFalse,
		Reason:             "NotAllocated",
		ObservedGeneration: network.Generation,
		Message:            "Waiting for " + networkingv1alpha.NetworkAllocated,
	}
	if allocated != nil {
		condition.Reason = allocated.Reason
		condition.Message = allocated.Message
	}
	apimeta.SetStatusCondition(&network.Status.Conditions, condition)
}

// releaseRoutingIdentity deletes the claim behind the identity. The allocation
// is retained by IPAM, so the identifier is not handed to another network while
// a location may still be forwarding on it.
func (r *NetworkReconciler) releaseRoutingIdentity(
	ctx context.Context,
	project string,
	network *networkingv1alpha.Network,
) error {
	if r.IPAM == nil || network.Status.RoutingIdentity == nil {
		return nil
	}

	ipamClient, err := r.IPAM.ClientForProject(project)
	if err != nil {
		return fmt.Errorf("failed building IPAM client for project %q: %w", project, err)
	}

	ref := network.Status.RoutingIdentity.ClaimRef
	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = ref.Namespace
	ipClaim.Name = ref.Name

	if err := ipamClient.Delete(ctx, ipClaim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed releasing routing identity claim %q: %w", ref.Name, err)
	}
	return nil
}

var errNetworkContextsExist = errors.New("network contexts exist")

func (r *NetworkReconciler) Finalize(ctx context.Context, obj client.Object) (finalizer.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("finalizing network")

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return finalizer.Result{}, fmt.Errorf("cluster name not found in context")
	}

	cl, err := r.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return finalizer.Result{}, err
	}

	listOpts := client.MatchingFields{
		networkContextControllerNetworkUIDIndex: string(obj.GetUID()),
	}
	var networkContexts networkingv1alpha.NetworkContextList
	if err := cl.GetClient().List(ctx, &networkContexts, listOpts); err != nil {
		return finalizer.Result{}, err
	}

	if len(networkContexts.Items) == 0 {
		log.FromContext(ctx).Info("network contexts have been removed")

		network, ok := obj.(*networkingv1alpha.Network)
		if !ok {
			return finalizer.Result{}, fmt.Errorf("expected a Network, got %T", obj)
		}
		return finalizer.Result{}, r.releaseRoutingIdentity(ctx, string(clusterName), network)
	}

	// All deployments need to be deleted before the workload may be deleted
	for _, networkContext := range networkContexts.Items {
		if networkContext.DeletionTimestamp.IsZero() {
			logger.Info("deleting network context", "network context", networkContext.Name)
			// Deletion will result in another reconcile of the workload, where we
			// will remove the finalizers.
			if err := cl.GetClient().Delete(ctx, &networkContext); client.IgnoreNotFound(err) != nil {
				return finalizer.Result{}, fmt.Errorf("failed deleting network context: %w", err)
			}
		}
	}

	// Really don't like using errors for communication here. I think we'd need
	// to move away from the finalizer helper to ensure we can wait on child
	// resources to be gone before allowing the finalizer to be removed.
	return finalizer.Result{}, errNetworkContextsExist
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	r.finalizers = finalizer.NewFinalizers()
	if err := r.finalizers.Register(networkControllerFinalizer, r); err != nil {
		return fmt.Errorf("failed to register finalizer: %w", err)
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.Network{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Owns(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("network").
		Complete(r)
}
