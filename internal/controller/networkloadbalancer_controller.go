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

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/words"
)

const (
	networkLoadBalancerFinalizer  = "networking.datumapis.com/networkloadbalancer-release"
	networkLoadBalancerOwnerLabel = "networking.datumapis.com/network-load-balancer-uid"
	networkLoadBalancerRetry      = time.Minute
)

type NetworkLoadBalancerReconciler struct {
	IPAM            IPAMClientFactory
	PublicIPv6Class string
	PublicIPv4Class string
	ManagedDomain   string
	mgr             mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=locations.miloapis.com,resources=locations,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *NetworkLoadBalancerReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cluster, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	ctx = mccontext.WithCluster(ctx, req.ClusterName)
	return r.reconcile(ctx, cluster.GetClient(), req.NamespacedName)
}

func (r *NetworkLoadBalancerReconciler) reconcile(ctx context.Context, cl client.Client, key client.ObjectKey) (ctrl.Result, error) {
	var lb networkingv1alpha.NetworkLoadBalancer
	if err := cl.Get(ctx, key, &lb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !lb.DeletionTimestamp.IsZero() {
		return r.release(ctx, cl, &lb)
	}

	if r.IPAM == nil || r.PublicIPv6Class == "" {
		return r.report(ctx, cl, &lb, nil, nil, metav1.ConditionFalse, "AddressServiceUnavailable",
			"Public IPv6 address allocation is not configured")
	}
	if controllerutil.AddFinalizer(&lb, networkLoadBalancerFinalizer) {
		if err := cl.Update(ctx, &lb); err != nil {
			return ctrl.Result{}, fmt.Errorf("add load balancer finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}
	for _, request := range lb.Spec.Exposure.Addresses {
		if slices.Contains(request.Families, networkingv1alpha.IPv4Protocol) && r.PublicIPv4Class == "" {
			return r.report(ctx, cl, &lb, nil, nil, metav1.ConditionFalse, "UnsupportedCapability",
				fmt.Sprintf("address request %q: public IPv4 NLB ingress is unavailable", request.Name))
		}
	}

	var network networkingv1alpha.Network
	if err := cl.Get(ctx, client.ObjectKey{Namespace: lb.Namespace, Name: lb.Spec.NetworkRef.Name}, &network); err != nil {
		if apierrors.IsNotFound(err) {
			return r.report(ctx, cl, &lb, nil, nil, metav1.ConditionFalse, "NetworkNotFound",
				fmt.Sprintf("Network %q was not found", lb.Spec.NetworkRef.Name))
		}
		return ctrl.Result{}, fmt.Errorf("read load balancer network: %w", err)
	}
	if !slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv6Protocol) {
		return r.report(ctx, cl, &lb, nil, nil, metav1.ConditionFalse, "IPv6Required",
			"The referenced network does not carry IPv6")
	}

	routing, err := resolveProjectOrCluster(ctx, cl, lb.Namespace)
	if err != nil {
		var unresolved *projectUnresolvable
		if errors.As(err, &unresolved) {
			return r.report(ctx, cl, &lb, nil, nil, metav1.ConditionFalse, "ProjectUnresolved", err.Error())
		}
		return ctrl.Result{}, err
	}
	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("build project IPAM client: %w", err)
	}

	requests, err := r.addressRequests(ctx, cl, &lb)
	if err != nil {
		return ctrl.Result{}, err
	}
	if countAddressEndpoints(requests, lb.Status.Endpoints) > 1024 {
		return r.report(ctx, cl, &lb, nil, requests, metav1.ConditionFalse, "TooManyEndpoints",
			"Selected and withdrawing address endpoints exceed the status limit of 1024; reduce address requests or locations")
	}

	wanted := make(map[string]bool, len(requests))
	status := make([]networkingv1alpha.NetworkLoadBalancerEndpointStatus, 0, len(requests)+len(lb.Status.Endpoints))
	allAllocated := len(requests) > 0
	var firstFailure string
	for _, request := range requests {
		claimName := loadBalancerClaimName(&lb, request)
		wanted[claimName] = true
		prior := priorAddressStatus(lb.Status.Endpoints, request.requestName, request.location, request.family)
		prior = *prior.DeepCopy()
		address, claimUID, claimErr := r.holdAddress(ctx, ipamClient, routing.projectNamespace, &lb, request, claimName)
		claimRef := &networkingv1alpha.NetworkLoadBalancerIPClaimRef{
			Project: routing.project, Namespace: routing.projectNamespace, Name: claimName, UID: claimUID,
		}
		changed := prior.Value != "" && prior.Value != address ||
			prior.ClaimRef != nil && prior.ClaimRef.UID != "" && claimUID != "" && prior.ClaimRef.UID != claimUID
		if changed && addressPendingWithdrawal(prior) {
			// Keep the old address visible to withdrawal controllers. In particular,
			// do not transfer its announcement or publication to a replacement IP.
			allAllocated = false
			if firstFailure == "" {
				firstFailure = fmt.Sprintf("%s: prior address is awaiting withdrawal", claimName)
			}
			setLoadBalancerCondition(&prior.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAllocated,
				metav1.ConditionFalse, "AddressReplacementPendingWithdrawal", "Prior address must be withdrawn before its replacement can be served")
			appendAddressStatus(&status, lb.Status.Endpoints, request.requestName, request.location, prior)
			continue
		}
		entry := prior
		if changed || prior.Value == "" && address != "" {
			// Programming, announcement, and DNS publication describe one concrete
			// address, not the request that happened to produce it.
			entry = networkingv1alpha.NetworkLoadBalancerAddressStatus{Family: request.family}
		}
		entry.Family = request.family
		entry.ClaimRef = claimRef
		if claimErr != nil {
			allAllocated = false
			entry.Value = ""
			reason := string(classifyAllocationFailure(claimErr))
			setLoadBalancerCondition(&entry.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAllocated,
				metav1.ConditionFalse, reason, claimErr.Error())
			if firstFailure == "" {
				firstFailure = fmt.Sprintf("%s: %s", claimName, claimErr)
			}
		} else if address == "" {
			allAllocated = false
			entry.Value = ""
			setLoadBalancerCondition(&entry.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAllocated,
				metav1.ConditionFalse, "AllocationPending", "IPAM has not assigned an address yet")
		} else {
			entry.Value = address
			setLoadBalancerCondition(&entry.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAllocated,
				metav1.ConditionTrue, "Allocated", "Public address is held in IPAM")
		}
		appendAddressStatus(&status, lb.Status.Endpoints, request.requestName, request.location, entry)
	}

	for _, endpoint := range lb.Status.Endpoints {
		for _, old := range endpoint.Addresses {
			if old.ClaimRef != nil && wanted[old.ClaimRef.Name] {
				continue
			}
			if addressPendingWithdrawal(old) {
				appendAddressStatus(&status, lb.Status.Endpoints, endpoint.RequestName, endpoint.Location, old)
				allAllocated = false
				continue
			}
			if old.ClaimRef != nil {
				if err := r.deleteAddressClaim(ctx, old.ClaimRef, string(lb.UID)); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}

	if err := r.releaseOrphanedClaims(ctx, ipamClient, routing.projectNamespace, &lb, wanted); err != nil {
		return ctrl.Result{}, err
	}
	slices.SortFunc(status, func(a, b networkingv1alpha.NetworkLoadBalancerEndpointStatus) int {
		if a.RequestName != b.RequestName {
			return strings.Compare(a.RequestName, b.RequestName)
		}
		return strings.Compare(a.Location, b.Location)
	})
	for i := range status {
		if status[i].CanonicalHostname == "" && r.ManagedDomain != "" {
			label := words.WordsAndEntropy("", string(lb.UID)+"/"+status[i].RequestName)
			status[i].CanonicalHostname = status[i].Location + "." + label + "." + r.ManagedDomain
		}
	}

	if !allAllocated {
		if firstFailure == "" {
			firstFailure = "One or more requested addresses are pending or awaiting withdrawal"
		}
		return r.report(ctx, cl, &lb, status, requests, metav1.ConditionFalse, "AllocationPending", firstFailure)
	}
	return r.report(ctx, cl, &lb, status, requests, metav1.ConditionTrue, "Allocated", "All requested public addresses are held")
}

func countAddressEndpoints(requests []loadBalancerAddressRequest, prior []networkingv1alpha.NetworkLoadBalancerEndpointStatus) int {
	type endpointKey struct{ requestName, location string }
	selected := make(map[endpointKey]bool, len(requests))
	for _, request := range requests {
		selected[endpointKey{request.requestName, request.location}] = true
	}
	for _, endpoint := range prior {
		key := endpointKey{endpoint.RequestName, endpoint.Location}
		if selected[key] {
			continue
		}
		for _, address := range endpoint.Addresses {
			if addressPendingWithdrawal(address) {
				selected[key] = true
				break
			}
		}
	}
	return len(selected)
}

type loadBalancerAddressRequest struct {
	requestName string
	location    string
	networkName string
	family      networkingv1alpha.IPFamily
}

func (r *NetworkLoadBalancerReconciler) addressRequests(
	ctx context.Context, cl client.Client, lb *networkingv1alpha.NetworkLoadBalancer,
) ([]loadBalancerAddressRequest, error) {
	var available locationsv1alpha1.LocationList
	if err := cl.List(ctx, &available); err != nil {
		return nil, fmt.Errorf("list available locations: %w", err)
	}
	known := map[string]bool{}
	for _, location := range available.Items {
		ready := apimeta.FindStatusCondition(location.Status.Conditions, locationsv1alpha1.LocationConditionReady)
		known[location.Name] = ready != nil && ready.Status == metav1.ConditionTrue && ready.ObservedGeneration == location.Generation
	}

	var requests []loadBalancerAddressRequest
	for _, address := range lb.Spec.Exposure.Addresses {
		var names []string
		source := networkingv1alpha.NetworkLoadBalancerLocationSourceBackends
		if address.Locations != nil {
			source = address.Locations.From
		}
		switch source {
		case networkingv1alpha.NetworkLoadBalancerLocationSourceAll:
			for name, ready := range known {
				if ready {
					names = append(names, name)
				}
			}
		case networkingv1alpha.NetworkLoadBalancerLocationSourceNames:
			// A location projection may temporarily disappear. An explicit
			// request still owns its IP throughout that gap.
			names = append(names, address.Locations.Names...)
		default:
			backendNames, err := backendLocationsForAddress(ctx, cl, lb, address.Name)
			if err != nil {
				return nil, err
			}
			names = backendNames
		}
		slices.Sort(names)
		for _, name := range names {
			for _, family := range address.Families {
				requests = append(requests, loadBalancerAddressRequest{
					requestName: address.Name, location: name, networkName: lb.Spec.NetworkRef.Name, family: family,
				})
			}
		}
	}
	return requests, nil
}

// backendLocationsForAddress selects locations of the effective backends on
// every listener using the address. It uses live, bound membership
// rather than the capped NetworkService status display list.
func backendLocationsForAddress(ctx context.Context, cl client.Client,
	lb *networkingv1alpha.NetworkLoadBalancer, addressName string,
) ([]string, error) {
	var routes networkingv1alpha.NetworkLoadBalancerRouteList
	if err := cl.List(ctx, &routes, client.InNamespace(lb.Namespace)); err != nil {
		return nil, fmt.Errorf("list load balancer routes: %w", err)
	}
	selected := map[string]bool{}
	attached := false
	for _, listener := range lb.Spec.Listeners {
		if !slices.Contains(listener.AddressRefs, addressName) {
			continue
		}
		var winner *networkingv1alpha.NetworkLoadBalancerRoute
		for i := range routes.Items {
			route := &routes.Items[i]
			if !route.DeletionTimestamp.IsZero() || !slices.ContainsFunc(route.Spec.ParentRefs,
				func(ref networkingv1alpha.NetworkLoadBalancerRouteParentRef) bool {
					return routeParentSelectsListener(lb, ref, listener.Name)
				}) {
				continue
			}
			if winner == nil || route.CreationTimestamp.Before(&winner.CreationTimestamp) ||
				(route.CreationTimestamp.Equal(&winner.CreationTimestamp) && route.Name < winner.Name) {
				winner = route
			}
		}
		if winner == nil {
			return nil, nil
		}
		attached = true
		for _, backend := range winner.Spec.Backends {
			var service networkingv1alpha.NetworkService
			key := client.ObjectKey{Namespace: winner.Namespace, Name: backend.NetworkService.Name}
			if backend.NetworkService.Namespace != "" {
				key.Namespace = backend.NetworkService.Namespace
			}
			if err := cl.Get(ctx, key, &service); err != nil {
				if apierrors.IsNotFound(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("get backend NetworkService %s: %w", key, err)
			}
			selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)
			if err != nil {
				return nil, fmt.Errorf("backend NetworkService %s has invalid selector: %w", key, err)
			}
			members, err := matchingInterfaces(ctx, cl, service.Namespace, selector)
			if err != nil {
				return nil, err
			}
			locations := map[string]bool{}
			for i := range members {
				member := &members[i]
				if member.Spec.Network.Name == lb.Spec.NetworkRef.Name {
					if location := member.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]; location != "" {
						locations[location] = true
					}
				}
			}
			for location := range locations {
				selected[location] = true
			}
		}
	}
	if !attached {
		return nil, nil
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func routeParentSelectsListener(lb *networkingv1alpha.NetworkLoadBalancer,
	ref networkingv1alpha.NetworkLoadBalancerRouteParentRef, listenerName string,
) bool {
	if ref.Name != lb.Name || ref.Namespace != "" && ref.Namespace != lb.Namespace {
		return false
	}
	if ref.Listener != "" {
		return ref.Listener == listenerName
	}
	if ref.Port == nil {
		return false
	}
	var matchedName string
	for _, candidate := range lb.Spec.Listeners {
		if candidate.Port != *ref.Port {
			continue
		}
		if matchedName != "" {
			return false
		}
		matchedName = candidate.Name
	}
	return matchedName == listenerName
}

func loadBalancerClaimName(lb *networkingv1alpha.NetworkLoadBalancer, request loadBalancerAddressRequest) string {
	identity := request.requestName + "\x00" + request.location + "\x00" + string(request.family)
	sum := sha256.Sum256([]byte(identity))
	return ipClaimName("nlb-"+string(lb.UID), request.requestName+"-"+hex.EncodeToString(sum[:8]))
}

func (r *NetworkLoadBalancerReconciler) holdAddress(
	ctx context.Context, cl client.Client, namespace string, lb *networkingv1alpha.NetworkLoadBalancer,
	request loadBalancerAddressRequest, claimName string,
) (string, string, error) {
	className, prefixLength := r.PublicIPv6Class, int32(128)
	if request.family == networkingv1alpha.IPv4Protocol {
		className, prefixLength = r.PublicIPv4Class, 32
	}
	claim := &ipamv1alpha1.IPClaim{}
	claim.Namespace = namespace
	claim.Name = claimName
	claim.Labels = map[string]string{networkLoadBalancerOwnerLabel: string(lb.UID)}
	claim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName:     className,
		IPFamily:      ipamv1alpha1.IPFamily(request.family),
		PrefixLength:  ptr.To(prefixLength),
		ReclaimPolicy: ipamv1alpha1.ReclaimDelete,
		Scope: map[string]ipamv1alpha1.ScopeRef{
			ipamScopeRoleNetwork: {APIGroup: datumNetworkingAPIGroup, Kind: "Network", Name: request.networkName},
		},
	}
	if request.location != "" {
		claim.Spec.Scope[ipamScopeRoleLocation] = ipamv1alpha1.ScopeRef{
			APIGroup: datumNetworkingAPIGroup, Kind: "Location", Name: request.location,
		}
	}

	existing := &ipamv1alpha1.IPClaim{}
	err := cl.Get(ctx, client.ObjectKeyFromObject(claim), existing)
	if apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, claim); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return "", "", err
			}
			if err := cl.Get(ctx, client.ObjectKeyFromObject(claim), existing); err != nil {
				return "", "", err
			}
		} else {
			existing = claim
		}
	} else if err != nil {
		return "", "", err
	}
	if !existing.DeletionTimestamp.IsZero() {
		return "", string(existing.UID), fmt.Errorf("IPClaim %q is being deleted", claimName)
	}
	if existing.Labels[networkLoadBalancerOwnerLabel] != string(lb.UID) ||
		existing.Spec.ClassName != claim.Spec.ClassName ||
		existing.Spec.IPFamily != claim.Spec.IPFamily ||
		existing.Spec.PrefixLength == nil || *existing.Spec.PrefixLength != prefixLength ||
		existing.Spec.ReclaimPolicy != claim.Spec.ReclaimPolicy ||
		!equality.Semantic.DeepEqual(existing.Spec.Scope, claim.Spec.Scope) {
		return "", "", fmt.Errorf("IPClaim %q does not match this load balancer's address request", claimName)
	}
	address := existing.Status.Address
	if address == "" && existing.Status.AllocatedCIDR != "" {
		prefix, err := netip.ParsePrefix(existing.Status.AllocatedCIDR)
		if err != nil || prefix.Bits() != int(prefixLength) {
			return "", "", fmt.Errorf("IPClaim %q did not allocate one %s address", claimName, request.family)
		}
		address = prefix.Addr().String()
	}
	if address != "" {
		parsed, err := netip.ParseAddr(address)
		if err != nil || parsed.Is4() != (request.family == networkingv1alpha.IPv4Protocol) || parsed.Is4In6() {
			return "", "", fmt.Errorf("IPClaim %q returned an invalid %s address %q", claimName, request.family, address)
		}
	}
	return address, string(existing.UID), nil
}

