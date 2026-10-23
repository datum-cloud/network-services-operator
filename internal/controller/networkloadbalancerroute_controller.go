// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	nlbRouteReasonAttached              = "Attached"
	nlbRouteReasonParentNotFound        = "ParentNotFound"
	nlbRouteReasonListenerNotFound      = "ListenerNotFound"
	nlbRouteReasonResolved              = "Resolved"
	nlbRouteReasonBackendNotFound       = "BackendNotFound"
	nlbRouteReasonPortNotFound          = "PortNotFound"
	nlbRouteReasonProtocolMismatch      = "ProtocolMismatch"
	nlbRouteReasonInvalidSelector       = "InvalidSelector"
	nlbRouteReasonNetworkMismatch       = "NetworkMismatch"
	nlbRouteReasonHealthCheckInvalid    = "HealthCheckInvalid"
	nlbRouteReasonNoEligibleEndpoints   = "NoEligibleEndpoints"
	nlbRouteReasonAwaitingDataPlane     = "AwaitingDataPlane"
	nlbRouteReasonSuperseded            = "Superseded"
	nlbRouteReasonUnsupportedCapability = "UnsupportedCapability"
	nlbRouteReasonBackendClaimConflict  = "BackendClaimConflict"
)

type NetworkLoadBalancerRouteReconciler struct {
	mgr mcmanager.Manager
}

type networkLoadBalancerRouteEndpoint struct {
	NetworkService  string
	Interface       string
	SourceNamespace string
	SourceUID       string
	Location        string
	Address         string
	Port            int32
}

type networkLoadBalancerRouteReferenceError struct {
	reason  string
	message string
}

func (e *networkLoadBalancerRouteReferenceError) Error() string { return e.message }

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers;networkservices;networkinterfaces,verbs=get;list;watch

func (r *NetworkLoadBalancerRouteReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	ctx = mccontext.WithCluster(ctx, req.ClusterName)
	return ctrl.Result{}, r.reconcileRoute(ctx, cl.GetClient(), req.NamespacedName)
}

func (r *NetworkLoadBalancerRouteReconciler) reconcileRoute(ctx context.Context, cl client.Client, key client.ObjectKey) error {
	var route networkingv1alpha.NetworkLoadBalancerRoute
	if err := cl.Get(ctx, key, &route); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !route.DeletionTimestamp.IsZero() {
		return nil
	}

	var peers networkingv1alpha.NetworkLoadBalancerRouteList
	if err := cl.List(ctx, &peers, client.InNamespace(route.Namespace)); err != nil {
		return fmt.Errorf("list load balancer routes: %w", err)
	}

	status := networkingv1alpha.NetworkLoadBalancerRouteStatus{
		Parents: make([]networkingv1alpha.NetworkLoadBalancerRouteParentStatus, 0, len(route.Spec.ParentRefs)),
	}
	seenParents := make(map[string]struct{}, len(route.Spec.ParentRefs))
	for _, ref := range route.Spec.ParentRefs {
		parent, err := reconcileRouteParent(ctx, cl, &route, ref, peers.Items)
		if err != nil {
			return err
		}
		key := parent.Name + "/" + parent.Listener
		if _, exists := seenParents[key]; !exists {
			status.Parents = append(status.Parents, parent)
			seenParents[key] = struct{}{}
		}
	}
	preserveRouteConditionTransitionTimes(&status, &route.Status)

	if equality.Semantic.DeepEqual(route.Status, status) {
		return nil
	}
	route.Status = status
	if err := cl.Status().Update(ctx, &route); err != nil {
		return fmt.Errorf("update load balancer route status: %w", err)
	}
	return nil
}

