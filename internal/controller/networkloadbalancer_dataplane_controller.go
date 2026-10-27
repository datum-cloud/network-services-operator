// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	nlbReasonPendingDataPlane = "PendingDataPlane"
	nlbReasonNotServed        = "NotServed"
)

// NetworkLoadBalancerDataPlaneReconciler reports what serving locations say
// about a load balancer's addresses. Locations write status on their copy of
// each cell intent, and the hub aggregates it onto the hub copy.
type NetworkLoadBalancerDataPlaneReconciler struct {
	HubCluster cluster.Cluster
	mgr        mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancercellintents,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *NetworkLoadBalancerDataPlaneReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.reconcileDataPlane(ctx, string(req.ClusterName), cl.GetClient(), req.NamespacedName)
}

func (r *NetworkLoadBalancerDataPlaneReconciler) reconcileDataPlane(
	ctx context.Context, project string, projectClient client.Client, key client.ObjectKey,
) error {
	var lb networkingv1alpha.NetworkLoadBalancer
	if err := projectClient.Get(ctx, key, &lb); err != nil {
		return client.IgnoreNotFound(err)
	}

	hub := r.HubCluster.GetClient()
	strategy := downstreamclient.NewMappedNamespaceResourceStrategy(project, projectClient, hub)
	hubNamespace, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, lb.Namespace)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve hub namespace: %w", err)
	}
	var published networkingv1alpha.NetworkLoadBalancerCellIntentList
	if err := hub.List(ctx, &published, client.InNamespace(hubNamespace), client.MatchingLabels{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + strings.ReplaceAll(project, "/", "_"),
		downstreamclient.UpstreamOwnerNamespaceLabel:   lb.Namespace,
	}); err != nil {
		return fmt.Errorf("list published cell intents: %w", err)
	}
	var intents []networkingv1alpha.NetworkLoadBalancerCellIntent
	for _, intent := range published.Items {
		if intent.Spec.Source.ParentUID == string(lb.UID) {
			intents = append(intents, intent)
		}
	}

	var routes networkingv1alpha.NetworkLoadBalancerRouteList
	if err := projectClient.List(ctx, &routes, client.InNamespace(lb.Namespace)); err != nil {
		return fmt.Errorf("list load balancer routes: %w", err)
	}

	before := lb.DeepCopy()
	applyNetworkLoadBalancerDataPlane(&lb, intents, routes.Items)
	if equality.Semantic.DeepEqual(before.Status, lb.Status) {
		return nil
	}
	if err := projectClient.Status().Patch(ctx, &lb, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("report load balancer data plane status: %w", err)
	}
	return nil
}

func applyNetworkLoadBalancerDataPlane(
	lb *networkingv1alpha.NetworkLoadBalancer,
	intents []networkingv1alpha.NetworkLoadBalancerCellIntent,
	routes []networkingv1alpha.NetworkLoadBalancerRoute,
) {
	var addresses []*networkingv1alpha.NetworkLoadBalancerAddressStatus
	for i := range lb.Status.Endpoints {
		endpoint := &lb.Status.Endpoints[i]
		for j := range endpoint.Addresses {
			address := &endpoint.Addresses[j]
			if address.Value == "" {
				continue
			}
			addresses = append(addresses, address)
			programmed, announced, known := addressDataPlane(intents, endpoint.RequestName, endpoint.Location, address.Value)
			if !known {
				continue
			}
			if programmed.Reason == nlbReasonNotServed {
				if rejection, ok := routeRejection(lb, routes, endpoint.RequestName); ok {
					programmed, announced = rejection, rejection
				}
			}
			setLoadBalancerCondition(&address.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerProgrammed,
				programmed.Status, programmed.Reason, programmed.Message)
			setLoadBalancerCondition(&address.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerAnnounced,
				announced.Status, announced.Reason, announced.Message)
			address.Announced = announced.Status == metav1.ConditionTrue
		}
	}
	if len(addresses) == 0 || !apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated) {
		return
	}

	programmed := combineAddressConditions(addresses, networkingv1alpha.NetworkLoadBalancerProgrammed)
	setLoadBalancerCondition(&lb.Status.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerProgrammed,
		programmed.Status, programmed.Reason, programmed.Message)
	ready := programmed
	if programmed.Status == metav1.ConditionTrue {
		ready = combineAddressConditions(addresses, networkingv1alpha.NetworkLoadBalancerAnnounced)
	}
	if ready.Status == metav1.ConditionTrue {
		ready.Reason = "Ready"
		ready.Message = "Every address is programmed and announced"
	}
	setLoadBalancerCondition(&lb.Status.Conditions, lb.Generation, networkingv1alpha.NetworkLoadBalancerReady,
		ready.Status, ready.Reason, ready.Message)
}

