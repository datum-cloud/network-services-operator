// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/finalizer"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	networkControllerFinalizer = "networking.datumapis.com/network-controller"
	networkPrefixFinalizer     = "networking.datumapis.com/network-prefix-release"
)

// NetworkReconciler reconciles a Network object
type NetworkReconciler struct {
	// IPAM is optional. Left nil, no address space is claimed and a network is
	// reconciled exactly as it was before the operator reached IPAM at all.
	IPAM IPAMClientFactory

	// PrefixClass is the IPClass that hands out the range a network is
	// addressed from. Empty means the same as a nil IPAM: nothing is claimed.
	PrefixClass string

	mgr        mcmanager.Manager
	finalizers finalizer.Finalizers
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

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

	return r.reconcileNetwork(ctx, cl.GetClient(), &network)
}

func (r *NetworkReconciler) reconcileNetwork(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// The locations a network is attached to hold subnets carved out of its
	// range, and IPAM refuses to give a range back while anything is allocated
	// inside it. So the locations go first: finalization below takes them down
	// and reports that it is not done until they are gone, and only then is the
	// range released. Releasing first is a deadlock — the network waits on
	// subnets held by locations it is the only thing that will ever delete.
	finalizationResult, err := r.finalizers.Finalize(ctx, network)
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
		if err = cl.Update(ctx, network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update based on finalization result: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if !network.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(network, networkPrefixFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.releasePrefix(ctx, cl, network); err != nil {
			var occupied *rangeOccupied
			if errors.As(err, &occupied) {
				return r.reportPrefix(ctx, cl, network,
					networkingv1alpha.NetworkReasonRangeOccupied, occupied.message)
			}
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(network, networkPrefixFinalizer)
		if err := cl.Update(ctx, network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed removing prefix finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	return r.reconcilePrefix(ctx, cl, network)
}

// reconcilePrefix claims the network's IPv6 address space when the network is
// created, rather than leaving it to appear under the first interface that
// happens to want an address.
func (r *NetworkReconciler) reconcilePrefix(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
) (ctrl.Result, error) {
	if r.IPAM == nil || r.PrefixClass == "" {
		return ctrl.Result{}, r.reportReady(ctx, cl, network,
			"No address service is configured, so the network is used without claimed address space")
	}

	if !networkCarriesIPv6(network) {
		return ctrl.Result{}, r.reportReady(ctx, cl, network,
			"The network claims no address space")
	}

	routing, err := resolveProjectOrCluster(ctx, cl, network.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			return r.reportPrefix(ctx, cl, network,
				networkingv1alpha.NetworkReasonProjectUnresolved, unresolvable.Error())
		}
		return ctrl.Result{}, err
	}

	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed building IPAM client: %w", err)
	}

	if controllerutil.AddFinalizer(network, networkPrefixFinalizer) {
		if err := cl.Update(ctx, network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed adding prefix finalizer: %w", err)
		}
	}

	prefix, err := r.claimPrefix(ctx, ipamClient, routing, network)
	if err != nil {
		var refused *bindingRefused
		if errors.As(err, &refused) {
			return r.reportPrefix(ctx, cl, network, refused.reason, refused.message)
		}
		var failure *allocationFailure
		if errors.As(err, &failure) {
			return r.reportPrefix(ctx, cl, network, string(failure.reason), failure.message)
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.publishPrefix(ctx, cl, network, routing, prefix)
}

func (r *NetworkReconciler) claimPrefix(
	ctx context.Context,
	ipamClient client.Client,
	routing projectRouting,
	network *networkingv1alpha.Network,
) (scopeRange, error) {
	return holdScopeRange(ctx, ipamClient, routing, scopeRangeRequest{
		className: r.PrefixClass,
		claimName: networkPrefixClaimName(network),
		namespace: routing.projectNamespace,
		scope: map[string]ipamv1alpha1.ScopeRef{
			ipamScopeRoleNetwork: {
				APIGroup: datumNetworkingAPIGroup,
				Kind:     "Network",
				Name:     network.Name,
			},
		},
		subject:                 "this network",
		namespaceNotFoundReason: networkingv1alpha.NetworkReasonProjectNamespaceNotFound,
		rangeUnsupportedReason:  networkingv1alpha.NetworkReasonRangeUnsupported,
	})
}

func (r *NetworkReconciler) publishPrefix(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	routing projectRouting,
	prefix scopeRange,
) error {
	allocated := &networkingv1alpha.NetworkIPAMStatus{
		IPv6Prefix: prefix.cidr,
		IPv6PrefixRef: &networkingv1alpha.NetworkPrefixRef{
			Project:   routing.project,
			Namespace: routing.projectNamespace,
			ClaimName: networkPrefixClaimName(network),
			PoolName:  prefix.poolName,
		},
	}

	changed := !equality.Semantic.DeepEqual(network.Status.IPAM, allocated)
	network.Status.IPAM = allocated

	if apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkIPAMAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             "Allocated",
		ObservedGeneration: network.Generation,
		Message:            "The network is addressed from " + prefix.cidr,
	}) {
		changed = true
	}

	if setNetworkReady(network, "") {
		changed = true
	}

	if !changed {
		return nil
	}

	if err := cl.Status().Update(ctx, network); err != nil {
		return fmt.Errorf("failed updating network status: %w", err)
	}
	return nil
}

// reportPrefix says why no address space was allocated and comes back later.
// Nothing watches IPAM or the namespace, so a condition that clears on its own
// has no other way back.
func (r *NetworkReconciler) reportPrefix(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	reason string,
	message string,
) (ctrl.Result, error) {
	log.FromContext(ctx).Info("network address space cannot be allocated",
		"reason", reason, "message", message)

	changed := apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkIPAMAllocated,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		ObservedGeneration: network.Generation,
		Message:            message,
	})

	if setNetworkReady(network, "") {
		changed = true
	}

	if changed {
		if err := cl.Status().Update(ctx, network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating network status: %w", err)
		}
	}
	return ctrl.Result{RequeueAfter: rejectedClaimRetryInterval}, nil
}