func reconcileRouteParent(
	ctx context.Context,
	cl client.Client,
	route *networkingv1alpha.NetworkLoadBalancerRoute,
	ref networkingv1alpha.NetworkLoadBalancerRouteParentRef,
	peers []networkingv1alpha.NetworkLoadBalancerRoute,
) (networkingv1alpha.NetworkLoadBalancerRouteParentStatus, error) {
	status := networkingv1alpha.NetworkLoadBalancerRouteParentStatus{Name: ref.Name, Listener: ref.Listener}
	if ref.Port != nil {
		status.Listener = fmt.Sprintf("port:%d", *ref.Port)
	}
	setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
		metav1.ConditionFalse, nlbRouteReasonAwaitingDataPlane, "Data plane programming has not been confirmed")
	if ref.Namespace != "" && ref.Namespace != route.Namespace {
		message := "Cross-namespace parent references are unavailable"
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
			metav1.ConditionFalse, nlbRouteReasonUnsupportedCapability, message)
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
			metav1.ConditionFalse, nlbRouteReasonUnsupportedCapability, message)
		return status, nil
	}

	var parent networkingv1alpha.NetworkLoadBalancer
	err := cl.Get(ctx, client.ObjectKey{Namespace: route.Namespace, Name: ref.Name}, &parent)
	if apierrors.IsNotFound(err) {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
			metav1.ConditionFalse, nlbRouteReasonParentNotFound, fmt.Sprintf("NetworkLoadBalancer %q does not exist", ref.Name))
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
			metav1.ConditionUnknown, nlbRouteReasonParentNotFound, "Backend protocol cannot be checked until the parent exists")
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("get load balancer %q: %w", ref.Name, err)
	}
	listenerName, found := ResolveNetworkLoadBalancerRouteListener(&parent, ref)
	if !found {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
			metav1.ConditionFalse, nlbRouteReasonListenerNotFound, fmt.Sprintf("Listener %q does not exist on NetworkLoadBalancer %q", ref.Listener, ref.Name))
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
			metav1.ConditionUnknown, nlbRouteReasonListenerNotFound, "Backend protocol cannot be checked until the listener exists")
		return status, nil
	}
	status.Listener = listenerName
	listenerIndex := slices.IndexFunc(parent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool {
		return l.Name == listenerName
	})

	setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
		metav1.ConditionTrue, nlbRouteReasonAttached, "Route is attached to the listener")
	listener := &parent.Spec.Listeners[listenerIndex]
	if message := unsupportedRouteListenerCapability(route, &parent, listener); message != "" {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
			metav1.ConditionFalse, nlbRouteReasonUnsupportedCapability, message)
		return status, nil
	}
	winner := oldestRouteForListener(peers, &parent, listenerName)
	status.Effective = winner != nil && winner.UID == route.UID
	if !status.Effective && winner != nil {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
			metav1.ConditionFalse, nlbRouteReasonSuperseded,
			fmt.Sprintf("Older route %q owns this listener", winner.Name))
	}

	endpoints, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, &parent, listener)
	if referenceErr, ok := err.(*networkLoadBalancerRouteReferenceError); ok {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
			metav1.ConditionFalse, referenceErr.reason, referenceErr.message)
		return status, nil
	}
	if err != nil {
		return status, err
	}
	if status.Effective {
		if len(listener.AddressRefs) > 1 {
			message := "One listener cannot select the same backend socket for multiple address requests until Galactic supports backend reuse across VIPs"
			setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
				metav1.ConditionFalse, nlbRouteReasonBackendClaimConflict, message)
			setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
				metav1.ConditionFalse, nlbRouteReasonBackendClaimConflict, message)
			return status, nil
		}
		owner, err := olderRouteClaimingBackend(ctx, cl, route, &parent, listener, endpoints, peers, newBackendClaimCache())
		if err != nil {
			return status, err
		}
		if owner != "" {
			message := fmt.Sprintf("Backend socket is already selected by older route %q; Galactic cannot return traffic to two NLB VIPs on one socket", owner)
			setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
				metav1.ConditionFalse, nlbRouteReasonBackendClaimConflict, message)
			setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
				metav1.ConditionFalse, nlbRouteReasonBackendClaimConflict, message)
			return status, nil
		}
	}
	setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
		metav1.ConditionTrue, nlbRouteReasonResolved,
		fmt.Sprintf("All backend references resolved; %d holder-available IPv6 endpoints are eligible", len(endpoints)))
	if status.Effective && len(endpoints) == 0 {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
			metav1.ConditionFalse, nlbRouteReasonNoEligibleEndpoints, "No enabled backend has a holder-available IPv6 endpoint with source identity in a selected location")
	}
	return status, nil
}

