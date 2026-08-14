// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	networkInterfaceClaimFinalizer = "networking.datumapis.com/networkinterfaceclaim-release"
	networkInterfaceFinalizer      = "networking.datumapis.com/networkinterface-release"

	// networkContextInUseFinalizer is added at the location, never on the hub.
	// Propagation keeps what a cell-local controller adds, so a hub deletion
	// removes the copy promptly unless an interface here still holds addresses
	// on the network.
	networkContextInUseFinalizer = "networking.datumapis.com/networkcontext-in-use"

	allocationClaimAnnotation = "networking.datumapis.com/allocation-claim"

	ipamScopeRoleNetwork  = "network"
	ipamScopeRoleLocation = "location"

	datumNetworkingAPIGroup = "networking.datumapis.com"

	maxObjectNameLength = 253
	ipClaimNameHashLen  = 12

	// Nothing watches the network, the namespace or IPAM, so a rejected claim
	// needs its own way back.
	rejectedClaimRetryInterval = time.Minute
)

// NetworkInterfaceClaimReconciler binds a NetworkInterfaceClaim to a
// NetworkInterface.
type NetworkInterfaceClaimReconciler struct {
	Location config.LocationConfig
	IPAM     IPAMClientFactory

	mgr         mcmanager.Manager
	localReader client.Reader
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func (r *NetworkInterfaceClaimReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("reconciling network interface claim")
	defer logger.Info("reconcile complete")

	return r.reconcileClaim(ctx, cl.GetClient(), cl.GetEventRecorder("networkinterfaceclaim-controller"), req.NamespacedName)
}

func (r *NetworkInterfaceClaimReconciler) reconcileClaim(
	ctx context.Context,
	cl client.Client,
	recorder events.EventRecorder,
	key client.ObjectKey,
) (ctrl.Result, error) {
	var claim networkingv1alpha.NetworkInterfaceClaim
	if err := cl.Get(ctx, key, &claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !claim.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.release(ctx, cl, &claim)
	}

	if controllerutil.AddFinalizer(&claim, networkInterfaceClaimFinalizer) {
		if err := cl.Update(ctx, &claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed adding finalizer: %w", err)
		}
	}

	return r.fulfill(ctx, cl, recorder, &claim)
}

func (r *NetworkInterfaceClaimReconciler) fulfill(
	ctx context.Context,
	cl client.Client,
	recorder events.EventRecorder,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) (ctrl.Result, error) {
	location, err := r.location(ctx)
	if err != nil {
		var unresolved *LocationUnresolved
		if errors.As(err, &unresolved) {
			return r.reject(ctx, cl, claim, unresolved.Reason, unresolved.Message)
		}
		return ctrl.Result{}, err
	}

	routing, err := r.resolveProject(ctx, cl, claim.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			return r.reject(ctx, cl, claim, "ProjectUnresolved", unresolvable.Error())
		}
		return ctrl.Result{}, err
	}

	contextKey := client.ObjectKey{
		Namespace: claim.Namespace,
		Name:      networkContextName(claim.Spec.Network.Name, location),
	}

	var networkContext networkingv1alpha.NetworkContext
	if err := cl.Get(ctx, contextKey, &networkContext); err != nil {
		if apierrors.IsNotFound(err) {
			return r.reject(ctx, cl, claim,
				networkingv1alpha.NetworkInterfaceClaimReasonNetworkNotAvailableInLocation,
				fmt.Sprintf("Network %q has not reached location %q: no network context %q exists in namespace %q",
					claim.Spec.Network.Name, location.Name, contextKey.Name, contextKey.Namespace))
		}
		return ctrl.Result{}, fmt.Errorf("failed fetching network context: %w", err)
	}

	// A context that states no families is one written before they were carried.
	// Defaulting here would attach the interface to rules nobody declared.
	if len(networkContext.Spec.IPFamilies) == 0 {
		return r.reject(ctx, cl, claim,
			networkingv1alpha.NetworkInterfaceClaimReasonAddressFamiliesUnknown,
			fmt.Sprintf("Network context %q states no address families, so what network %q carries in location %q is unknown here",
				contextKey.Name, claim.Spec.Network.Name, location.Name))
	}

	for _, family := range claim.Spec.IPFamilies {
		if !slices.Contains(networkContext.Spec.IPFamilies, family) {
			return r.reject(ctx, cl, claim,
				networkingv1alpha.NetworkInterfaceClaimReasonAddressFamilyNotCarried,
				fmt.Sprintf("Network %q in location %q does not carry address family %s",
					claim.Spec.Network.Name, location.Name, family))
		}
	}

	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed building IPAM client: %w", err)
	}

	iface, err := r.bindInterface(ctx, cl, ipamClient, routing, claim, &networkContext, location)
	if err != nil {
		var failure *allocationFailure
		if errors.As(err, &failure) {
			return r.reject(ctx, cl, claim, string(failure.reason), failure.message)
		}
		var refused *bindingRefused
		if errors.As(err, &refused) {
			return r.reject(ctx, cl, claim, refused.reason, refused.message)
		}
		return ctrl.Result{}, err
	}

	if err := r.syncNetworkContextHold(ctx, cl, claim.Namespace, claim.Spec.Network.Name, location); err != nil {
		return ctrl.Result{}, err
	}

	allocations, err := r.checkAllocations(ctx, ipamClient, recorder, routing, claim, iface)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.syncInterface(ctx, cl, iface, claim, &networkContext); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.publishInterfaceStatus(ctx, cl, iface, networkContext.Name, allocations); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.publishClaimStatus(ctx, cl, claim, iface, allocations)
}