// reportReady summarises on the network what a consumer otherwise has to
// assemble out of the allocation condition and the network's own families.
func (r *NetworkReconciler) reportReady(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	message string,
) error {
	if !setNetworkReady(network, message) {
		return nil
	}

	if err := cl.Status().Update(ctx, network); err != nil {
		return fmt.Errorf("failed updating network status: %w", err)
	}
	return nil
}

// setNetworkReady derives Ready from the address space the network needs.
// Allocation is the only thing a network waits on, so Ready carries that
// condition's own reason rather than a second vocabulary for the same
// failures. A network nothing is allocated for has nothing outstanding.
func setNetworkReady(network *networkingv1alpha.Network, message string) bool {
	ready := metav1.Condition{
		Type:               networkingv1alpha.NetworkReady,
		Status:             metav1.ConditionTrue,
		Reason:             networkingv1alpha.NetworkReadyReasonReady,
		ObservedGeneration: network.Generation,
		Message:            message,
	}

	if allocated := apimeta.FindStatusCondition(
		network.Status.Conditions, networkingv1alpha.NetworkIPAMAllocated,
	); allocated != nil {
		ready.Message = allocated.Message
		if allocated.Status != metav1.ConditionTrue {
			ready.Status = allocated.Status
			ready.Reason = allocated.Reason
		}
	}

	return apimeta.SetStatusCondition(&network.Status.Conditions, ready)
}

// releasePrefix gives back what this operator holds. The recorded reference is
// what it releases against, because a namespace that stopped naming its project
// must not turn into a network that cannot be deleted.
func (r *NetworkReconciler) releasePrefix(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
) error {
	if r.IPAM == nil {
		return nil
	}

	ref := prefixRef(network)
	if ref == nil {
		routing, err := resolveProjectOrCluster(ctx, cl, network.Namespace)
		if err != nil {
			log.FromContext(ctx).Info("network holds no recorded address space and names no project; releasing nothing",
				"error", err.Error())
			return nil
		}
		ref = &networkingv1alpha.NetworkPrefixRef{
			Project:   routing.project,
			Namespace: routing.projectNamespace,
			ClaimName: networkPrefixClaimName(network),
		}
	}

	ipamClient, err := r.IPAM.ClientForProject(ref.Project)
	if err != nil {
		return fmt.Errorf("failed building IPAM client: %w", err)
	}

	if err := releaseScopeRange(ctx, ipamClient, ref.Namespace, ref.ClaimName); err != nil {
		var occupied *rangeOccupied
		if errors.As(err, &occupied) {
			return &rangeOccupied{message: fmt.Sprintf(
				"the network's address space still has addresses allocated inside it: %s", occupied.message)}
		}
		return err
	}
	return nil
}