func unsupportedRouteListenerCapability(route *networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
) string {
	if listener.Protocol == networkingv1alpha.NetworkLoadBalancerProtocolTLS || len(route.Spec.SNIHostnames) > 0 {
		return "TLS listener and SNI routing require a proxy-capable edge path"
	}
	for _, addressName := range listener.AddressRefs {
		if slices.ContainsFunc(parent.Spec.Listeners, func(other networkingv1alpha.NetworkLoadBalancerListener) bool {
			return other.Name != listener.Name && slices.Contains(other.AddressRefs, addressName)
		}) {
			return "Address sharing across listeners requires coordinated readiness"
		}
	}
	return ""
}

// olderRouteClaimingBackend applies the temporary Galactic return-path gate.
// Each serving rule may own a backend address, port, protocol, and location
// only once. A rule with an unresolved backend claims no socket yet; interface
// changes requeue every route so a newly available socket is checked again.
func olderRouteClaimingBackend(ctx context.Context, cl client.Client,
	route *networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
	endpoints []networkLoadBalancerRouteEndpoint,
	peers []networkingv1alpha.NetworkLoadBalancerRoute,
	cache *backendClaimCache,
) (string, error) {
	key := backendClaimKey{route: route.UID, parent: parent.Name, listener: listener.Name}
	if owner, done := cache.blocked[key]; done {
		return owner, nil
	}
	owner, err := findOlderRouteClaimingBackend(ctx, cl, route, parent, listener, endpoints, peers, cache)
	if err != nil {
		return "", err
	}
	cache.blocked[key] = owner
	return owner, nil
}

func findOlderRouteClaimingBackend(ctx context.Context, cl client.Client,
	route *networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
	endpoints []networkLoadBalancerRouteEndpoint,
	peers []networkingv1alpha.NetworkLoadBalancerRoute,
	cache *backendClaimCache,
) (string, error) {
	// A listener cannot reserve return-path sockets before it has a usable
	// address. In particular, a stalled allocation must not block a peer NLB.
	endpoints = slices.DeleteFunc(slices.Clone(endpoints), func(endpoint networkLoadBalancerRouteEndpoint) bool {
		return !listenerHasAllocatedVIP(parent, listener, endpoint.Location)
	})
	if len(endpoints) == 0 {
		return "", nil
	}
	for i := range peers {
		peer := &peers[i]
		older := peer.CreationTimestamp.Before(&route.CreationTimestamp) ||
			(peer.CreationTimestamp.Equal(&route.CreationTimestamp) && peer.Name < route.Name)
		if !peer.DeletionTimestamp.IsZero() || (peer.UID != route.UID && !older) {
			continue
		}
		for _, ref := range peer.Spec.ParentRefs {
			if ref.Namespace != "" && ref.Namespace != peer.Namespace {
				continue
			}
			var otherParent networkingv1alpha.NetworkLoadBalancer
			if err := cl.Get(ctx, client.ObjectKey{Namespace: peer.Namespace, Name: ref.Name}, &otherParent); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return "", err
			}
			if otherParent.Spec.NetworkRef.Name != parent.Spec.NetworkRef.Name {
				continue
			}
			otherListenerName, found := ResolveNetworkLoadBalancerRouteListener(&otherParent, ref)
			if !found || oldestRouteForListener(peers, &otherParent, otherListenerName) != peer {
				continue
			}
			otherIndex := slices.IndexFunc(otherParent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool {
				return l.Name == otherListenerName
			})
			if otherIndex < 0 || otherParent.Spec.Listeners[otherIndex].Protocol != listener.Protocol {
				continue
			}
			otherListener := &otherParent.Spec.Listeners[otherIndex]
			if unsupportedRouteListenerCapability(peer, &otherParent, otherListener) != "" || len(otherListener.AddressRefs) != 1 {
				continue
			}
			if peer.UID == route.UID {
				if otherParent.Name > parent.Name ||
					(otherParent.Name == parent.Name && otherListener.Name >= listener.Name) {
					continue
				}
			}
			otherEndpoints, err := cache.claimedEndpoints(ctx, cl, peer, &otherParent, otherListener)
			if err != nil {
				return "", err
			}
			if len(otherEndpoints) == 0 {
				continue
			}
			// An older candidate that lost a conflict claims nothing. This makes
			// socket ownership transitive: A blocking B does not let B block C.
			if peer.UID != route.UID {
				blockedBy, err := olderRouteClaimingBackend(ctx, cl, peer, &otherParent, otherListener, otherEndpoints, peers, cache)
				if err != nil {
					return "", err
				}
				if blockedBy != "" {
					continue
				}
			}
			for _, endpoint := range endpoints {
				if slices.ContainsFunc(otherEndpoints, func(other networkLoadBalancerRouteEndpoint) bool {
					return other.Location == endpoint.Location && other.Address == endpoint.Address && other.Port == endpoint.Port
				}) {
					return peer.Name, nil
				}
			}
		}
	}
	return "", nil
}

