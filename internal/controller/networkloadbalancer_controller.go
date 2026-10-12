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
)

const (
	networkLoadBalancerFinalizer  = "networking.datumapis.com/networkloadbalancer-release"
	networkLoadBalancerOwnerLabel = "networking.datumapis.com/network-load-balancer-uid"
	networkLoadBalancerRetry      = time.Minute
)

type NetworkLoadBalancerReconciler struct {
	IPAM            IPAMClientFactory
	PublicIPv6Class string
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
		return r.report(ctx, cl, &lb, nil, metav1.ConditionFalse, "AddressServiceUnavailable",
			"Public IPv6 address allocation is not configured")
	}
	if controllerutil.AddFinalizer(&lb, networkLoadBalancerFinalizer) {
		if err := cl.Update(ctx, &lb); err != nil {
			return ctrl.Result{}, fmt.Errorf("add load balancer finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	var network networkingv1alpha.Network
	if err := cl.Get(ctx, client.ObjectKey{Namespace: lb.Namespace, Name: lb.Spec.NetworkRef.Name}, &network); err != nil {
		if apierrors.IsNotFound(err) {
			return r.report(ctx, cl, &lb, nil, metav1.ConditionFalse, "NetworkNotFound",
				fmt.Sprintf("Network %q was not found", lb.Spec.NetworkRef.Name))
		}
		return ctrl.Result{}, fmt.Errorf("read load balancer network: %w", err)
	}
	if !slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv6Protocol) {
		return r.report(ctx, cl, &lb, nil, metav1.ConditionFalse, "IPv6Required",
			"The referenced network does not carry IPv6")
	}

	routing, err := resolveProjectOrCluster(ctx, cl, lb.Namespace)
	if err != nil {
		var unresolved *projectUnresolvable
		if errors.As(err, &unresolved) {
			return r.report(ctx, cl, &lb, nil, metav1.ConditionFalse, "ProjectUnresolved", err.Error())
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

	wanted := make(map[string]bool, len(requests))
	status := make([]networkingv1alpha.NetworkLoadBalancerAddressStatus, 0, len(requests)+len(lb.Status.Addresses))
	allAllocated := len(requests) > 0
	var firstFailure string
	for _, request := range requests {
		claimName := loadBalancerClaimName(&lb, request)
		wanted[claimName] = true
		entry := priorAddressStatus(lb.Status.Addresses, request.requestName, request.location)
		entry.RequestName = request.requestName
		entry.Location = request.location
		entry.Family = networkingv1alpha.IPv6Protocol
		entry.Locations = mergeAddressLocations(entry.Locations, request.locations)
		entry.ClaimRef = &networkingv1alpha.NetworkLoadBalancerIPClaimRef{
			Project: routing.project, Namespace: routing.projectNamespace, Name: claimName,
		}

		address, claimErr := r.holdAddress(ctx, ipamClient, routing.projectNamespace, &lb, request, claimName)
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
				metav1.ConditionTrue, "Allocated", "Public IPv6 address is held in IPAM")
		}
		status = append(status, entry)
	}

	for _, old := range lb.Status.Addresses {
		if old.ClaimRef != nil && wanted[old.ClaimRef.Name] {
			continue
		}
		if addressAnnounced(old) {
			status = append(status, old)
			allAllocated = false
			continue
		}
		if old.ClaimRef != nil {
			if err := r.deleteAddressClaim(ctx, old.ClaimRef); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	if err := r.releaseOrphanedClaims(ctx, ipamClient, routing.projectNamespace, &lb, wanted); err != nil {
		return ctrl.Result{}, err
	}
	slices.SortFunc(status, func(a, b networkingv1alpha.NetworkLoadBalancerAddressStatus) int {
		if a.RequestName != b.RequestName {
			return strings.Compare(a.RequestName, b.RequestName)
		}
		return strings.Compare(a.Location, b.Location)
	})

	if !allAllocated {
		if firstFailure == "" {
			firstFailure = "One or more requested addresses are pending or awaiting withdrawal"
		}
		return r.report(ctx, cl, &lb, status, metav1.ConditionFalse, "AllocationPending", firstFailure)
	}
	return r.report(ctx, cl, &lb, status, metav1.ConditionTrue, "Allocated", "All requested public IPv6 addresses are held")
}

type loadBalancerAddressRequest struct {
	requestName string
	mode        networkingv1alpha.NetworkLoadBalancerAddressMode
	location    string
	locations   []string
	networkName string
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
		known[location.Name] = true
	}

	var requests []loadBalancerAddressRequest
	for _, address := range lb.Spec.Exposure.Addresses {
		var names []string
		if address.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceAll {
			for name := range known {
				names = append(names, name)
			}
		} else {
			// A location projection may temporarily disappear. An explicit
			// request still owns its IP throughout that gap.
			names = append(names, address.Locations.Names...)
		}
		slices.Sort(names)
		if address.Mode == networkingv1alpha.NetworkLoadBalancerAddressModeAnycast {
			requests = append(requests, loadBalancerAddressRequest{
				requestName: address.Name, mode: address.Mode, locations: names, networkName: lb.Spec.NetworkRef.Name,
			})
			continue
		}
		for _, name := range names {
			requests = append(requests, loadBalancerAddressRequest{
				requestName: address.Name, mode: address.Mode, location: name, locations: []string{name}, networkName: lb.Spec.NetworkRef.Name,
			})
		}
	}
	return requests, nil
}

func loadBalancerClaimName(lb *networkingv1alpha.NetworkLoadBalancer, request loadBalancerAddressRequest) string {
	identity := string(request.mode) + "\x00" + request.requestName + "\x00" + request.location
	sum := sha256.Sum256([]byte(identity))
	return ipClaimName("nlb-"+string(lb.UID), request.requestName+"-"+hex.EncodeToString(sum[:8]))
}

func (r *NetworkLoadBalancerReconciler) holdAddress(
	ctx context.Context, cl client.Client, namespace string, lb *networkingv1alpha.NetworkLoadBalancer,
	request loadBalancerAddressRequest, claimName string,
) (string, error) {
	claim := &ipamv1alpha1.IPClaim{}
	claim.Namespace = namespace
	claim.Name = claimName
	claim.Labels = map[string]string{networkLoadBalancerOwnerLabel: string(lb.UID)}
	claim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName:     r.PublicIPv6Class,
		IPFamily:      ipamv1alpha1.IPFamily(networkingv1alpha.IPv6Protocol),
		PrefixLength:  ptr.To[int32](128),
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
				return "", err
			}
			if err := cl.Get(ctx, client.ObjectKeyFromObject(claim), existing); err != nil {
				return "", err
			}
		} else {
			existing = claim
		}
	} else if err != nil {
		return "", err
	}
	if existing.Labels[networkLoadBalancerOwnerLabel] != string(lb.UID) ||
		existing.Spec.ClassName != claim.Spec.ClassName ||
		existing.Spec.PrefixLength == nil || *existing.Spec.PrefixLength != 128 ||
		!equality.Semantic.DeepEqual(existing.Spec.Scope, claim.Spec.Scope) {
		return "", fmt.Errorf("IPClaim %q does not match this load balancer's address request", claimName)
	}
	address := existing.Status.Address
	if address == "" && existing.Status.AllocatedCIDR != "" {
		prefix, err := netip.ParsePrefix(existing.Status.AllocatedCIDR)
		if err != nil || prefix.Bits() != 128 {
			return "", fmt.Errorf("IPClaim %q did not allocate one IPv6 address", claimName)
		}
		address = prefix.Addr().String()
	}
	if address != "" {
		parsed, err := netip.ParseAddr(address)
		if err != nil || !parsed.Is6() {
			return "", fmt.Errorf("IPClaim %q returned an invalid IPv6 address %q", claimName, address)
		}
	}
	return address, nil
}

