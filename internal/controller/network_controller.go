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

	if !network.DeletionTimestamp.IsZero() &&
		controllerutil.ContainsFinalizer(network, networkPrefixFinalizer) {
		if err := r.releasePrefix(ctx, cl, network); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(network, networkPrefixFinalizer)
		if err := cl.Update(ctx, network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed removing prefix finalizer: %w", err)
		}
	}

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
	if r.IPAM == nil || r.PrefixClass == "" || !networkCarriesIPv6(network) {
		return ctrl.Result{}, nil
	}

	routing, err := r.resolveProject(ctx, cl, network)
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

	prefix, poolName, err := r.claimPrefix(ctx, ipamClient, routing, network)
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

	return ctrl.Result{}, r.publishPrefix(ctx, cl, network, routing, prefix, poolName)
}

// claimPrefix holds the range the network is addressed from, and reports it.
//
// The claim's name is derived from the network's UID, so a reconcile that lost
// its answer finds the same range again instead of taking a second one. IPAM
// binds on create and refuses a duplicate name, so the read comes first.
func (r *NetworkReconciler) claimPrefix(
	ctx context.Context,
	ipamClient client.Client,
	routing projectRouting,
	network *networkingv1alpha.Network,
) (string, string, error) {
	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = routing.projectNamespace
	ipClaim.Name = networkPrefixClaimName(network)
	ipClaim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName: r.PrefixClass,
		Target:    ipamv1alpha1.TargetScopeRange,
		Scope: map[string]ipamv1alpha1.ScopeRef{
			ipamScopeRoleNetwork: {
				APIGroup: datumNetworkingAPIGroup,
				Kind:     "Network",
				Name:     network.Name,
			},
		},
	}

	existing := &ipamv1alpha1.IPClaim{}
	getErr := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), existing)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return "", "", fmt.Errorf("failed reading IPClaim %q: %w", ipClaim.Name, getErr)
	}

	if getErr == nil {
		ipClaim = existing
	} else if createErr := ipamClient.Create(ctx, ipClaim); createErr != nil {
		if isNamespaceNotFound(createErr, routing.projectNamespace) {
			return "", "", &bindingRefused{
				reason: networkingv1alpha.NetworkReasonProjectNamespaceNotFound,
				message: fmt.Sprintf(
					"Project %q has no namespace %q in its control plane, so no address space can be allocated for it",
					routing.project, routing.projectNamespace),
			}
		}

		raced := &ipamv1alpha1.IPClaim{}
		if err := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), raced); err != nil {
			reason := classifyAllocationFailure(createErr)
			return "", "", &allocationFailure{
				reason:  reason,
				message: r.prefixFailureMessage(reason, createErr),
			}
		}
		ipClaim = raced
	}

	if ipClaim.Status.AllocatedCIDR == "" {
		return "", "", &allocationFailure{
			reason: allocationFailureUnknown,
			message: fmt.Sprintf("IPAM allocated no address space for this network (phase %q)",
				ipClaim.Status.Phase),
		}
	}

	poolName := ""
	if ipClaim.Status.PoolRef != nil {
		poolName = ipClaim.Status.PoolRef.Name
	}
	return ipClaim.Status.AllocatedCIDR, poolName, nil
}

func (r *NetworkReconciler) publishPrefix(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	routing projectRouting,
	prefix string,
	poolName string,
) error {
	allocated := &networkingv1alpha.NetworkIPAMStatus{
		IPv6Prefix: prefix,
		IPv6PrefixRef: &networkingv1alpha.NetworkPrefixRef{
			Project:   routing.project,
			Namespace: routing.projectNamespace,
			ClaimName: networkPrefixClaimName(network),
			PoolName:  poolName,
		},
	}

	changed := !equality.Semantic.DeepEqual(network.Status.IPAM, allocated)
	network.Status.IPAM = allocated

	if apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkIPAMAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             "Allocated",
		ObservedGeneration: network.Generation,
		Message:            "The network is addressed from " + prefix,
	}) {
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

	if apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkIPAMAllocated,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		ObservedGeneration: network.Generation,
		Message:            message,
	}) {
		if err := cl.Status().Update(ctx, network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating network status: %w", err)
		}
	}
	return ctrl.Result{RequeueAfter: rejectedClaimRetryInterval}, nil
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
		routing, err := r.resolveProject(ctx, cl, network)
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

	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = ref.Namespace
	ipClaim.Name = ref.ClaimName
	if err := ipamClient.Delete(ctx, ipClaim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed releasing IPClaim %q: %w", ipClaim.Name, err)
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

// resolveProject reads the project from the network's namespace, and falls back
// to the cluster the network was read from. A namespace that declares an owner
// is answering for a control plane holding several projects' objects; one that
// declares none is a project's own control plane, which the cluster names.
func (r *NetworkReconciler) resolveProject(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
) (projectRouting, error) {
	routing, err := resolveProjectRouting(ctx, cl, network.Namespace)
	if err == nil {
		return routing, nil
	}

	var unresolvable *projectUnresolvable
	if !errors.As(err, &unresolvable) {
		return projectRouting{}, err
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok || string(clusterName) == "" {
		return projectRouting{}, err
	}

	return projectRouting{
		project:          string(clusterName),
		projectNamespace: network.Namespace,
	}, nil
}

func networkCarriesIPv6(network *networkingv1alpha.Network) bool {
	return network.Spec.IPAM.Mode == networkingv1alpha.NetworkIPAMModeAuto &&
		slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv6Protocol)
}

func networkPrefixClaimName(network *networkingv1alpha.Network) string {
	return "network-" + string(network.UID)
}

func (r *NetworkReconciler) prefixFailureMessage(reason allocationFailureReason, err error) string {
	return allocationFailureMessage(reason, allocationRequest{className: r.PrefixClass}, err)
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
		return finalizer.Result{}, nil
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