type backendClaimKey struct {
	route    types.UID
	parent   string
	listener string
}

type backendClaimCache struct {
	blocked   map[backendClaimKey]string
	endpoints map[backendClaimKey][]networkLoadBalancerRouteEndpoint
}

func newBackendClaimCache() *backendClaimCache {
	return &backendClaimCache{
		blocked:   map[backendClaimKey]string{},
		endpoints: map[backendClaimKey][]networkLoadBalancerRouteEndpoint{},
	}
}

func (c *backendClaimCache) claimedEndpoints(ctx context.Context, cl client.Client,
	route *networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
) ([]networkLoadBalancerRouteEndpoint, error) {
	key := backendClaimKey{route: route.UID, parent: parent.Name, listener: listener.Name}
	if endpoints, done := c.endpoints[key]; done {
		return endpoints, nil
	}
	endpoints, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, listener)
	if _, unresolved := err.(*networkLoadBalancerRouteReferenceError); unresolved {
		endpoints, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	endpoints = slices.DeleteFunc(endpoints, func(endpoint networkLoadBalancerRouteEndpoint) bool {
		return !listenerHasAllocatedVIP(parent, listener, endpoint.Location)
	})
	c.endpoints[key] = endpoints
	return endpoints, nil
}

func listenerHasAllocatedVIP(parent *networkingv1alpha.NetworkLoadBalancer, listener *networkingv1alpha.NetworkLoadBalancerListener, location string) bool {
	for _, endpoint := range parent.Status.Endpoints {
		if endpoint.Location != location || !slices.Contains(listener.AddressRefs, endpoint.RequestName) {
			continue
		}
		for _, address := range endpoint.Addresses {
			allocated := apimeta.FindStatusCondition(address.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
			if address.Family == networkingv1alpha.IPv6Protocol && address.Value != "" && address.ClaimRef != nil &&
				allocated != nil && allocated.Status == metav1.ConditionTrue && allocated.ObservedGeneration == parent.Generation {
				return true
			}
		}
	}
	return false
}

// ResolveNetworkLoadBalancerRouteListener returns the unique listener selected
// by a route parent reference. Port selection is ambiguous when TCP and UDP
// listeners share a port, even if their addresses differ.
func ResolveNetworkLoadBalancerRouteListener(parent *networkingv1alpha.NetworkLoadBalancer,
	ref networkingv1alpha.NetworkLoadBalancerRouteParentRef) (string, bool) {
	if ref.Listener != "" {
		index := slices.IndexFunc(parent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool {
			return l.Name == ref.Listener
		})
		return ref.Listener, index >= 0
	}
	if ref.Port == nil {
		return "", false
	}
	name := ""
	for _, listener := range parent.Spec.Listeners {
		if listener.Port == *ref.Port {
			if name != "" {
				return "", false
			}
			name = listener.Name
		}
	}
	return name, name != ""
}

func oldestRouteForListener(routes []networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer, listener string) *networkingv1alpha.NetworkLoadBalancerRoute {
	var oldest *networkingv1alpha.NetworkLoadBalancerRoute
	for i := range routes {
		candidate := &routes[i]
		if !candidate.DeletionTimestamp.IsZero() || !slices.ContainsFunc(candidate.Spec.ParentRefs, func(ref networkingv1alpha.NetworkLoadBalancerRouteParentRef) bool {
			if ref.Name != parent.Name || (ref.Namespace != "" && ref.Namespace != candidate.Namespace) || candidate.Namespace != parent.Namespace {
				return false
			}
			resolved, found := ResolveNetworkLoadBalancerRouteListener(parent, ref)
			return found && resolved == listener
		}) {
			continue
		}
		listenerIndex := slices.IndexFunc(parent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool {
			return l.Name == listener
		})
		if listenerIndex < 0 || unsupportedRouteListenerCapability(candidate, parent, &parent.Spec.Listeners[listenerIndex]) != "" {
			continue
		}
		if oldest == nil || candidate.CreationTimestamp.Before(&oldest.CreationTimestamp) ||
			(candidate.CreationTimestamp.Equal(&oldest.CreationTimestamp) &&
				(strings.Compare(candidate.Name, oldest.Name) < 0 ||
					(candidate.Name == oldest.Name && strings.Compare(string(candidate.UID), string(oldest.UID)) < 0))) {
			oldest = candidate
		}
	}
	return oldest
}

// Status is rebuilt on each reconcile, so restore transition times for
// conditions whose status has not changed before comparing or writing it.
func preserveRouteConditionTransitionTimes(current, previous *networkingv1alpha.NetworkLoadBalancerRouteStatus) {
	for i := range current.Parents {
		parent := &current.Parents[i]
		oldIndex := slices.IndexFunc(previous.Parents, func(old networkingv1alpha.NetworkLoadBalancerRouteParentStatus) bool {
			return old.Name == parent.Name && old.Listener == parent.Listener
		})
		if oldIndex < 0 {
			continue
		}
		for j := range parent.Conditions {
			condition := &parent.Conditions[j]
			old := apimeta.FindStatusCondition(previous.Parents[oldIndex].Conditions, condition.Type)
			if old != nil && old.Status == condition.Status {
				condition.LastTransitionTime = old.LastTransitionTime
			}
		}
	}
}

func resolveNetworkLoadBalancerRouteBackends(
	ctx context.Context,
	cl client.Client,
	route *networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
) ([]networkLoadBalancerRouteEndpoint, error) {
	if len(route.Spec.Backends) > 1 {
		return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonUnsupportedCapability,
			"Multiple backend entries require cell support for complete endpoint selection"}
	}
	var endpoints []networkLoadBalancerRouteEndpoint
	for _, backend := range route.Spec.Backends {
		if backend.Transport != nil || (backend.NetworkService.Namespace != "" && backend.NetworkService.Namespace != route.Namespace) {
			return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonUnsupportedCapability,
				"Backend TLS transport and cross-namespace backend references are unavailable"}
		}
		var service networkingv1alpha.NetworkService
		ref := backend.NetworkService
		err := cl.Get(ctx, client.ObjectKey{Namespace: route.Namespace, Name: ref.Name}, &service)
		if apierrors.IsNotFound(err) {
			return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonBackendNotFound,
				fmt.Sprintf("NetworkService %q does not exist", ref.Name)}
		}
		if err != nil {
			return nil, fmt.Errorf("get NetworkService %q: %w", ref.Name, err)
		}
		portIndex := slices.IndexFunc(service.Spec.Ports, func(p networkingv1alpha.NetworkServicePort) bool { return p.Name == ref.Port })
		if portIndex < 0 {
			return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonPortNotFound,
				fmt.Sprintf("NetworkService %q has no port named %q", ref.Name, ref.Port)}
		}
		port := service.Spec.Ports[portIndex]
		protocol := port.Protocol
		if protocol == "" {
			protocol = networkingv1alpha.NetworkServiceProtocolTCP
		}
		if string(protocol) != string(listener.Protocol) {
			return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonProtocolMismatch,
				fmt.Sprintf("NetworkService %q port %q uses %s; listener %q requires %s", ref.Name, ref.Port, protocol, listener.Name, listener.Protocol)}
		}
		if err := validateNetworkLoadBalancerHealthCheck(&service, &backend, protocol); err != nil {
			return nil, err
		}
		selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)
		if err != nil {
			return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonInvalidSelector,
				fmt.Sprintf("NetworkService %q has an invalid interface selector: %v", ref.Name, err)}
		}
		members, err := matchingInterfaces(ctx, cl, route.Namespace, selector)
		if err != nil {
			return nil, err
		}
		for i := range members {
			if members[i].Spec.Network.Name != parent.Spec.NetworkRef.Name {
				return nil, &networkLoadBalancerRouteReferenceError{nlbRouteReasonNetworkMismatch,
					fmt.Sprintf("NetworkService %q selects an interface outside network %q", ref.Name, parent.Spec.NetworkRef.Name)}
			}
		}
		for i := range members {
			member := &members[i]
			location := member.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]
			sourceNamespace := member.Labels[networkingv1alpha.NetworkInterfaceSourceNamespaceLabel]
			sourceUID := member.Labels[networkingv1alpha.NetworkInterfaceSourceUIDLabel]
			if !isProjection(member) || sourceNamespace == "" || sourceUID == "" ||
				location == "" || !routeLocationSelected(parent, listener, location) || !isServiceMemberHealthy(member) {
				continue
			}
			address, ok := addressOfType(interfaceBackhaulAddresses(member), discoveryv1.AddressTypeIPv6)
			if !ok {
				continue
			}
			endpoints = append(endpoints, networkLoadBalancerRouteEndpoint{
				NetworkService:  ref.Name,
				Interface:       member.Name,
				SourceNamespace: sourceNamespace,
				SourceUID:       sourceUID,
				Location:        location,
				Address:         address,
				Port:            port.Port,
			})
		}
	}
	slices.SortFunc(endpoints, func(a, b networkLoadBalancerRouteEndpoint) int {
		if n := strings.Compare(a.Location, b.Location); n != 0 {
			return n
		}
		if n := strings.Compare(a.NetworkService, b.NetworkService); n != 0 {
			return n
		}
		return strings.Compare(a.Interface, b.Interface)
	})
	return endpoints, nil
}