func prefixRef(network *networkingv1alpha.Network) *networkingv1alpha.NetworkPrefixRef {
	if network.Status.IPAM == nil {
		return nil
	}
	ref := network.Status.IPAM.IPv6PrefixRef
	if ref == nil || ref.Project == "" || ref.ClaimName == "" {
		return nil
	}
	return ref
}

func networkCarriesIPv6(network *networkingv1alpha.Network) bool {
	return network.Spec.IPAM.Mode == networkingv1alpha.NetworkIPAMModeAuto &&
		slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv6Protocol)
}

func networkPrefixClaimName(network *networkingv1alpha.Network) string {
	return "network-" + string(network.UID)
}

var errNetworkContextsExist = errors.New("network contexts exist")

func (r *NetworkReconciler) Finalize(ctx context.Context, obj client.Object) (finalizer.Result, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return finalizer.Result{}, fmt.Errorf("cluster name not found in context")
	}

	cl, err := r.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return finalizer.Result{}, err
	}

	return finalizeNetworkContexts(ctx, cl.GetClient(), obj)
}

// finalizeNetworkContexts takes down the locations a network is attached to,
// and reports that it is not done until they are gone.
func finalizeNetworkContexts(
	ctx context.Context,
	cl client.Client,
	network client.Object,
) (finalizer.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("finalizing network")

	networkContexts, err := networkContextsOfNetwork(ctx, cl, network)
	if err != nil {
		return finalizer.Result{}, err
	}

	if len(networkContexts) == 0 {
		logger.Info("network contexts have been removed")
		return finalizer.Result{}, nil
	}

	for i := range networkContexts {
		networkContext := &networkContexts[i]
		if networkContext.DeletionTimestamp.IsZero() {
			logger.Info("deleting network context", "network context", networkContext.Name)
			if err := cl.Delete(ctx, networkContext); client.IgnoreNotFound(err) != nil {
				return finalizer.Result{}, fmt.Errorf("failed deleting network context: %w", err)
			}
		}
	}

	// Really don't like using errors for communication here. I think we'd need
	// to move away from the finalizer helper to ensure we can wait on child
	// resources to be gone before allowing the finalizer to be removed.
	return finalizer.Result{}, errNetworkContextsExist
}

func networkContextsOfNetwork(
	ctx context.Context,
	cl client.Client,
	network client.Object,
) ([]networkingv1alpha.NetworkContext, error) {
	var networkContexts networkingv1alpha.NetworkContextList
	if err := cl.List(ctx, &networkContexts, client.InNamespace(network.GetNamespace())); err != nil {
		return nil, fmt.Errorf("failed listing network contexts: %w", err)
	}

	var owned []networkingv1alpha.NetworkContext
	for i := range networkContexts.Items {
		if networkContextBelongsTo(&networkContexts.Items[i], network) {
			owned = append(owned, networkContexts.Items[i])
		}
	}
	return owned, nil
}

// networkContextBelongsTo reads the relationship off what the context names,
// not off who created it. A context the binding controller made carries the
// network as its controller; one delivered by propagation carries no owner at
// all, and both are the same network's presence in a location.
func networkContextBelongsTo(
	networkContext *networkingv1alpha.NetworkContext,
	network client.Object,
) bool {
	if networkRef := metav1.GetControllerOf(networkContext); networkRef != nil {
		return networkRef.UID == network.GetUID()
	}
	return networkContext.Spec.Network.Name == network.GetName()
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
		// Not Owns: a context delivered by propagation carries no owner
		// reference, and the network waits on it all the same.
		Watches(&networkingv1alpha.NetworkContext{}, mchandler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []ctrl.Request {
				networkContext, ok := obj.(*networkingv1alpha.NetworkContext)
				if !ok {
					return nil
				}
				name := networkContext.Spec.Network.Name
				if networkRef := metav1.GetControllerOf(networkContext); networkRef != nil {
					name = networkRef.Name
				}
				if name == "" {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKey{
					Namespace: networkContext.Namespace,
					Name:      name,
				}}}
			}), mcbuilder.WithEngageWithLocalCluster(false)).
		Named("network").
		Complete(r)
}