func priorAddressStatus(
	addresses []networkingv1alpha.NetworkLoadBalancerAddressStatus, requestName, location string,
) networkingv1alpha.NetworkLoadBalancerAddressStatus {
	for _, address := range addresses {
		if address.RequestName == requestName && address.Location == location {
			return address
		}
	}
	return networkingv1alpha.NetworkLoadBalancerAddressStatus{}
}

func mergeAddressLocations(
	old []networkingv1alpha.NetworkLoadBalancerAddressLocationStatus, wanted []string,
) []networkingv1alpha.NetworkLoadBalancerAddressLocationStatus {
	byName := make(map[string]networkingv1alpha.NetworkLoadBalancerAddressLocationStatus, len(old))
	for _, location := range old {
		byName[location.Name] = location
	}
	var result []networkingv1alpha.NetworkLoadBalancerAddressLocationStatus
	for _, name := range wanted {
		location, ok := byName[name]
		if !ok {
			location.Name = name
		}
		result = append(result, location)
		delete(byName, name)
	}
	for _, location := range old {
		if _, ok := byName[location.Name]; ok && apimeta.IsStatusConditionTrue(location.Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced) {
			result = append(result, location)
		}
	}
	slices.SortFunc(result, func(a, b networkingv1alpha.NetworkLoadBalancerAddressLocationStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
	return result
}

func addressAnnounced(address networkingv1alpha.NetworkLoadBalancerAddressStatus) bool {
	for _, location := range address.Locations {
		if apimeta.IsStatusConditionTrue(location.Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced) {
			return true
		}
	}
	return false
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
		if old := priorClaimAddressStatus(lb.Status.Addresses, claim.Name); old != nil && addressAnnounced(*old) {
			continue
		}
		if err := cl.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("release stale IPClaim %q: %w", claim.Name, err)
		}
	}
	return nil
}

func priorClaimAddressStatus(
	addresses []networkingv1alpha.NetworkLoadBalancerAddressStatus, claimName string,
) *networkingv1alpha.NetworkLoadBalancerAddressStatus {
	for i := range addresses {
		if addresses[i].ClaimRef != nil && addresses[i].ClaimRef.Name == claimName {
			return &addresses[i]
		}
	}
	return nil
}

func (r *NetworkLoadBalancerReconciler) deleteAddressClaim(
	ctx context.Context, ref *networkingv1alpha.NetworkLoadBalancerIPClaimRef,
) error {
	cl, err := r.IPAM.ClientForProject(ref.Project)
	if err != nil {
		return err
	}
	claim := &ipamv1alpha1.IPClaim{}
	claim.Namespace = ref.Namespace
	claim.Name = ref.Name
	if err := cl.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
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
	for _, address := range lb.Status.Addresses {
		if addressAnnounced(address) {
			return ctrl.Result{RequeueAfter: networkLoadBalancerRetry}, nil
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
	addresses []networkingv1alpha.NetworkLoadBalancerAddressStatus,
	status metav1.ConditionStatus, reason, message string,
) (ctrl.Result, error) {
	before := lb.DeepCopy()
	if addresses != nil {
		lb.Status.Addresses = addresses
	}
	setLoadBalancerCondition(&lb.Status.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAllocated,
		status, reason, message)
	for _, conditionType := range []string{networkingv1alpha.NetworkLoadBalancerProgrammed, networkingv1alpha.NetworkLoadBalancerReady} {
		if apimeta.FindStatusCondition(lb.Status.Conditions, conditionType) == nil {
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
