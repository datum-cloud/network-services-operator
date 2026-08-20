// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	networkContextSubnetFinalizer = "networking.datumapis.com/networkcontext-subnet-release"

	// privateSubnetClass is the class of tenant-private space, and the only
	// subnet class the platform issues today.
	privateSubnetClass = "private"
)

// NetworkContextReconciler reconciles a NetworkContext object
type NetworkContextReconciler struct {
	// IPAM is optional. Left nil, no subnet is claimed and a network context is
	// reconciled exactly as it was before the operator reached IPAM at all.
	IPAM IPAMClientFactory

	// SubnetClass is the IPClass that hands out the range a network is
	// addressed from in one location. Empty means the same as a nil IPAM:
	// nothing is claimed.
	SubnetClass string

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *NetworkContextReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var networkContext networkingv1alpha.NetworkContext
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &networkContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("reconciling network context")
	defer logger.Info("reconcile complete")

	return r.reconcileNetworkContext(ctx, cl.GetClient(), &networkContext)
}

func (r *NetworkContextReconciler) reconcileNetworkContext(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (ctrl.Result, error) {
	if !networkContext.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(networkContext, networkContextSubnetFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.releaseSubnet(ctx, cl, networkContext); err != nil {
			var occupied *rangeOccupied
			if errors.As(err, &occupied) {
				return r.reportSubnet(ctx, cl, networkContext,
					networkingv1alpha.NetworkContextReasonRangeOccupied, occupied.message)
			}
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(networkContext, networkContextSubnetFinalizer)
		if err := cl.Update(ctx, networkContext); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed removing subnet finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	result, err := r.reconcileSubnet(ctx, cl, networkContext)
	if err != nil {
		return ctrl.Result{}, err
	}

	return result, r.reportReady(ctx, cl, networkContext)
}

func (r *NetworkContextReconciler) reportReady(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	if !apimeta.IsStatusConditionTrue(networkContext.Status.Conditions, networkingv1alpha.NetworkContextProgrammed) {
		return nil
	}

	if !apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkContextReady,
		Status:             metav1.ConditionTrue,
		Reason:             networkingv1alpha.NetworkContextReadyReasonReady,
		ObservedGeneration: networkContext.Generation,
		Message:            "Network context is ready",
	}) {
		return nil
	}

	if err := cl.Status().Update(ctx, networkContext); err != nil {
		return fmt.Errorf("failed updating network context status: %w", err)
	}
	return nil
}

// reconcileSubnet holds the range this network is addressed from in this
// location. A context is (network, location) and lives exactly as long as the
// subnet does, so it is what owns it: without an owner the subnet is only ever
// brought into being under the first endpoint that lands here, is invisible
// until then, and is never given back.
func (r *NetworkContextReconciler) reconcileSubnet(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (ctrl.Result, error) {
	if r.IPAM == nil || r.SubnetClass == "" || !networkContextCarriesIPv6(networkContext) {
		return ctrl.Result{}, nil
	}

	routing, err := resolveProjectOrCluster(ctx, cl, networkContext.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			return r.reportSubnet(ctx, cl, networkContext,
				networkingv1alpha.NetworkContextReasonProjectUnresolved, unresolvable.Error())
		}
		return ctrl.Result{}, err
	}

	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed building IPAM client: %w", err)
	}

	if controllerutil.AddFinalizer(networkContext, networkContextSubnetFinalizer) {
		if err := cl.Update(ctx, networkContext); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed adding subnet finalizer: %w", err)
		}
	}

	subnet, err := r.claimSubnet(ctx, ipamClient, routing, networkContext)
	if err != nil {
		var refused *bindingRefused
		if errors.As(err, &refused) {
			return r.reportSubnet(ctx, cl, networkContext, refused.reason, refused.message)
		}
		var failure *allocationFailure
		if errors.As(err, &failure) {
			return r.reportSubnet(ctx, cl, networkContext, string(failure.reason), failure.message)
		}
		return ctrl.Result{}, err
	}

	subnetName, err := r.publishSubnetObject(ctx, cl, networkContext, subnet)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.publishSubnet(ctx, cl, networkContext, routing, subnet, subnetName)
}

func (r *NetworkContextReconciler) claimSubnet(
	ctx context.Context,
	ipamClient client.Client,
	routing projectRouting,
	networkContext *networkingv1alpha.NetworkContext,
) (scopeRange, error) {
	return holdScopeRange(ctx, ipamClient, routing, scopeRangeRequest{
		className: r.SubnetClass,
		claimName: networkContextSubnetClaimName(networkContext),
		namespace: routing.projectNamespace,
		scope: map[string]ipamv1alpha1.ScopeRef{
			ipamScopeRoleNetwork: {
				APIGroup: datumNetworkingAPIGroup,
				Kind:     "Network",
				Name:     networkContext.Spec.Network.Name,
			},
			ipamScopeRoleLocation: {
				APIGroup: datumNetworkingAPIGroup,
				Kind:     "Location",
				Name:     networkContext.Spec.Location.Name,
			},
		},
		subject:                 "this location",
		namespaceNotFoundReason: networkingv1alpha.NetworkContextReasonProjectNamespaceNotFound,
		rangeUnsupportedReason:  networkingv1alpha.NetworkContextReasonRangeUnsupported,
	})
}