type projectRouting struct {
	project          string
	projectNamespace string

	// clusterNameLabel is the namespace's own label value, copied rather than
	// re-encoded so anything derived from it is selected by the same propagation
	// policy that already carries the namespace.
	clusterNameLabel string
}

func (r *NetworkInterfaceClaimReconciler) resolveProject(
	ctx context.Context,
	cl client.Client,
	namespaceName string,
) (projectRouting, error) {
	return resolveProjectRouting(ctx, cl, namespaceName)
}

func resolveProjectRouting(
	ctx context.Context,
	cl client.Client,
	namespaceName string,
) (projectRouting, error) {
	var namespace corev1.Namespace
	if err := cl.Get(ctx, client.ObjectKey{Name: namespaceName}, &namespace); err != nil {
		return projectRouting{}, fmt.Errorf("failed reading namespace %q: %w", namespaceName, err)
	}

	project, err := projectFromNamespace(&namespace)
	if err != nil {
		return projectRouting{}, &projectUnresolvable{message: err.Error()}
	}

	projectNamespace, err := projectNamespaceFromNamespace(&namespace)
	if err != nil {
		return projectRouting{}, &projectUnresolvable{message: err.Error()}
	}

	return projectRouting{
		project:          project,
		projectNamespace: projectNamespace,
		clusterNameLabel: namespace.Labels[downstreamclient.UpstreamOwnerClusterNameLabel],
	}, nil
}

// projectUnresolvable means the namespace does not name a project. A failure to
// read the namespace is an ordinary error, and retrying fixes it.
type projectUnresolvable struct {
	message string
}

func (e *projectUnresolvable) Error() string { return e.message }

// syncNetworkContextHold keeps the propagated context alive while an interface
// here still holds addresses on the network. A retained interface outlives the
// consumer that used it, and would have nothing to re-bind against if the
// context were torn down under it.
func (r *NetworkInterfaceClaimReconciler) syncNetworkContextHold(
	ctx context.Context,
	cl client.Client,
	namespace string,
	network string,
	location networkingv1alpha.LocationReference,
) error {
	return reconcileNetworkContextHold(ctx, cl, client.ObjectKey{
		Namespace: namespace,
		Name:      networkContextName(network, location),
	})
}

// reconcileNetworkContextHold is keyed on the context rather than on whatever
// released the last interface. The hold is evaluated while that interface is
// still terminating, so the answer it produces is stale by the time the object
// is gone, and nothing else would ever look again.
func reconcileNetworkContextHold(ctx context.Context, cl client.Client, key client.ObjectKey) error {
	var networkContext networkingv1alpha.NetworkContext
	if err := cl.Get(ctx, key, &networkContext); err != nil {
		return client.IgnoreNotFound(err)
	}

	held, err := networkCarriesInterfaces(ctx, cl, key.Namespace, networkContext.Spec.Network.Name)
	if err != nil {
		return err
	}

	changed := false
	switch {
	case held && networkContext.DeletionTimestamp.IsZero():
		changed = controllerutil.AddFinalizer(&networkContext, networkContextInUseFinalizer)
	case !held:
		changed = controllerutil.RemoveFinalizer(&networkContext, networkContextInUseFinalizer)
	}
	if !changed {
		return nil
	}

	if err := cl.Update(ctx, &networkContext); err != nil {
		return fmt.Errorf("failed updating network context %q: %w", key.Name, err)
	}
	return nil
}

func networkCarriesInterfaces(
	ctx context.Context,
	cl client.Client,
	namespace string,
	network string,
) (bool, error) {
	var interfaces networkingv1alpha.NetworkInterfaceList
	if err := cl.List(ctx, &interfaces, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("failed listing network interfaces: %w", err)
	}

	for i := range interfaces.Items {
		iface := &interfaces.Items[i]
		if iface.Spec.Network.Name == network && iface.DeletionTimestamp.IsZero() {
			return true, nil
		}
	}
	return false, nil
}

func claimsOnNetworkContext(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	networkContext, ok := obj.(*networkingv1alpha.NetworkContext)
	if !ok || networkContext.Spec.Network.Name == "" {
		return nil
	}

	var claims networkingv1alpha.NetworkInterfaceClaimList
	if err := cl.List(ctx, &claims,
		client.InNamespace(networkContext.Namespace),
		client.MatchingFields{networkInterfaceClaimNetworkIndex: networkContext.Spec.Network.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed listing claims for a network context event",
			"networkcontext", networkContext.Name)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(claims.Items))
	for i := range claims.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&claims.Items[i]),
		})
	}
	return requests
}

func (r *NetworkInterfaceClaimReconciler) location(
	ctx context.Context,
) (networkingv1alpha.LocationReference, error) {
	identity, err := ResolveLocationIdentity(ctx, r.localReader, r.Location)
	reportLocationIdentity(identity, err)
	if err != nil {
		return networkingv1alpha.LocationReference{}, err
	}
	if identity.Mismatch {
		log.FromContext(ctx).Info(
			"the delivered serving location disagrees with the configured one; using the delivered copy",
			"delivered", identity.Reference.Name,
			"configured", r.Location.Name)
	}
	return identity.Reference, nil
}

type bindingRefused struct {
	reason  string
	message string
}

func (e *bindingRefused) Error() string { return e.message }