func routeLocationSelected(
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
	location string,
) bool {
	for _, address := range parent.Spec.Exposure.Addresses {
		if !slices.Contains(listener.AddressRefs, address.Name) {
			continue
		}
		if address.Locations == nil || address.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceBackends ||
			address.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceAll ||
			(address.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceNames && slices.Contains(address.Locations.Names, location)) {
			return true
		}
	}
	return false
}

func validateNetworkLoadBalancerHealthCheck(
	service *networkingv1alpha.NetworkService,
	backend *networkingv1alpha.NetworkLoadBalancerRouteBackend,
	protocol networkingv1alpha.NetworkServiceProtocol,
) error {
	check := backend.HealthCheck
	if check != nil && (check.IntervalSeconds != nil || check.TimeoutSeconds != nil ||
		check.HealthyThreshold != nil || check.UnhealthyThreshold != nil) {
		return &networkLoadBalancerRouteReferenceError{nlbRouteReasonUnsupportedCapability,
			"Custom health timing and thresholds are not supported by serving cells"}
	}
	if check == nil {
		if string(protocol) == string(corev1.ProtocolUDP) {
			return &networkLoadBalancerRouteReferenceError{nlbRouteReasonHealthCheckInvalid,
				fmt.Sprintf("UDP backend %q requires a TCP, HTTP, or HTTPS health check", backend.NetworkService.Name)}
		}
		return nil
	}
	if check.Port == "" {
		if check.Protocol != "TCP" || string(protocol) != string(corev1.ProtocolTCP) {
			return &networkLoadBalancerRouteReferenceError{nlbRouteReasonHealthCheckInvalid,
				fmt.Sprintf("Health check for NetworkService %q requires a named TCP port", backend.NetworkService.Name)}
		}
		return nil
	}
	probeIndex := slices.IndexFunc(service.Spec.Ports, func(p networkingv1alpha.NetworkServicePort) bool { return p.Name == check.Port })
	if probeIndex < 0 {
		return &networkLoadBalancerRouteReferenceError{nlbRouteReasonHealthCheckInvalid,
			fmt.Sprintf("NetworkService %q has no health check port named %q", backend.NetworkService.Name, check.Port)}
	}
	probeProtocol := service.Spec.Ports[probeIndex].Protocol
	if probeProtocol != "" && probeProtocol != networkingv1alpha.NetworkServiceProtocolTCP {
		return &networkLoadBalancerRouteReferenceError{nlbRouteReasonHealthCheckInvalid,
			fmt.Sprintf("NetworkService %q health check port %q must use TCP", backend.NetworkService.Name, check.Port)}
	}
	return nil
}

