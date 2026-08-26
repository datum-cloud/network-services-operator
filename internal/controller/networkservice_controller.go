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

	// networkServiceReasonNotAssessed means no request outcome has been reported
	// for the service's members. Reachability is judged from real traffic, so a
	// service nothing has asked for yet is unknown rather than unreachable.
	networkServiceReasonNotAssessed = "NotAssessed"
)

// NetworkServiceReconciler resolves a NetworkService's membership from the
// network interface claims its selector matches.
type NetworkServiceReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims,verbs=get;list;watch

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

	selector, selectorErr := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaceClaims.Selector)

	var claims []networkingv1alpha.NetworkInterfaceClaim
	if selectorErr == nil {
		matched, err := matchingClaims(ctx, cl, service.Namespace, selector)
		if err != nil {
			return err
		}
		claims = matched
	}

	switch {
	case selectorErr != nil:
		setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
			metav1.ConditionFalse, networkServiceReasonInvalidSelector,
			fmt.Sprintf("The claim selector cannot be evaluated: %v", selectorErr))

	case len(claims) == 0:
		setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
			metav1.ConditionFalse, networkingv1alpha.NetworkServiceReasonNoMatchingClaims,
			"The selector matched no network interface claim")

	default:
		if networks := claimNetworks(claims); len(networks) > 1 {
			setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
				metav1.ConditionFalse, networkingv1alpha.NetworkServiceReasonMultipleNetworks,
				fmt.Sprintf("The selector matched claims on %d networks (%s), and a service spans one network",
					len(networks), strings.Join(networks, ", ")))
			break
		}

		locations, summary, unlocated := summarizeMembership(claims)
		status.Locations = locations
		status.Summary = summary
		setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceMembersResolved,
			metav1.ConditionTrue, networkServiceReasonResolved, membershipMessage(summary, unlocated))
	}

	setNetworkServiceCondition(&status, &service, networkingv1alpha.NetworkServiceEndpointsReachable,
		metav1.ConditionUnknown, networkServiceReasonNotAssessed,
		"No request outcome has been reported for the service's members")

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

// matchingClaims never leaves the service's own namespace. A selector reaches
// only the claims the consumer owns.
func matchingClaims(
	ctx context.Context,
	cl client.Client,
	namespace string,
	selector labels.Selector,
) ([]networkingv1alpha.NetworkInterfaceClaim, error) {
	var claims networkingv1alpha.NetworkInterfaceClaimList
	if err := cl.List(ctx, &claims,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed listing network interface claims: %w", err)
	}
	return claims.Items, nil
}

func claimNetworks(claims []networkingv1alpha.NetworkInterfaceClaim) []string {
	var networks []string
	for i := range claims {
		name := claims[i].Spec.Network.Name
		if !slices.Contains(networks, name) {
			networks = append(networks, name)
		}
	}
	slices.Sort(networks)
	return networks
}

// summarizeMembership rolls the matched claims up per location. A claim with no
// location label belongs to no location and is returned separately: it counts
// as a member, and no location can be told to serve it.
func summarizeMembership(claims []networkingv1alpha.NetworkInterfaceClaim) (
	[]networkingv1alpha.NetworkServiceLocationStatus,
	networkingv1alpha.NetworkServiceSummary,
	int32,
) {
	byLocation := map[string]*networkingv1alpha.NetworkServiceLocationStatus{}
	var unlocated int32

	for i := range claims {
		claim := &claims[i]
		location := claim.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]
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
		if apimeta.IsStatusConditionTrue(claim.Status.Conditions, networkingv1alpha.NetworkInterfaceClaimProgrammed) {
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
	message := fmt.Sprintf("%d claims matched across %d locations", summary.Members, summary.Locations)
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

	if apimeta.IsStatusConditionFalse(status.Conditions, networkingv1alpha.NetworkServiceEndpointsReachable) {
		setNetworkServiceCondition(status, service, networkingv1alpha.NetworkServiceReady,
			metav1.ConditionFalse, networkingv1alpha.NetworkServiceReasonMembersUnreachable,
			"The edge is failing requests to the service's members")
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

// servicesMatchingClaim returns every service in the claim's namespace whose
// selector reaches it. An update is mapped from both the old and the new claim,
// so a claim relabelled out of a service still reaches the service it left.
func servicesMatchingClaim(ctx context.Context, cl client.Client, obj client.Object) []reconcile.Request {
	claim, ok := obj.(*networkingv1alpha.NetworkInterfaceClaim)
	if !ok {
		return nil
	}

	var services networkingv1alpha.NetworkServiceList
	if err := cl.List(ctx, &services, client.InNamespace(claim.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed listing network services for a claim event")
		return nil
	}

	claimLabels := labels.Set(claim.Labels)
	requests := make([]reconcile.Request, 0, len(services.Items))
	for i := range services.Items {
		service := &services.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaceClaims.Selector)
		if err != nil || !selector.Matches(claimLabels) {
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
		// Membership tracks the claims rather than a list, so every claim event
		// has to reach the services the claim is a member of.
		Watches(&networkingv1alpha.NetworkInterfaceClaim{}, func(clusterName multicluster.ClusterName, cl cluster.Cluster) mchandler.EventHandler {
			return mchandler.ForCluster(handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					return servicesMatchingClaim(ctx, cl.GetClient(), obj)
				}), clusterName)
		}).
		Named("networkservice").
		Complete(r)
}