func interfaceSatisfies(
	iface *networkingv1alpha.NetworkInterface,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) error {
	// The addresses belong to the network they were allocated from. Publishing
	// them under another network would hand one network's addresses to another.
	if iface.Spec.Network.Name != claim.Spec.Network.Name {
		return &bindingRefused{
			reason: "NetworkMismatch",
			message: fmt.Sprintf(
				"Network interface %q holds addresses on network %q and cannot be bound by a claim naming network %q",
				iface.Name, iface.Spec.Network.Name, claim.Spec.Network.Name),
		}
	}

	if iface.Spec.InterfaceName != claim.Spec.InterfaceName {
		return &bindingRefused{
			reason: "InterfaceNameMismatch",
			message: fmt.Sprintf(
				"Network interface %q presents as %q to the guest and cannot be bound by a claim asking for %q",
				iface.Name, iface.Spec.InterfaceName, claim.Spec.InterfaceName),
		}
	}

	for _, family := range claim.Spec.IPFamilies {
		if !slices.ContainsFunc(iface.Spec.Addresses, func(a networkingv1alpha.NetworkInterfaceAddress) bool {
			return a.Family == family
		}) {
			return &bindingRefused{
				reason: "AddressFamilyMissing",
				message: fmt.Sprintf(
					"Network interface %q holds no %s address, which this claim requires",
					iface.Name, family),
			}
		}
	}

	for _, request := range claim.Spec.Addresses {
		if !slices.ContainsFunc(iface.Spec.ExternalAddresses, func(a networkingv1alpha.NetworkInterfaceExternalAddress) bool {
			return a.Class == request.Class
		}) {
			return &bindingRefused{
				reason: "AddressClassMissing",
				message: fmt.Sprintf(
					"Network interface %q holds no address of class %q, which this claim requires",
					iface.Name, request.Class),
			}
		}
	}

	// An address keeps the reclaim policy it was allocated under, so a claim
	// asking for a different one cannot be honoured.
	if iface.Spec.ReclaimPolicy != claim.Spec.ReclaimPolicy {
		return &bindingRefused{
			reason: "ReclaimPolicyMismatch",
			message: fmt.Sprintf(
				"Network interface %q holds addresses allocated with reclaimPolicy %s and cannot be bound by a claim requesting %s",
				iface.Name, iface.Spec.ReclaimPolicy, claim.Spec.ReclaimPolicy),
		}
	}

	return nil
}

func (r *NetworkInterfaceClaimReconciler) bindInterface(
	ctx context.Context,
	cl client.Client,
	ipamClient client.Client,
	routing projectRouting,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	networkContext *networkingv1alpha.NetworkContext,
	location networkingv1alpha.LocationReference,
) (*networkingv1alpha.NetworkInterface, error) {
	interfaceKey := client.ObjectKey{Namespace: claim.Namespace, Name: interfaceNameForClaim(claim)}

	var existing networkingv1alpha.NetworkInterface
	err := cl.Get(ctx, interfaceKey, &existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed fetching network interface: %w", err)
	}

	if err == nil {
		if ref := existing.Spec.ClaimRef; ref != nil && ref.Name != claim.Name {
			return nil, &bindingRefused{
				reason: "InterfaceHeldByAnotherClaim",
				message: fmt.Sprintf(
					"Network interface %q is held by claim %q", existing.Name, ref.Name),
			}
		}

		if err := interfaceSatisfies(&existing, claim); err != nil {
			return nil, err
		}

		existing.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: claim.Name}
		controllerutil.AddFinalizer(&existing, networkInterfaceFinalizer)
		if err := cl.Update(ctx, &existing); err != nil {
			return nil, fmt.Errorf("failed binding network interface: %w", err)
		}
		return &existing, nil
	}

	requests, err := r.allocationRequests(ctx, ipamClient, claim)
	if err != nil {
		return nil, err
	}

	allocated, err := r.allocate(ctx, ipamClient, routing, claim, networkContext, requests, location)
	if err != nil {
		return nil, err
	}

	iface := &networkingv1alpha.NetworkInterface{}
	iface.Namespace = interfaceKey.Namespace
	iface.Name = interfaceKey.Name
	iface.Annotations = map[string]string{allocationClaimAnnotation: claim.Name}
	iface.Finalizers = []string{networkInterfaceFinalizer}
	iface.Spec = networkingv1alpha.NetworkInterfaceSpec{
		Network:       networkingv1alpha.LocalNetworkRef{Name: networkContext.Spec.Network.Name},
		ClaimRef:      &networkingv1alpha.NetworkInterfaceClaimRef{Name: claim.Name},
		InterfaceName: claim.Spec.InterfaceName,
		MTU:           networkContext.Spec.MTU,
		ReclaimPolicy: claim.Spec.ReclaimPolicy,
	}

	for _, entry := range allocated {
		if entry.request.external {
			iface.Spec.ExternalAddresses = append(iface.Spec.ExternalAddresses, networkingv1alpha.NetworkInterfaceExternalAddress{
				Family:  entry.request.family,
				Address: entry.bareAddress(),
				Class:   entry.request.className,
			})
			continue
		}

		iface.Spec.Addresses = append(iface.Spec.Addresses, networkingv1alpha.NetworkInterfaceAddress{
			Family:  entry.request.family,
			Address: entry.cidr,
			Primary: entry.request.family == claim.Spec.IPFamilies[0],
			Class:   entry.request.className,
		})
	}

	if err := cl.Create(ctx, iface); err != nil {
		return nil, fmt.Errorf("failed creating network interface: %w", err)
	}

	return iface, nil
}

type allocationRequest struct {
	discriminator string
	family        networkingv1alpha.IPFamily
	className     string
	external      bool
}

func (a allocationRequest) describe() string {
	if a.className != "" {
		return fmt.Sprintf("an address of class %q", a.className)
	}
	return fmt.Sprintf("an %s address", a.family)
}

type allocatedAddress struct {
	request allocationRequest
	cidr    string
}

func (a allocatedAddress) bareAddress() string {
	prefix, err := netip.ParsePrefix(a.cidr)
	if err != nil {
		return a.cidr
	}
	if prefix.Bits() != prefix.Addr().BitLen() {
		return a.cidr
	}
	return prefix.Addr().String()
}