// addressDataPlane reports one address's state from the intents that serve it.
// With none serving, it reports the address unserved only once every
// withdrawn intent has confirmed its withdrawal, so an address is never
// released while a location may still serve it.
func addressDataPlane(
	intents []networkingv1alpha.NetworkLoadBalancerCellIntent, requestName, location, vip string,
) (programmed, announced metav1.Condition, known bool) {
	var serving, withdrawn []*networkingv1alpha.NetworkLoadBalancerCellIntent
	for i := range intents {
		intent := &intents[i]
		if intent.Spec.Source.AddressRequest != requestName || intent.Spec.Location != location || intent.Spec.VIP != vip {
			continue
		}
		if intent.Spec.State == networkingv1alpha.NetworkLoadBalancerCellIntentValidate {
			serving = append(serving, intent)
		} else {
			withdrawn = append(withdrawn, intent)
		}
	}
	if len(serving) > 0 {
		return combineIntentConditions(serving, networkingv1alpha.NetworkLoadBalancerCellIntentProgrammed),
			combineIntentConditions(serving, networkingv1alpha.NetworkLoadBalancerCellIntentAnnounced), true
	}
	for _, intent := range withdrawn {
		if !intentWithdrawalAcknowledged(intent) {
			return metav1.Condition{}, metav1.Condition{}, false
		}
	}
	unserved := metav1.Condition{Status: metav1.ConditionFalse, Reason: nlbReasonNotServed, Message: "No route serves this address"}
	return unserved, unserved, true
}

func routeRejection(
	lb *networkingv1alpha.NetworkLoadBalancer, routes []networkingv1alpha.NetworkLoadBalancerRoute, requestName string,
) (metav1.Condition, bool) {
	listeners := map[string]bool{}
	for _, listener := range lb.Spec.Listeners {
		if slices.Contains(listener.AddressRefs, requestName) {
			listeners[listener.Name] = true
		}
	}
	sorted := slices.Clone(routes)
	slices.SortFunc(sorted, func(a, b networkingv1alpha.NetworkLoadBalancerRoute) int { return strings.Compare(a.Name, b.Name) })
	for i := range sorted {
		route := &sorted[i]
		for _, parent := range route.Status.Parents {
			if parent.Name != lb.Name || !listeners[parent.Listener] {
				continue
			}
			for _, conditionType := range []string{
				networkingv1alpha.NetworkLoadBalancerRouteAccepted,
				networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs,
				networkingv1alpha.NetworkLoadBalancerRouteProgrammed,
			} {
				condition := apimeta.FindStatusCondition(parent.Conditions, conditionType)
				if condition == nil || condition.Status != metav1.ConditionFalse || condition.ObservedGeneration != route.Generation {
					continue
				}
				if conditionType != networkingv1alpha.NetworkLoadBalancerRouteAccepted && !parent.Effective {
					continue
				}
				if conditionType == networkingv1alpha.NetworkLoadBalancerRouteProgrammed && condition.Reason != nlbRouteReasonNoEligibleEndpoints {
					continue
				}
				return metav1.Condition{
					Status:  metav1.ConditionFalse,
					Reason:  condition.Reason,
					Message: fmt.Sprintf("Route %q: %s", route.Name, condition.Message),
				}, true
			}
		}
	}
	return metav1.Condition{}, false
}

func reportedIntentCondition(intent *networkingv1alpha.NetworkLoadBalancerCellIntent, conditionType string) metav1.Condition {
	pending := metav1.Condition{Status: metav1.ConditionUnknown, Reason: nlbReasonPendingDataPlane,
		Message: "Waiting for the serving location to report"}
	if intent.Status.ObservedGeneration != intent.Generation {
		return pending
	}
	reported := apimeta.FindStatusCondition(intent.Status.Conditions, conditionType)
	if reported == nil || reported.ObservedGeneration != intent.Generation {
		return pending
	}
	return metav1.Condition{Status: reported.Status, Reason: reported.Reason, Message: reported.Message}
}