func setRouteParentCondition(
	status *networkingv1alpha.NetworkLoadBalancerRouteParentStatus,
	generation int64,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
	reason string,
	message string,
) {
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: conditionType, Status: conditionStatus, Reason: reason,
		Message: message, ObservedGeneration: generation,
	})
}

func routesForLoadBalancerEvent(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	return routesInNamespace(ctx, cl, obj.GetNamespace(), func(route *networkingv1alpha.NetworkLoadBalancerRoute) bool {
		return slices.ContainsFunc(route.Spec.ParentRefs, func(ref networkingv1alpha.NetworkLoadBalancerRouteParentRef) bool { return ref.Name == obj.GetName() })
	})
}

func routesForNetworkServiceEvent(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	return routesInNamespace(ctx, cl, obj.GetNamespace(), func(route *networkingv1alpha.NetworkLoadBalancerRoute) bool {
		return slices.ContainsFunc(route.Spec.Backends, func(backend networkingv1alpha.NetworkLoadBalancerRouteBackend) bool {
			return backend.NetworkService.Name == obj.GetName()
		})
	})
}

func routesForInterfaceEvent(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	return routesInNamespace(ctx, cl, obj.GetNamespace(), func(*networkingv1alpha.NetworkLoadBalancerRoute) bool { return true })
}