func (r *NetworkInterfaceClaimReconciler) allocationRequests(
	ctx context.Context,
	ipamClient client.Client,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) ([]allocationRequest, error) {
	requests := make([]allocationRequest, 0, len(claim.Spec.IPFamilies)+len(claim.Spec.Addresses))

	for _, family := range claim.Spec.IPFamilies {
		requests = append(requests, allocationRequest{
			discriminator: familyDiscriminator(family),
			family:        family,
		})
	}

	for _, address := range claim.Spec.Addresses {
		var class ipamv1alpha1.IPClass
		if err := ipamClient.Get(ctx, client.ObjectKey{Name: address.Class}, &class); err != nil {
			return nil, &allocationFailure{
				reason: allocationFailureRejected,
				message: fmt.Sprintf("Address class %q could not be read: %v",
					address.Class, err),
			}
		}

		family := networkingv1alpha.IPFamily(class.Spec.IPFamily)
		if !slices.Contains(claim.Spec.IPFamilies, family) {
			return nil, &allocationFailure{
				reason: allocationFailureRejected,
				message: fmt.Sprintf(
					"Address class %q hands out %s addresses, which map onto an %s address this interface does not carry",
					address.Class, family, family),
			}
		}

		requests = append(requests, allocationRequest{
			discriminator: classDiscriminator(address.Class),
			family:        family,
			className:     address.Class,
			external:      true,
		})
	}

	return requests, nil
}

type allocationFailure struct {
	reason  allocationFailureReason
	message string
}

func (e *allocationFailure) Error() string { return e.message }

// allocate claims every requested address, or none. Names are derived from the
// claim, so a failed rollback leaves addresses the next attempt finds again.
func (r *NetworkInterfaceClaimReconciler) allocate(
	ctx context.Context,
	ipamClient client.Client,
	routing projectRouting,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	networkContext *networkingv1alpha.NetworkContext,
	requests []allocationRequest,
	location networkingv1alpha.LocationReference,
) ([]allocatedAddress, error) {
	logger := log.FromContext(ctx)

	if err := ensureProjectNamespace(ctx, ipamClient, routing.projectNamespace); err != nil {
		return nil, fmt.Errorf("failed ensuring project namespace %q: %w", routing.projectNamespace, err)
	}

	allocated := make([]allocatedAddress, 0, len(requests))
	created := make([]*ipamv1alpha1.IPClaim, 0, len(requests))

	rollback := func() {
		var leaked []string
		for _, ipClaim := range created {
			if err := ipamClient.Delete(ctx, ipClaim); err != nil && !apierrors.IsNotFound(err) {
				leaked = append(leaked, ipClaim.Name)
				logger.Error(err, "failed releasing address after a partial allocation",
					"ipclaim", ipClaim.Name, "project", routing.project)
			}
		}
		if len(leaked) > 0 {
			logger.Info("addresses remain claimed after a failed rollback and will be reused on retry",
				"ipclaims", strings.Join(leaked, ","), "project", routing.project)
		}
	}

	for _, request := range requests {
		ipClaim := &ipamv1alpha1.IPClaim{}
		ipClaim.Namespace = routing.projectNamespace
		ipClaim.Name = ipClaimName(claim.Name, request.discriminator)
		ipClaim.Spec = ipamv1alpha1.IPClaimSpec{
			ClassName:     request.className,
			ReclaimPolicy: ipamReclaimPolicy(claim.Spec.ReclaimPolicy),
			Scope: map[string]ipamv1alpha1.ScopeRef{
				ipamScopeRoleNetwork: {
					APIGroup: datumNetworkingAPIGroup,
					Kind:     "Network",
					Name:     networkContext.Spec.Network.Name,
				},
				ipamScopeRoleLocation: {
					APIGroup: datumNetworkingAPIGroup,
					Kind:     "Location",
					Name:     location.Name,
				},
			},
		}
		if request.className == "" {
			ipClaim.Spec.IPFamily = ipamv1alpha1.IPFamily(request.family)
		}

		// Ask whether the address exists rather than reading the refusal. This
		// holds against any IPAM version, whatever that version reports for a
		// duplicate, which matters while the operator can run ahead of the
		// server it talks to. The cost is one read per address per reconcile.
		existing := &ipamv1alpha1.IPClaim{}
		getErr := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), existing)
		if getErr != nil && !apierrors.IsNotFound(getErr) {
			rollback()
			return nil, fmt.Errorf("failed reading IPClaim %q: %w", ipClaim.Name, getErr)
		}

		if getErr == nil {
			ipClaim = existing
		} else if createErr := ipamClient.Create(ctx, ipClaim); createErr != nil {
			// The create can still lose a race with another writer, so ask
			// again before calling this a failure to allocate.
			raced := &ipamv1alpha1.IPClaim{}
			if err := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), raced); err != nil {
				rollback()
				reason := classifyAllocationFailure(createErr)
				return nil, &allocationFailure{
					reason:  reason,
					message: allocationFailureMessage(reason, request, createErr),
				}
			}
			ipClaim = raced
		} else {
			created = append(created, ipClaim)
		}

		if ipClaim.Status.AllocatedCIDR == "" {
			rollback()
			return nil, &allocationFailure{
				reason: allocationFailureUnknown,
				message: fmt.Sprintf("IPAM reported no address for %s (phase %q)",
					request.describe(), ipClaim.Status.Phase),
			}
		}

		allocated = append(allocated, allocatedAddress{
			request: request,
			cidr:    ipClaim.Status.AllocatedCIDR,
		})
	}

	return allocated, nil
}