func combineIntentConditions(intents []*networkingv1alpha.NetworkLoadBalancerCellIntent, conditionType string) metav1.Condition {
	conditions := make([]metav1.Condition, 0, len(intents))
	for _, intent := range intents {
		conditions = append(conditions, reportedIntentCondition(intent, conditionType))
	}
	return combineConditions(conditions, conditionType)
}

func combineAddressConditions(addresses []*networkingv1alpha.NetworkLoadBalancerAddressStatus, conditionType string) metav1.Condition {
	conditions := make([]metav1.Condition, 0, len(addresses))
	for _, address := range addresses {
		condition := apimeta.FindStatusCondition(address.Conditions, conditionType)
		if condition == nil {
			conditions = append(conditions, metav1.Condition{Status: metav1.ConditionUnknown, Reason: nlbReasonPendingDataPlane,
				Message: "Waiting for serving locations to report programming and announcements"})
			continue
		}
		conditions = append(conditions, *condition)
	}
	return combineConditions(conditions, conditionType)
}

func combineConditions(conditions []metav1.Condition, conditionType string) metav1.Condition {
	var unknown *metav1.Condition
	for i := range conditions {
		switch conditions[i].Status {
		case metav1.ConditionFalse:
			return metav1.Condition{Status: metav1.ConditionFalse, Reason: conditions[i].Reason, Message: conditions[i].Message}
		case metav1.ConditionUnknown:
			if unknown == nil {
				unknown = &conditions[i]
			}
		}
	}
	if unknown != nil {
		return metav1.Condition{Status: metav1.ConditionUnknown, Reason: unknown.Reason, Message: unknown.Message}
	}
	return metav1.Condition{Status: metav1.ConditionTrue, Reason: conditionType,
		Message: fmt.Sprintf("%s by every serving location", strings.ToLower(conditionType))}
}

func (r *NetworkLoadBalancerDataPlaneReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.HubCluster == nil {
		return fmt.Errorf("a hub cluster is required")
	}
	r.mgr = mgr
	intentSource, _, err := mcsource.TypedKind(
		&networkingv1alpha.NetworkLoadBalancerCellIntent{},
		func(multicluster.ClusterName, cluster.Cluster) handler.TypedEventHandler[*networkingv1alpha.NetworkLoadBalancerCellIntent, mcreconcile.Request] {
			return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, intent *networkingv1alpha.NetworkLoadBalancerCellIntent) []mcreconcile.Request {
				return loadBalancerForCellIntent(intent)
			})
		},
	).ForCluster("", r.HubCluster)
	if err != nil {
		return fmt.Errorf("watch hub cell intents: %w", err)
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancer{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Watches(&networkingv1alpha.NetworkLoadBalancerRoute{},
			func(clusterName multicluster.ClusterName, _ cluster.Cluster) mchandler.EventHandler {
				return mchandler.ForCluster(handler.EnqueueRequestsFromMapFunc(loadBalancersForRoute), clusterName)
			}).
		WatchesRawSource(intentSource).
		Named("networkloadbalancerdataplane").
		Complete(r)
}

func loadBalancersForRoute(_ context.Context, obj client.Object) []reconcile.Request {
	route, ok := obj.(*networkingv1alpha.NetworkLoadBalancerRoute)
	if !ok {
		return nil
	}
	var requests []reconcile.Request
	for _, ref := range route.Spec.ParentRefs {
		request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: route.Namespace, Name: ref.Name}}
		if !slices.Contains(requests, request) {
			requests = append(requests, request)
		}
	}
	return requests
}

func loadBalancerForCellIntent(intent *networkingv1alpha.NetworkLoadBalancerCellIntent) []mcreconcile.Request {
	source := intent.Spec.Source
	if source.ProjectCluster == "" || source.Namespace == "" || source.ParentName == "" {
		return nil
	}
	return []mcreconcile.Request{{
		ClusterName: multicluster.ClusterName(source.ProjectCluster),
		Request:     ctrl.Request{NamespacedName: types.NamespacedName{Namespace: source.Namespace, Name: source.ParentName}},
	}}
}