// publishSubnetObject writes the allocation onto the Subnet this location is
// addressed from. The Subnet is the API a consumer already reads a location's
// addressing from, and the gateway an interface is given is derived from it, so
// the allocation lands there rather than becoming a second range on the context
// that a reader would have to reconcile against it.
func (r *NetworkContextReconciler) publishSubnetObject(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	held scopeRange,
) (string, error) {
	prefix, err := netip.ParsePrefix(held.cidr)
	if err != nil {
		return "", fmt.Errorf("IPAM held %q for this location, which is not a prefix: %w", held.cidr, err)
	}
	prefix = prefix.Masked()

	subnet := &networkingv1alpha.Subnet{}
	subnet.Namespace = networkContext.Namespace
	subnet.Name = networkContextSubnetName(networkContext)

	result, err := controllerutil.CreateOrUpdate(ctx, cl, subnet, func() error {
		subnet.Spec.SubnetClass = privateSubnetClass
		subnet.Spec.IPFamily = networkingv1alpha.IPv6Protocol
		subnet.Spec.NetworkContext = networkingv1alpha.LocalNetworkContextRef{Name: networkContext.Name}
		subnet.Spec.Location = networkContext.Spec.Location
		subnet.Spec.StartAddress = prefix.Addr().String()
		subnet.Spec.PrefixLength = int32(prefix.Bits())
		return controllerutil.SetControllerReference(networkContext, subnet, cl.Scheme())
	})
	if err != nil {
		return "", fmt.Errorf("failed writing subnet %q: %w", subnet.Name, err)
	}
	if result != controllerutil.OperationResultNone {
		log.FromContext(ctx).Info("published the location's subnet",
			"subnet", subnet.Name, "range", held.cidr, "result", result)
	}

	return subnet.Name, nil
}

func (r *NetworkContextReconciler) publishSubnet(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	routing projectRouting,
	held scopeRange,
	subnetName string,
) error {
	allocated := &networkingv1alpha.NetworkContextIPAMStatus{
		IPv6SubnetRef: &networkingv1alpha.LocalSubnetReference{Name: subnetName},
		IPv6ClaimRef: &networkingv1alpha.NetworkPrefixRef{
			Project:   routing.project,
			Namespace: routing.projectNamespace,
			ClaimName: networkContextSubnetClaimName(networkContext),
			PoolName:  held.poolName,
		},
	}

	changed := !equality.Semantic.DeepEqual(networkContext.Status.IPAM, allocated)
	networkContext.Status.IPAM = allocated

	if apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkContextIPAMAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             "Allocated",
		ObservedGeneration: networkContext.Generation,
		Message:            "This location is addressed from " + held.cidr,
	}) {
		changed = true
	}

	if !changed {
		return nil
	}

	if err := cl.Status().Update(ctx, networkContext); err != nil {
		return fmt.Errorf("failed updating network context status: %w", err)
	}
	return nil
}

// reportSubnet says why no subnet was allocated and comes back later. Nothing
// watches IPAM or the namespace, so a condition that clears on its own has no
// other way back.
func (r *NetworkContextReconciler) reportSubnet(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	reason string,
	message string,
) (ctrl.Result, error) {
	log.FromContext(ctx).Info("network context subnet cannot be allocated",
		"reason", reason, "message", message)

	if apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkContextIPAMAllocated,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		ObservedGeneration: networkContext.Generation,
		Message:            message,
	}) {
		if err := cl.Status().Update(ctx, networkContext); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating network context status: %w", err)
		}
	}
	return ctrl.Result{RequeueAfter: rejectedClaimRetryInterval}, nil
}

// releaseSubnet gives back what this operator holds. The recorded reference is
// what it releases against, because a namespace that stopped naming its project
// must not turn into a context that cannot be deleted.
func (r *NetworkContextReconciler) releaseSubnet(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	if r.IPAM == nil {
		return nil
	}

	ref := subnetClaimRef(networkContext)
	if ref == nil {
		routing, err := resolveProjectOrCluster(ctx, cl, networkContext.Namespace)
		if err != nil {
			log.FromContext(ctx).Info("network context holds no recorded subnet and names no project; releasing nothing",
				"error", err.Error())
			return nil
		}
		ref = &networkingv1alpha.NetworkPrefixRef{
			Project:   routing.project,
			Namespace: routing.projectNamespace,
			ClaimName: networkContextSubnetClaimName(networkContext),
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
				"this location's subnet still has addresses allocated inside it: %s", occupied.message)}
		}
		return err
	}
	return nil
}

func subnetClaimRef(networkContext *networkingv1alpha.NetworkContext) *networkingv1alpha.NetworkPrefixRef {
	if networkContext.Status.IPAM == nil {
		return nil
	}
	ref := networkContext.Status.IPAM.IPv6ClaimRef
	if ref == nil || ref.Project == "" || ref.ClaimName == "" {
		return nil
	}
	return ref
}

func networkContextCarriesIPv6(networkContext *networkingv1alpha.NetworkContext) bool {
	return slices.Contains(networkContext.Spec.IPFamilies, networkingv1alpha.IPv6Protocol)
}

func networkContextSubnetName(networkContext *networkingv1alpha.NetworkContext) string {
	return networkContext.Name + "-ipv6"
}

func networkContextSubnetClaimName(networkContext *networkingv1alpha.NetworkContext) string {
	return "networkcontext-" + string(networkContext.UID)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkContextReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkcontext").
		Complete(r)
}