func (r *NetworkInterfaceClaimReconciler) checkAllocations(
	ctx context.Context,
	ipamClient client.Client,
	recorder events.EventRecorder,
	routing projectRouting,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	iface *networkingv1alpha.NetworkInterface,
) (allocationHealth, error) {
	owner := allocationClaimName(iface)
	health := allocationHealth{intact: true}

	var errs []error
	for _, entry := range allocationEntries(iface) {
		var ipClaim ipamv1alpha1.IPClaim
		key := client.ObjectKey{
			Namespace: routing.projectNamespace,
			Name:      ipClaimName(owner, entry.discriminator),
		}
		if err := ipamClient.Get(ctx, key, &ipClaim); err != nil {
			if apierrors.IsNotFound(err) {
				health = allocationHealth{
					message: fmt.Sprintf("Address %s is held by no allocation in project %q",
						entry.address, routing.project),
				}
				r.reportMissingAllocation(ctx, recorder, routing, claim, iface, entry, key.Name, "")
				continue
			}
			errs = append(errs, fmt.Errorf("failed reading IPClaim %q: %w", key.Name, err))
			continue
		}

		if !entry.holds(&ipClaim) {
			health = allocationHealth{
				message: fmt.Sprintf("Address %s is published, but allocation %q holds %s",
					entry.address, key.Name, ipClaim.Status.AllocatedCIDR),
			}
			r.reportMissingAllocation(ctx, recorder, routing, claim, iface, entry, key.Name,
				ipClaim.Status.AllocatedCIDR)
		}
	}

	return health, errors.Join(errs...)
}

// allocationClaimName is the claim whose name the addresses were allocated
// under, which is not always the claim holding the interface now.
func allocationClaimName(iface *networkingv1alpha.NetworkInterface) string {
	if recorded := iface.Annotations[allocationClaimAnnotation]; recorded != "" {
		return recorded
	}
	if ref := iface.Spec.ClaimRef; ref != nil {
		return ref.Name
	}
	return iface.Name
}

// reportMissingAllocation warns that an address may be handed to another claim.
// Reallocating instead would change the address of a running workload.
func (r *NetworkInterfaceClaimReconciler) reportMissingAllocation(
	ctx context.Context,
	recorder events.EventRecorder,
	routing projectRouting,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	iface *networkingv1alpha.NetworkInterface,
	entry allocationEntry,
	ipClaimName string,
	heldInstead string,
) {
	missingAllocationsTotal.WithLabelValues(routing.project).Inc()

	log.FromContext(ctx).Error(errNoAllocationBehindAddress,
		"network interface advertises an address with no allocation behind it",
		"interface", iface.Name, "address", entry.address, "heldInstead", heldInstead,
		"ipclaim", ipClaimName, "project", routing.project)

	if recorder == nil {
		return
	}

	if heldInstead != "" {
		recorder.Eventf(claim, iface, corev1.EventTypeWarning, "AddressAllocationMissing", "VerifyAllocation",
			"Address %s on network interface %q is published, but allocation %q in project %q holds %s. "+
				"The published address belongs to no one and may be given to another claim.",
			entry.address, iface.Name, ipClaimName, routing.project, heldInstead)
		return
	}

	recorder.Eventf(claim, iface, corev1.EventTypeWarning, "AddressAllocationMissing", "VerifyAllocation",
		"Address %s on network interface %q has no allocation in project %q (IPClaim %q is gone). "+
			"IPAM considers the address free and may hand it to another claim.",
		entry.address, iface.Name, routing.project, ipClaimName)
}

var errNoAllocationBehindAddress = errors.New("no IPClaim holds this address")

type allocationEntry struct {
	discriminator string
	address       string
	external      bool
}

// holds reports whether an allocation still carries the address the interface
// publishes. An IPClaim recreated under the same name may hold a different one.
func (e allocationEntry) holds(ipClaim *ipamv1alpha1.IPClaim) bool {
	allocated := allocatedAddress{cidr: ipClaim.Status.AllocatedCIDR}
	if e.external {
		return allocated.bareAddress() == e.address
	}
	return ipClaim.Status.AllocatedCIDR == e.address
}

func allocationEntries(iface *networkingv1alpha.NetworkInterface) []allocationEntry {
	entries := make([]allocationEntry, 0, len(iface.Spec.Addresses)+len(iface.Spec.ExternalAddresses))
	for _, address := range iface.Spec.Addresses {
		entries = append(entries, allocationEntry{
			discriminator: familyDiscriminator(address.Family),
			address:       address.Address,
		})
	}
	for _, address := range iface.Spec.ExternalAddresses {
		entries = append(entries, allocationEntry{
			discriminator: classDiscriminator(address.Class),
			address:       address.Address,
			external:      true,
		})
	}
	return entries
}

func allocationDiscriminators(iface *networkingv1alpha.NetworkInterface) []string {
	entries := allocationEntries(iface)
	discriminators := make([]string, 0, len(entries))
	for _, entry := range entries {
		discriminators = append(discriminators, entry.discriminator)
	}
	return discriminators
}

func (r *NetworkInterfaceClaimReconciler) publishInterfaceStatus(
	ctx context.Context,
	cl client.Client,
	iface *networkingv1alpha.NetworkInterface,
	networkContextName string,
	allocations allocationHealth,
) error {
	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseBound
	if networkContextName != "" {
		iface.Status.NetworkContextRef = &networkingv1alpha.LocalNetworkContextRef{Name: networkContextName}
	}

	apimeta.SetStatusCondition(&iface.Status.Conditions, allocatedCondition(
		networkingv1alpha.NetworkInterfaceAllocated, iface.Generation, allocations))

	if apimeta.FindStatusCondition(iface.Status.Conditions, networkingv1alpha.NetworkInterfaceProgrammed) == nil {
		apimeta.SetStatusCondition(&iface.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.NetworkInterfaceProgrammed,
			Status:             metav1.ConditionUnknown,
			Reason:             "Pending",
			ObservedGeneration: iface.Generation,
			Message:            "Waiting for the data plane to report the attachment",
		})
	}

	if err := cl.Status().Update(ctx, iface); err != nil {
		return fmt.Errorf("failed updating network interface status: %w", err)
	}
	return nil
}

