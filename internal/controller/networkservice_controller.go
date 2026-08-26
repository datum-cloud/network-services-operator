// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	// networkServiceReasonInvalidSelector means the selector cannot be compiled.
	// Schema validation rejects an empty selector but not every malformed
	// expression, so the membership reports what it could not evaluate rather
	// than retrying an object no retry can fix.
	networkServiceReasonInvalidSelector = "InvalidSelector"

	networkServiceReasonResolved = "Resolved"
	networkServiceReasonReady    = "Ready"

	// maxNetworkServiceLocations is what the status field holds. The totals stay
	// truthful past it and the per-location list is cut, because a status the
	// API server refuses reports nothing at all.
	maxNetworkServiceLocations = 64
)

// NetworkServiceReconciler resolves a NetworkService's membership from the
// network interfaces its selector matches.
type NetworkServiceReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch

func (r *NetworkServiceReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	ctx = mccontext.WithCluster(ctx, req.ClusterName)

	logger.Info("reconciling network service")
	defer logger.Info("reconcile complete")

	return ctrl.Result{}, r.reconcileService(ctx, cl.GetClient(), req.NamespacedName)
}

func (r *NetworkServiceReconciler) reconcileService(
	ctx context.Context,
	cl client.Client,
	key client.ObjectKey,
) error {
	var service networkingv1alpha.NetworkService
	if err := cl.Get(ctx, key, &service); err != nil {
		return client.IgnoreNotFound(err)
	}

	if !service.DeletionTimestamp.IsZero() {
		return nil
	}

	status := networkingv1alpha.NetworkServiceStatus{
		Conditions: slices.Clone(service.Status.Conditions),
	}

	selector, selectorErr := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)

	var members []networkingv1alpha.NetworkInterface
	if selectorErr == nil {
		matched, err := matchingInterfaces(ctx, cl, service.Namespace, selector)
		if err != nil {
			return err
		}
		members = matched
	}

	switch {
	case selectorErr != nil:
		setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
			metav1.ConditionFalse, networkServiceReasonInvalidSelector,
			fmt.Sprintf("The interface selector cannot be evaluated: %v", selectorErr))

	case len(members) == 0:
		setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
			metav1.ConditionFalse, networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces,
			"The selector matched no network interface a workload holds")

	default:
		if networks := memberNetworks(members); len(networks) > 1 {
			setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
				metav1.ConditionFalse, networkingv1alpha.NetworkServiceReasonMultipleNetworks,
				fmt.Sprintf("The selector matched interfaces on %d networks (%s), and a service spans one network",
					len(networks), strings.Join(networks, ", ")))
			break
		}

		locations, summary, unlocated := summarizeMembership(members)
		status.Locations = locations
		status.Summary = summary
		setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
			metav1.ConditionTrue, networkServiceReasonResolved, membershipMessage(summary, unlocated))
	}

	setNetworkServiceReady(&status, &service)

	if equality.Semantic.DeepEqual(service.Status, status) {
		return nil
	}

	service.Status = status
	if err := cl.Status().Update(ctx, &service); err != nil {
		return fmt.Errorf("failed updating network service status: %w", err)
	}
	return nil
}

// matchingInterfaces never leaves the service's own namespace. A selector
// reaches only the interfaces the consumer owns, and only those a workload
// still holds.
func matchingInterfaces(
	ctx context.Context,
	cl client.Client,
	namespace string,
	selector labels.Selector,
) ([]networkingv1alpha.NetworkInterface, error) {
	var interfaces networkingv1alpha.NetworkInterfaceList
	if err := cl.List(ctx, &interfaces,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed listing network interfaces: %w", err)
	}

	held := make([]networkingv1alpha.NetworkInterface, 0, len(interfaces.Items))
	for i := range interfaces.Items {
		if isServiceMember(&interfaces.Items[i]) {
			held = append(held, interfaces.Items[i])
		}
	}
	return held, nil
}

// isServiceMember reports whether an interface is capacity a service can serve
// from. An interface a workload has released keeps its labels and its
// addresses, and nothing answers on them, so only a held one is a member.
func isServiceMember(iface *networkingv1alpha.NetworkInterface) bool {
	return iface.Status.Phase == networkingv1alpha.NetworkInterfacePhaseBound
}

// isServiceMemberHealthy reports whether a member is taking traffic, which
// whatever holds the interface says by reporting itself available to serve.
// Nothing here knows what a holder is, only that it has spoken.
func isServiceMemberHealthy(iface *networkingv1alpha.NetworkInterface) bool {
	return apimeta.IsStatusConditionTrue(iface.Status.Conditions, networkingv1alpha.NetworkInterfaceHolderAvailable)
}

func memberNetworks(members []networkingv1alpha.NetworkInterface) []string {
	var networks []string
	for i := range members {
		name := members[i].Spec.Network.Name
		if !slices.Contains(networks, name) {
			networks = append(networks, name)
		}
	}
	slices.Sort(networks)
	return networks
}