func siblingRoutesForRouteEvent(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	// A route can claim a backend socket used by a different NLB in this
	// namespace. Its creation or withdrawal must re-evaluate every peer.
	return routesInNamespace(ctx, cl, obj.GetNamespace(), func(*networkingv1alpha.NetworkLoadBalancerRoute) bool { return true })
}

func routesInNamespace(
	ctx context.Context,
	cl client.Client,
	namespace string,
	include func(*networkingv1alpha.NetworkLoadBalancerRoute) bool,
) []reconcile.Request {
	var routes networkingv1alpha.NetworkLoadBalancerRouteList
	if err := cl.List(ctx, &routes, client.InNamespace(namespace)); err != nil {
		log.FromContext(ctx).Error(err, "list routes for load balancer event")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(routes.Items))
	for i := range routes.Items {
		if include(&routes.Items[i]) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&routes.Items[i])})
		}
	}
	return requests
}

func (r *NetworkLoadBalancerRouteReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	watch := func(mapper func(context.Context, client.Client, client.Object) []reconcile.Request) func(multicluster.ClusterName, cluster.Cluster) mchandler.EventHandler {
		return func(clusterName multicluster.ClusterName, cl cluster.Cluster) mchandler.EventHandler {
			return mchandler.ForCluster(handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				return mapper(ctx, cl.GetClient(), obj)
			}), clusterName)
		}
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancerRoute{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Watches(&networkingv1alpha.NetworkLoadBalancerRoute{}, watch(siblingRoutesForRouteEvent)).
		Watches(&networkingv1alpha.NetworkLoadBalancer{}, watch(routesForLoadBalancerEvent)).
		Watches(&networkingv1alpha.NetworkService{}, watch(routesForNetworkServiceEvent)).
		Watches(&networkingv1alpha.NetworkInterface{}, watch(routesForInterfaceEvent)).
		Named("networkloadbalancerroute").
		Complete(r)
}