func (r *NetworkInterfaceClaimReconciler) publishClaimStatus(
	ctx context.Context,
	cl client.Client,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	iface *networkingv1alpha.NetworkInterface,
	allocations allocationHealth,
) error {
	claim.Status.Addresses = append([]networkingv1alpha.NetworkInterfaceAddress(nil), iface.Spec.Addresses...)
	claim.Status.NetworkInterfaceRef = &networkingv1alpha.LocalNetworkInterfaceRef{Name: iface.Name}
	claim.Status.ExternalAddresses = append([]networkingv1alpha.NetworkInterfaceExternalAddress(nil), iface.Spec.ExternalAddresses...)

	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkInterfaceClaimBound,
		Status:             metav1.ConditionTrue,
		Reason:             "Bound",
		ObservedGeneration: claim.Generation,
		Message:            fmt.Sprintf("Bound to network interface %q", iface.Name),
	})
	apimeta.SetStatusCondition(&claim.Status.Conditions, allocatedCondition(
		networkingv1alpha.NetworkInterfaceClaimAllocated, claim.Generation, allocations))
	seedProgrammed(&claim.Status.Conditions, claim.Generation)
	setReady(&claim.Status.Conditions, claim.Generation)

	if err := cl.Status().Update(ctx, claim); err != nil {
		return fmt.Errorf("failed updating claim status: %w", err)
	}
	return nil
}

// syncGateways writes each address's gateway onto the interface once the
// location has a subnet. A provider configures a NIC from the interface alone,
// so the gateway has to live there and not only on the claim's status copy.
func (r *NetworkInterfaceClaimReconciler) syncInterface(
	ctx context.Context,
	cl client.Client,
	iface *networkingv1alpha.NetworkInterface,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	changed := false

	// MTU follows the network, and the primary address follows the claim's
	// first family. Both are derived, and an adopted interface carries whatever
	// its previous claim left behind.
	if iface.Spec.MTU != networkContext.Spec.MTU {
		iface.Spec.MTU = networkContext.Spec.MTU
		changed = true
	}
	for i := range iface.Spec.Addresses {
		primary := iface.Spec.Addresses[i].Family == claim.Spec.IPFamilies[0]
		if iface.Spec.Addresses[i].Primary != primary {
			iface.Spec.Addresses[i].Primary = primary
			changed = true
		}
	}

	if err := r.applyGateways(ctx, cl, iface, networkContext.Name, &changed); err != nil {
		return err
	}

	if !changed {
		return nil
	}

	if err := cl.Update(ctx, iface); err != nil {
		return fmt.Errorf("failed updating network interface: %w", err)
	}
	return nil
}

func (r *NetworkInterfaceClaimReconciler) applyGateways(
	ctx context.Context,
	cl client.Client,
	iface *networkingv1alpha.NetworkInterface,
	networkContextName string,
	changed *bool,
) error {
	if networkContextName == "" {
		return nil
	}

	var subnets networkingv1alpha.SubnetList
	if err := cl.List(ctx, &subnets, client.InNamespace(iface.Namespace)); err != nil {
		return fmt.Errorf("failed listing subnets: %w", err)
	}

	for i := range iface.Spec.Addresses {
		gateway := subnetGatewayFor(&subnets, networkContextName, iface.Spec.Addresses[i].Family)
		if gateway == "" || iface.Spec.Addresses[i].Gateway == gateway {
			continue
		}
		iface.Spec.Addresses[i].Gateway = gateway
		*changed = true
	}
	return nil
}

func subnetGatewayFor(
	subnets *networkingv1alpha.SubnetList,
	networkContextName string,
	family networkingv1alpha.IPFamily,
) string {
	for i := range subnets.Items {
		subnet := &subnets.Items[i]
		if subnet.Spec.NetworkContext.Name == networkContextName && subnet.Spec.IPFamily == family {
			return subnetGateway(subnet)
		}
	}
	return ""
}

func subnetGateway(subnet *networkingv1alpha.Subnet) string {
	start := subnet.Spec.StartAddress
	if subnet.Status.StartAddress != nil {
		start = *subnet.Status.StartAddress
	}

	addr, err := netip.ParseAddr(start)
	if err != nil {
		return ""
	}
	return addr.Next().String()
}

func (r *NetworkInterfaceClaimReconciler) reject(
	ctx context.Context,
	cl client.Client,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	reason string,
	message string,
) (ctrl.Result, error) {
	log.FromContext(ctx).Info("claim cannot be fulfilled", "reason", reason, "message", message)

	demoted := []string{networkingv1alpha.NetworkInterfaceClaimReady}

	// A claim that already holds an interface still holds it, and still holds
	// the addresses IPAM allocated. Only Ready is false.
	if claim.Status.NetworkInterfaceRef == nil {
		demoted = append(demoted,
			networkingv1alpha.NetworkInterfaceClaimBound,
			networkingv1alpha.NetworkInterfaceClaimAllocated)
	}

	for _, conditionType := range demoted {
		apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			ObservedGeneration: claim.Generation,
			Message:            message,
		})
	}
	seedProgrammed(&claim.Status.Conditions, claim.Generation)

	if err := cl.Status().Update(ctx, claim); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed updating claim status: %w", err)
	}

	// Nothing watches the network, the namespace or IPAM, so a claim rejected
	// for a condition that later clears has no other way back.
	return ctrl.Result{RequeueAfter: rejectedClaimRetryInterval}, nil
}