func priorAddressStatus(
	endpoints []networkingv1alpha.NetworkLoadBalancerEndpointStatus, requestName, location string, family networkingv1alpha.IPFamily,
) networkingv1alpha.NetworkLoadBalancerAddressStatus {
	for _, endpoint := range endpoints {
		if endpoint.RequestName == requestName && endpoint.Location == location {
			for _, address := range endpoint.Addresses {
				if address.Family == family {
					return address
				}
			}
		}
	}
	return networkingv1alpha.NetworkLoadBalancerAddressStatus{}
}

func appendAddressStatus(status *[]networkingv1alpha.NetworkLoadBalancerEndpointStatus,
	prior []networkingv1alpha.NetworkLoadBalancerEndpointStatus, requestName, location string,
	address networkingv1alpha.NetworkLoadBalancerAddressStatus,
) {
	i := slices.IndexFunc(*status, func(endpoint networkingv1alpha.NetworkLoadBalancerEndpointStatus) bool {
		return endpoint.RequestName == requestName && endpoint.Location == location
	})
	if i < 0 {
		entry := networkingv1alpha.NetworkLoadBalancerEndpointStatus{RequestName: requestName, Location: location}
		for _, old := range prior {
			if old.RequestName == requestName && old.Location == location {
				entry.CanonicalHostname = old.CanonicalHostname
				break
			}
		}
		*status = append(*status, entry)
		i = len(*status) - 1
	}
	(*status)[i].Addresses = append((*status)[i].Addresses, address)
}