// summarizeMembership rolls the matched interfaces up per location. An
// interface with no location label belongs to no location and is returned
// separately: it counts as a member, and no location can be told to serve it.
func summarizeMembership(members []networkingv1alpha.NetworkInterface) (
	[]networkingv1alpha.NetworkServiceLocationStatus,
	networkingv1alpha.NetworkServiceSummary,
	int32,
) {
	byLocation := map[string]*networkingv1alpha.NetworkServiceLocationStatus{}
	var unlocated int32

	for i := range members {
		member := &members[i]
		location := member.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]
		if location == "" {
			unlocated++
			continue
		}

		entry, ok := byLocation[location]
		if !ok {
			entry = &networkingv1alpha.NetworkServiceLocationStatus{Name: location}
			byLocation[location] = entry
		}
		entry.Members++
		if isServiceMemberHealthy(member) {
			entry.Healthy++
		}
	}

	locations := make([]networkingv1alpha.NetworkServiceLocationStatus, 0, len(byLocation))
	summary := networkingv1alpha.NetworkServiceSummary{Members: unlocated}
	for _, entry := range byLocation {
		entry.Serving = entry.Healthy > 0
		locations = append(locations, *entry)
		summary.Members += entry.Members
		summary.Healthy += entry.Healthy
	}
	slices.SortFunc(locations, func(a, b networkingv1alpha.NetworkServiceLocationStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
	summary.Locations = int32(len(locations))
	if len(locations) > maxNetworkServiceLocations {
		locations = locations[:maxNetworkServiceLocations]
	}

	return locations, summary, unlocated
}

func membershipMessage(summary networkingv1alpha.NetworkServiceSummary, unlocated int32) string {
	message := fmt.Sprintf("%d interfaces matched across %d locations", summary.Members, summary.Locations)
	if summary.Locations > maxNetworkServiceLocations {
		message += fmt.Sprintf(", of which %d are listed below", maxNetworkServiceLocations)
	}
	if unlocated > 0 {
		message += fmt.Sprintf(", %d of them carrying no %s label and reachable from no location",
			unlocated, networkingv1alpha.NetworkInterfaceLocationLabel)
	}
	return message
}

func setNetworkServiceCondition(
	status *networkingv1alpha.NetworkServiceStatus,
	service *networkingv1alpha.NetworkService,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
	reason string,
	message string,
) {
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: service.Generation,
	})
}

// setNetworkServiceReady summarizes the two conditions above it. Reachability
// stays unknown until real traffic says otherwise, so an unknown does not hold
// a service back; only a reported failure does.
func setNetworkServiceReady(
	status *networkingv1alpha.NetworkServiceStatus,
	service *networkingv1alpha.NetworkService,
) {
	resolved := apimeta.FindStatusCondition(status.Conditions, networkingv1alpha.NetworkServiceMembersResolved)
	if resolved == nil {
		return
	}

	if resolved.Status != metav1.ConditionTrue {
		setNetworkServiceCondition(status, service, networkingv1alpha.NetworkServiceReady,
			metav1.ConditionFalse, resolved.Reason, resolved.Message)
		return
	}

	if !slices.ContainsFunc(status.Locations, func(l networkingv1alpha.NetworkServiceLocationStatus) bool {
		return l.Serving
	}) {
		setNetworkServiceCondition(status, service, networkingv1alpha.NetworkServiceReady,
			metav1.ConditionFalse, networkingv1alpha.NetworkServiceReasonNoServingLocations,
			"No location the service has members in is taking traffic")
		return
	}

	setNetworkServiceCondition(status, service, networkingv1alpha.NetworkServiceReady,
		metav1.ConditionTrue, networkServiceReasonReady, resolved.Message)
}

// servicesMatchingInterface returns every service in the interface's namespace
// whose selector reaches it. An update is mapped from both the old and the new
// interface, so an interface relabelled out of a service still reaches the
// service it left.
func servicesMatchingInterface(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	iface, ok := obj.(*networkingv1alpha.NetworkInterface)
	if !ok {
		return nil
	}

	var services networkingv1alpha.NetworkServiceList
	if err := cl.List(ctx, &services, client.InNamespace(iface.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed listing network services for an interface event")
		return nil
	}

	interfaceLabels := labels.Set(iface.Labels)
	requests := make([]reconcile.Request, 0, len(services.Items))
	for i := range services.Items {
		service := &services.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)
		if err != nil || !selector.Matches(interfaceLabels) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(service)})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkServiceReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkService{}, mcbuilder.WithEngageWithLocalCluster(false)).
		// Membership tracks the interfaces rather than a list, so every interface
		// event has to reach the services the interface is a member of.
		Watches(&networkingv1alpha.NetworkInterface{}, func(clusterName multicluster.ClusterName, cl cluster.Cluster) mchandler.EventHandler {
			return mchandler.ForCluster(handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					return servicesMatchingInterface(ctx, cl.GetClient(), obj)
				}), clusterName)
		}).
		Named("networkservice").
		Complete(r)
}