// allocationHealth reports whether every address the interface publishes is
// still backed by an allocation.
type allocationHealth struct {
	intact  bool
	message string
}

func allocatedCondition(conditionType string, generation int64, health allocationHealth) metav1.Condition {
	if health.intact {
		return metav1.Condition{
			Type:               conditionType,
			Status:             metav1.ConditionTrue,
			Reason:             "Allocated",
			ObservedGeneration: generation,
			Message:            "Every requested address is held",
		}
	}
	return metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionFalse,
		Reason:             "AddressAllocationMissing",
		ObservedGeneration: generation,
		Message:            health.message,
	}
}

// setReady derives Ready from the conditions it depends on, so it becomes true
// on its own once the data plane reports the attachment.
func setReady(conditions *[]metav1.Condition, generation int64) {
	unmet := ""
	for _, conditionType := range []string{
		networkingv1alpha.NetworkInterfaceClaimBound,
		networkingv1alpha.NetworkInterfaceClaimAllocated,
		networkingv1alpha.NetworkInterfaceClaimProgrammed,
	} {
		if !apimeta.IsStatusConditionTrue(*conditions, conditionType) {
			unmet = conditionType
			break
		}
	}

	if unmet == "" {
		apimeta.SetStatusCondition(conditions, metav1.Condition{
			Type:               networkingv1alpha.NetworkInterfaceClaimReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			ObservedGeneration: generation,
			Message:            "The interface is bound, addressed, and programmed",
		})
		return
	}

	status := metav1.ConditionFalse
	if apimeta.IsStatusConditionPresentAndEqual(*conditions, unmet, metav1.ConditionUnknown) {
		status = metav1.ConditionUnknown
	}
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkInterfaceClaimReady,
		Status:             status,
		Reason:             "Not" + unmet,
		ObservedGeneration: generation,
		Message:            fmt.Sprintf("Waiting for %s", unmet),
	})
}

// seedProgrammed sets Programmed only when it is absent. The data plane owns
// this condition; overwriting it would revert whoever reported the attachment.
func seedProgrammed(conditions *[]metav1.Condition, generation int64) {
	if apimeta.FindStatusCondition(*conditions, networkingv1alpha.NetworkInterfaceClaimProgrammed) != nil {
		return
	}
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkInterfaceClaimProgrammed,
		Status:             metav1.ConditionUnknown,
		Reason:             "Pending",
		ObservedGeneration: generation,
		Message:            "Waiting for the data plane to report the attachment",
	})
}

// release returns the addresses to their pools, or under Retain leaves the
// interface holding them for the next claim of this name.
func (r *NetworkInterfaceClaimReconciler) release(
	ctx context.Context,
	cl client.Client,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) error {
	if !controllerutil.ContainsFinalizer(claim, networkInterfaceClaimFinalizer) {
		return nil
	}

	var iface networkingv1alpha.NetworkInterface
	interfaceKey := client.ObjectKey{Namespace: claim.Namespace, Name: interfaceNameForClaim(claim)}
	err := cl.Get(ctx, interfaceKey, &iface)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed fetching network interface: %w", err)
	}

	interfaceExists := err == nil
	held := interfaceExists && iface.Spec.ClaimRef != nil &&
		iface.Spec.ClaimRef.Name == claim.Name
	retain := claim.Spec.ReclaimPolicy == networkingv1alpha.NetworkInterfaceReclaimPolicyRetain

	switch {
	case held && retain:
		iface.Spec.ClaimRef = nil
		if err := cl.Update(ctx, &iface); err != nil {
			return fmt.Errorf("failed unbinding network interface: %w", err)
		}
		if err := markInterfaceAvailable(ctx, cl, &iface); err != nil {
			return err
		}

	case held:
		if err := r.releaseAddresses(ctx, cl, claim.Namespace, &iface); err != nil {
			return err
		}
		if controllerutil.RemoveFinalizer(&iface, networkInterfaceFinalizer) {
			if err := cl.Update(ctx, &iface); err != nil {
				return fmt.Errorf("failed clearing network interface finalizer: %w", err)
			}
		}
		if err := cl.Delete(ctx, &iface); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed deleting network interface: %w", err)
		}

	case interfaceExists && iface.Spec.ClaimRef == nil && retain:
		// A previous attempt unbound the interface and failed before recording
		// it. Nothing else writes phase for an unbound interface.
		if err := markInterfaceAvailable(ctx, cl, &iface); err != nil {
			return err
		}

	case !retain:
		// The interface is gone, or never named this claim. Either way the
		// addresses this claim allocated are named after it.
		if err := r.releaseClaimAllocations(ctx, cl, claim); err != nil {
			return err
		}
	}

	location, err := r.location(ctx)
	if err != nil {
		return err
	}
	if err := r.syncNetworkContextHold(ctx, cl, claim.Namespace, claim.Spec.Network.Name, location); err != nil {
		return err
	}

	controllerutil.RemoveFinalizer(claim, networkInterfaceClaimFinalizer)
	if err := cl.Update(ctx, claim); err != nil {
		return fmt.Errorf("failed removing finalizer: %w", err)
	}
	return nil
}

func (r *NetworkInterfaceClaimReconciler) releaseAddresses(
	ctx context.Context,
	cl client.Client,
	namespace string,
	iface *networkingv1alpha.NetworkInterface,
) error {
	return r.releaseIPClaims(ctx, cl, namespace,
		allocationClaimName(iface), allocationDiscriminators(iface))
}