func addressAnnounced(address networkingv1alpha.NetworkLoadBalancerAddressStatus) bool {
	return address.Announced || apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced)
}

func addressPendingWithdrawal(address networkingv1alpha.NetworkLoadBalancerAddressStatus) bool {
	return addressAnnounced(address) || address.DNSPublished ||
		apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed)
}

func (r *NetworkLoadBalancerReconciler) releaseOrphanedClaims(
	ctx context.Context, cl client.Client, namespace string, lb *networkingv1alpha.NetworkLoadBalancer, wanted map[string]bool,
) error {
	var claims ipamv1alpha1.IPClaimList
	if err := cl.List(ctx, &claims, client.InNamespace(namespace),
		client.MatchingLabels{networkLoadBalancerOwnerLabel: string(lb.UID)}); err != nil {
		return fmt.Errorf("list load balancer claims: %w", err)
	}
	for i := range claims.Items {
		claim := &claims.Items[i]
		if wanted[claim.Name] {
			continue
		}
		if old := priorClaimAddressStatus(lb.Status.Endpoints, claim.Name); old != nil && addressPendingWithdrawal(*old) {
			continue
		}
		if err := cl.Delete(ctx, claim, client.Preconditions{UID: &claim.UID}); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			return fmt.Errorf("release stale IPClaim %q: %w", claim.Name, err)
		}
	}
	return nil
}

