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
	nlbRouteReasonAttached            = "Attached"
	nlbRouteReasonParentNotFound      = "ParentNotFound"
	nlbRouteReasonListenerNotFound    = "ListenerNotFound"
	nlbRouteReasonResolved            = "Resolved"
	nlbRouteReasonBackendNotFound     = "BackendNotFound"
	nlbRouteReasonPortNotFound        = "PortNotFound"
	nlbRouteReasonProtocolMismatch    = "ProtocolMismatch"
	nlbRouteReasonInvalidSelector     = "InvalidSelector"
	nlbRouteReasonNetworkMismatch     = "NetworkMismatch"
	nlbRouteReasonHealthCheckInvalid  = "HealthCheckInvalid"
	nlbRouteReasonNoEligibleEndpoints = "NoEligibleEndpoints"
	nlbRouteReasonAwaitingDataPlane   = "AwaitingDataPlane"
	nlbRouteReasonSuperseded          = "Superseded"
)

type NetworkLoadBalancerRouteReconciler struct {
	mgr mcmanager.Manager
}

type networkLoadBalancerRouteEndpoint struct {
	NetworkService string
	Interface      string
	Location       string
	Address        string
	Port           int32
	Weight         int32
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
	for _, ref := range route.Spec.ParentRefs {
		parent, err := reconcileRouteParent(ctx, cl, &route, ref, peers.Items)
		if err != nil {
			return err
		}
		status.Parents = append(status.Parents, parent)
	}

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
	setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
		metav1.ConditionFalse, nlbRouteReasonAwaitingDataPlane, "Data plane programming has not been confirmed")

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
	listenerIndex := slices.IndexFunc(parent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool {
		return l.Name == ref.Listener
	})
	if listenerIndex < 0 {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
			metav1.ConditionFalse, nlbRouteReasonListenerNotFound, fmt.Sprintf("Listener %q does not exist on NetworkLoadBalancer %q", ref.Listener, ref.Name))
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
			metav1.ConditionUnknown, nlbRouteReasonListenerNotFound, "Backend protocol cannot be checked until the listener exists")
		return status, nil
	}

	setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteAccepted,
		metav1.ConditionTrue, nlbRouteReasonAttached, "Route is attached to the listener")
	winner := oldestRouteForListener(peers, ref)
	status.Effective = winner != nil && winner.UID == route.UID
	if !status.Effective && winner != nil {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
			metav1.ConditionFalse, nlbRouteReasonSuperseded,
			fmt.Sprintf("Older route %q owns this listener", winner.Name))
	}

	endpoints, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, &parent, &parent.Spec.Listeners[listenerIndex])
	if referenceErr, ok := err.(*networkLoadBalancerRouteReferenceError); ok {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
			metav1.ConditionFalse, referenceErr.reason, referenceErr.message)
		return status, nil
	}
	if err != nil {
		return status, err
	}
	setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
		metav1.ConditionTrue, nlbRouteReasonResolved,
		fmt.Sprintf("All backend references resolved; %d holder-available IPv6 endpoints are eligible", len(endpoints)))
	if status.Effective && len(endpoints) == 0 {
		setRouteParentCondition(&status, route.Generation, networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
			metav1.ConditionFalse, nlbRouteReasonNoEligibleEndpoints, "No enabled backend has a holder-available IPv6 endpoint in a selected location")
	}
	return status, nil
}

func oldestRouteForListener(routes []networkingv1alpha.NetworkLoadBalancerRoute, ref networkingv1alpha.NetworkLoadBalancerRouteParentRef) *networkingv1alpha.NetworkLoadBalancerRoute {
	var oldest *networkingv1alpha.NetworkLoadBalancerRoute
	for i := range routes {
		candidate := &routes[i]
		if !candidate.DeletionTimestamp.IsZero() || !slices.Contains(candidate.Spec.ParentRefs, ref) {
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

func resolveNetworkLoadBalancerRouteBackends(
	ctx context.Context,
	cl client.Client,
	route *networkingv1alpha.NetworkLoadBalancerRoute,
	parent *networkingv1alpha.NetworkLoadBalancer,
	listener *networkingv1alpha.NetworkLoadBalancerListener,
) ([]networkLoadBalancerRouteEndpoint, error) {
	var endpoints []networkLoadBalancerRouteEndpoint
	for _, backend := range route.Spec.Backends {
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
		weight := int32(1)
		if backend.Weight != nil {
			weight = *backend.Weight
		}
		if weight == 0 {
			continue
		}
		for i := range members {
			member := &members[i]
			location := member.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]
			if location == "" || !routeLocationSelected(parent, listener, location) || !isServiceMemberHealthy(member) {
				continue
			}
			address, ok := addressOfType(interfaceBackhaulAddresses(member), discoveryv1.AddressTypeIPv6)
			if !ok {
				continue
			}
			endpoints = append(endpoints, networkLoadBalancerRouteEndpoint{
				NetworkService: ref.Name,
				Interface:      member.Name,
				Location:       location,
				Address:        address,
				Port:           port.Port,
				Weight:         weight,
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
		if address.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceAll ||
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
	changed, ok := obj.(*networkingv1alpha.NetworkLoadBalancerRoute)
	if !ok {
		return nil
	}
	return routesInNamespace(ctx, cl, obj.GetNamespace(), func(route *networkingv1alpha.NetworkLoadBalancerRoute) bool {
		for _, ref := range changed.Spec.ParentRefs {
			if slices.Contains(route.Spec.ParentRefs, ref) {
				return true
			}
		}
		return false
	})
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