// releaseClaimAllocations releases the addresses this claim allocated. A claim
// that only adopted an existing interface allocated none and releases none.
func (r *NetworkInterfaceClaimReconciler) releaseClaimAllocations(
	ctx context.Context,
	cl client.Client,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) error {
	// A claim that never bound allocated nothing, so there is nothing to
	// release and no reason to block its deletion on a project it never named.
	if claim.Status.NetworkInterfaceRef == nil {
		return nil
	}

	return r.releaseIPClaims(ctx, cl, claim.Namespace,
		claim.Name, claimDiscriminators(claim))
}

func markInterfaceAvailable(
	ctx context.Context,
	cl client.Client,
	iface *networkingv1alpha.NetworkInterface,
) error {
	if iface.Status.Phase == networkingv1alpha.NetworkInterfacePhaseAvailable {
		return nil
	}
	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseAvailable
	if err := cl.Status().Update(ctx, iface); err != nil {
		return fmt.Errorf("failed updating network interface status: %w", err)
	}
	return nil
}

func (r *NetworkInterfaceClaimReconciler) releaseIPClaims(
	ctx context.Context,
	cl client.Client,
	namespace string,
	owner string,
	discriminators []string,
) error {
	if len(discriminators) == 0 {
		return nil
	}

	routing, err := r.resolveProject(ctx, cl, namespace)
	if err != nil {
		return fmt.Errorf("cannot release addresses claimed by %q: %w", owner, err)
	}

	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return fmt.Errorf("failed building IPAM client: %w", err)
	}

	var errs []error
	for _, discriminator := range discriminators {
		ipClaim := &ipamv1alpha1.IPClaim{}
		ipClaim.Namespace = routing.projectNamespace
		ipClaim.Name = ipClaimName(owner, discriminator)
		if err := ipamClient.Delete(ctx, ipClaim); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed releasing %q: %w", ipClaim.Name, err))
		}
	}
	return errors.Join(errs...)
}

func claimDiscriminators(claim *networkingv1alpha.NetworkInterfaceClaim) []string {
	discriminators := make([]string, 0, len(claim.Spec.IPFamilies)+len(claim.Spec.Addresses))
	for _, family := range claim.Spec.IPFamilies {
		discriminators = append(discriminators, familyDiscriminator(family))
	}
	for _, address := range claim.Spec.Addresses {
		discriminators = append(discriminators, classDiscriminator(address.Class))
	}
	return discriminators
}

func interfaceNameForClaim(claim *networkingv1alpha.NetworkInterfaceClaim) string {
	if claim.Spec.NetworkInterfaceName != "" {
		return claim.Spec.NetworkInterfaceName
	}
	return claim.Name
}

func familyDiscriminator(family networkingv1alpha.IPFamily) string {
	return "f-" + strings.ToLower(string(family))
}

func classDiscriminator(class string) string {
	return "c-" + class
}

// ipClaimName derives a stable name from the claim and what it asks for, so a
// replacement instance finds the addresses it already has.
func ipClaimName(claimName, discriminator string) string {
	candidate := claimName + "-" + discriminator
	if len(candidate) <= maxObjectNameLength && len(validation.IsDNS1123Subdomain(candidate)) == 0 {
		return candidate
	}

	sum := sha256.Sum256([]byte(claimName + "\x00" + discriminator))
	suffix := hex.EncodeToString(sum[:])[:ipClaimNameHashLen]

	prefix := claimName
	if max := maxObjectNameLength - 1 - ipClaimNameHashLen; len(prefix) > max {
		prefix = prefix[:max]
	}
	return strings.TrimRight(prefix, "-.") + "-" + suffix
}

func ipamReclaimPolicy(policy networkingv1alpha.NetworkInterfaceReclaimPolicy) ipamv1alpha1.ReclaimPolicy {
	if policy == networkingv1alpha.NetworkInterfaceReclaimPolicyRetain {
		return ipamv1alpha1.ReclaimRetain
	}
	return ipamv1alpha1.ReclaimDelete
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkInterfaceClaimReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.IPAM == nil {
		return errors.New("an IPAM client factory is required")
	}
	r.mgr = mgr
	r.localReader = mgr.GetLocalManager().GetClient()
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterfaceClaim{}, mcbuilder.WithEngageWithLocalCluster(false)).
		// A deleted interface is rebuilt by the claim that holds it.
		Watches(&networkingv1alpha.NetworkInterface{}, mchandler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []ctrl.Request {
				iface, ok := obj.(*networkingv1alpha.NetworkInterface)
				if !ok || iface.Spec.ClaimRef == nil {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKey{
					Namespace: iface.Namespace,
					Name:      iface.Spec.ClaimRef.Name,
				}}}
			})).
		// The context arriving is what makes a claim fulfillable, and a change to
		// it has to reach the interfaces that already exist.
		Watches(&networkingv1alpha.NetworkContext{}, func(clusterName multicluster.ClusterName, cl cluster.Cluster) mchandler.EventHandler {
			return mchandler.ForCluster(handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					return claimsOnNetworkContext(ctx, cl.GetClient(), obj)
				}), clusterName)
		}).
		// A subnet appearing is what makes the gateway resolvable, and no claim
		// event follows it.
		Watches(&networkingv1alpha.Subnet{}, func(clusterName multicluster.ClusterName, cl cluster.Cluster) mchandler.EventHandler {
			return mchandler.ForCluster(handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					var claims networkingv1alpha.NetworkInterfaceClaimList
					if err := cl.GetClient().List(ctx, &claims, client.InNamespace(obj.GetNamespace())); err != nil {
						log.FromContext(ctx).Error(err, "failed listing claims for a subnet event")
						return nil
					}

					requests := make([]reconcile.Request, 0, len(claims.Items))
					for i := range claims.Items {
						requests = append(requests, reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(&claims.Items[i]),
						})
					}
					return requests
				}), clusterName)
		}).
		Named("networkinterfaceclaim").
		Complete(r)
}