func priorClaimAddressStatus(
	endpoints []networkingv1alpha.NetworkLoadBalancerEndpointStatus, claimName string,
) *networkingv1alpha.NetworkLoadBalancerAddressStatus {
	for i := range endpoints {
		for j := range endpoints[i].Addresses {
			address := &endpoints[i].Addresses[j]
			if address.ClaimRef != nil && address.ClaimRef.Name == claimName {
				return address
			}
		}
	}
	return nil
}

func (r *NetworkLoadBalancerReconciler) deleteAddressClaim(
	ctx context.Context, ref *networkingv1alpha.NetworkLoadBalancerIPClaimRef, lbUID string,
) error {
	if ref.UID == "" {
		// A legacy status entry has no safe identity to delete by name.
		return nil
	}
	cl, err := r.IPAM.ClientForProject(ref.Project)
	if err != nil {
		return err
	}
	claim := &ipamv1alpha1.IPClaim{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, claim); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("read IPClaim %q before release: %w", ref.Name, err)
	}
	if string(claim.UID) != ref.UID || claim.Labels[networkLoadBalancerOwnerLabel] != lbUID {
		return nil
	}
	if err := cl.Delete(ctx, claim, client.Preconditions{UID: &claim.UID}); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		return fmt.Errorf("release IPClaim %q: %w", ref.Name, err)
	}
	return nil
}

func (r *NetworkLoadBalancerReconciler) release(
	ctx context.Context, cl client.Client, lb *networkingv1alpha.NetworkLoadBalancer,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(lb, networkLoadBalancerFinalizer) {
		return ctrl.Result{}, nil
	}
	for _, endpoint := range lb.Status.Endpoints {
		for _, address := range endpoint.Addresses {
			if addressPendingWithdrawal(address) {
				return ctrl.Result{RequeueAfter: networkLoadBalancerRetry}, nil
			}
		}
	}
	if r.IPAM == nil {
		return ctrl.Result{}, errors.New("cannot release load balancer addresses without IPAM")
	}
	routing, err := resolveProjectOrCluster(ctx, cl, lb.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.releaseOrphanedClaims(ctx, ipamClient, routing.projectNamespace, lb, nil); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(lb, networkLoadBalancerFinalizer)
	return ctrl.Result{}, cl.Update(ctx, lb)
}

func (r *NetworkLoadBalancerReconciler) report(
	ctx context.Context, cl client.Client, lb *networkingv1alpha.NetworkLoadBalancer,
	endpoints []networkingv1alpha.NetworkLoadBalancerEndpointStatus,
	selected []loadBalancerAddressRequest,
	status metav1.ConditionStatus, reason, message string,
) (ctrl.Result, error) {
	before := lb.DeepCopy()
	if endpoints != nil {
		lb.Status.Endpoints = endpoints
	}
	requestStatuses := make([]networkingv1alpha.NetworkLoadBalancerAddressRequestStatus, 0, len(lb.Spec.Exposure.Addresses))
	for _, request := range lb.Spec.Exposure.Addresses {
		source := networkingv1alpha.NetworkLoadBalancerLocationSourceBackends
		if request.Locations != nil {
			source = request.Locations.From
		}
		entry := networkingv1alpha.NetworkLoadBalancerAddressRequestStatus{Name: request.Name, LocationSource: source}
		for _, selection := range selected {
			if selection.requestName == request.Name && !slices.Contains(entry.ResolvedLocations, selection.location) {
				entry.ResolvedLocations = append(entry.ResolvedLocations, selection.location)
			}
		}
		slices.Sort(entry.ResolvedLocations)
		requestStatuses = append(requestStatuses, entry)
	}
	lb.Status.AddressRequests = requestStatuses
	setLoadBalancerCondition(&lb.Status.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAllocated,
		status, reason, message)
	for _, conditionType := range []string{networkingv1alpha.NetworkLoadBalancerProgrammed, networkingv1alpha.NetworkLoadBalancerReady} {
		switch {
		case status != metav1.ConditionTrue:
			setLoadBalancerCondition(&lb.Status.Conditions, lb.Generation, conditionType,
				metav1.ConditionFalse, "AllocationPending", "At least one public address is not ready for serving")
		case apimeta.FindStatusCondition(lb.Status.Conditions, conditionType) == nil ||
			addressIdentityChanged(before.Status.Endpoints, lb.Status.Endpoints):
			setLoadBalancerCondition(&lb.Status.Conditions, lb.Generation, conditionType,
				metav1.ConditionUnknown, "PendingDataPlane", "Waiting for serving locations to report programming and announcements")
		}
	}
	if equality.Semantic.DeepEqual(before.Status, lb.Status) {
		return ctrl.Result{RequeueAfter: networkLoadBalancerRetry}, nil
	}
	// Location controllers also write nested address conditions. A version
	// precondition prevents this allocation pass from overwriting their newer
	// announcement result; the manager retries the conflict.
	if err := cl.Status().Patch(ctx, lb, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("update load balancer status: %w", err)
	}
	// Periodic reconciliation notices IPAM completion and changes to the
	// dynamically selected "All" location set without a cross-cluster watch.
	return ctrl.Result{RequeueAfter: networkLoadBalancerRetry}, nil
}

func addressIdentityChanged(before, after []networkingv1alpha.NetworkLoadBalancerEndpointStatus) bool {
	if len(before) != len(after) {
		return true
	}
	for _, endpoint := range after {
		for _, address := range endpoint.Addresses {
			prior := priorAddressStatus(before, endpoint.RequestName, endpoint.Location, address.Family)
			if prior.Value != address.Value {
				return true
			}
			if prior.ClaimRef != nil && address.ClaimRef != nil &&
				prior.ClaimRef.UID != "" && address.ClaimRef.UID != "" && prior.ClaimRef.UID != address.ClaimRef.UID {
				return true
			}
		}
	}
	return false
}

func setLoadBalancerCondition(
	conditions *[]metav1.Condition, generation int64, conditionType string,
	status metav1.ConditionStatus, reason, message string,
) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type: conditionType, Status: status, Reason: reason, Message: message, ObservedGeneration: generation,
	})
}

func (r *NetworkLoadBalancerReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancer{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkloadbalancer").
		Complete(r)
}
