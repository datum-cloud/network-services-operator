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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	nlbIntentFinalizer     = "networking.datumapis.com/nlb-cell-intent-withdrawal"
	nlbIntentRouteUIDLabel = "networking.datumapis.com/route-uid"
	nlbIntentRetry         = 30 * time.Second
)

type NetworkLoadBalancerCellIntentReconciler struct {
	HubCluster cluster.Cluster
	mgr        mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers;networkservices;networkinterfaces;networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancercellintents,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *NetworkLoadBalancerCellIntentReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	return r.reconcileIntent(ctx, string(req.ClusterName), cl.GetClient(), req.NamespacedName)
}

func (r *NetworkLoadBalancerCellIntentReconciler) reconcileIntent(ctx context.Context, project string, projectClient client.Client, key client.ObjectKey) (ctrl.Result, error) {
	var route networkingv1alpha.NetworkLoadBalancerRoute
	if err := projectClient.Get(ctx, key, &route); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if route.UID == "" {
		return ctrl.Result{}, fmt.Errorf("route %s has no UID", key)
	}

	if route.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(&route, nlbIntentFinalizer) {
		if err := projectClient.Update(ctx, &route); err != nil {
			return ctrl.Result{}, fmt.Errorf("hold route until cell intent withdrawal: %w", err)
		}
	}
	if !route.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(&route, nlbIntentFinalizer) {
		return ctrl.Result{}, nil
	}

	strategy := downstreamclient.NewMappedNamespaceResourceStrategy(project, projectClient, r.HubCluster.GetClient())
	hubNamespace, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, route.Namespace)
	if apierrors.IsNotFound(err) {
		if !route.DeletionTimestamp.IsZero() {
			controllerutil.RemoveFinalizer(&route, nlbIntentFinalizer)
			return ctrl.Result{}, projectClient.Update(ctx, &route)
		}
		return ctrl.Result{RequeueAfter: nlbIntentRetry}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve hub namespace: %w", err)
	}

	var desired []networkingv1alpha.NetworkLoadBalancerCellIntent
	if route.DeletionTimestamp.IsZero() {
		desired, err = buildNetworkLoadBalancerCellIntents(ctx, projectClient, project, hubNamespace, &route)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := reconcileHubCellIntents(ctx, r.HubCluster.GetClient(), hubNamespace, project, &route, desired); err != nil {
		return ctrl.Result{}, err
	}
	if !route.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(&route, nlbIntentFinalizer)
		if err := projectClient.Update(ctx, &route); err != nil {
			return ctrl.Result{}, fmt.Errorf("release withdrawn route: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

func buildNetworkLoadBalancerCellIntents(ctx context.Context, cl client.Client, project, hubNamespace string, route *networkingv1alpha.NetworkLoadBalancerRoute) ([]networkingv1alpha.NetworkLoadBalancerCellIntent, error) {
	var peers networkingv1alpha.NetworkLoadBalancerRouteList
	if err := cl.List(ctx, &peers, client.InNamespace(route.Namespace)); err != nil {
		return nil, fmt.Errorf("list route owners: %w", err)
	}
	var desired []networkingv1alpha.NetworkLoadBalancerCellIntent
	for _, ref := range route.Spec.ParentRefs {
		var parent networkingv1alpha.NetworkLoadBalancer
		if err := cl.Get(ctx, client.ObjectKey{Namespace: route.Namespace, Name: ref.Name}, &parent); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		if !parent.DeletionTimestamp.IsZero() || parent.UID == "" {
			continue
		}
		winner := oldestRouteForListener(peers.Items, ref)
		if winner == nil || winner.UID != route.UID {
			continue
		}
		listenerIndex := slices.IndexFunc(parent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool { return l.Name == ref.Listener })
		if listenerIndex < 0 {
			continue
		}
		listener := &parent.Spec.Listeners[listenerIndex]
		var network networkingv1alpha.Network
		if err := cl.Get(ctx, client.ObjectKey{Namespace: route.Namespace, Name: parent.Spec.NetworkRef.Name}, &network); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		ready := apimeta.FindStatusCondition(network.Status.Conditions, networkingv1alpha.NetworkReady)
		if !network.DeletionTimestamp.IsZero() || ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration != network.Generation {
			continue
		}
		endpoints, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, &parent, listener)
		var refError *networkLoadBalancerRouteReferenceError
		if errors.As(err, &refError) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sharedVIPs := map[string]bool{}
		seenVIPs := map[string]bool{}
		for _, allocation := range parent.Status.Addresses {
			vip, parseErr := netip.ParseAddr(allocation.Value)
			if parseErr != nil {
				continue
			}
			address := vip.String()
			if seenVIPs[address] {
				sharedVIPs[address] = true
			}
			seenVIPs[address] = true
		}
		for _, addressRef := range listener.AddressRefs {
			requestIndex := slices.IndexFunc(parent.Spec.Exposure.Addresses, func(a networkingv1alpha.NetworkLoadBalancerAddressRequest) bool { return a.Name == addressRef })
			if requestIndex < 0 {
				continue
			}
			request := parent.Spec.Exposure.Addresses[requestIndex]
			for _, allocation := range parent.Status.Addresses {
				if allocation.RequestName != addressRef || allocation.Family != networkingv1alpha.IPv6Protocol || allocation.Value == "" ||
					allocation.ClaimRef == nil || allocation.ClaimRef.Name == "" {
					continue
				}
				allocated := apimeta.FindStatusCondition(allocation.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
				if allocated == nil || allocated.Status != metav1.ConditionTrue || allocated.ObservedGeneration != parent.Generation {
					continue
				}
				vip, parseErr := netip.ParseAddr(allocation.Value)
				if parseErr != nil || !vip.Is6() || vip.Is4In6() || vip.Zone() != "" || !vip.IsGlobalUnicast() ||
					allocation.Location == "" || sharedVIPs[vip.String()] {
					continue
				}
				location := allocation.Location
				if request.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceNames && !slices.Contains(request.Locations.Names, location) {
					continue
				}
				intent := networkingv1alpha.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{
					Namespace: hubNamespace,
					Labels: map[string]string{
						downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + strings.ReplaceAll(project, "/", "_"),
						downstreamclient.UpstreamOwnerNamespaceLabel:   route.Namespace,
						nlbIntentRouteUIDLabel:                         string(route.UID),
						networkingv1alpha.LocationLabel:                location,
					},
				}, Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
					Source: networkingv1alpha.NetworkLoadBalancerCellIntentSource{
						ProjectCluster: project, Namespace: route.Namespace, RouteName: route.Name,
						RouteUID: string(route.UID), RouteGeneration: route.Generation,
						ParentName: parent.Name, ParentUID: string(parent.UID), ParentGeneration: parent.Generation,
						ListenerName: listener.Name, AddressRequest: addressRef,
					},
					Location: location, NetworkName: parent.Spec.NetworkRef.Name,
					VIP: vip.String(), Protocol: listener.Protocol, ListenerPort: listener.Port,
					State: networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn,
				}}
				for _, endpoint := range endpoints {
					if endpoint.Location != location {
						continue
					}
					intent.Spec.Backends = append(intent.Spec.Backends, networkingv1alpha.NetworkLoadBalancerCellIntentBackend{
						SourceNamespace: endpoint.SourceNamespace, SourceName: endpoint.Interface,
						SourceUID: endpoint.SourceUID, Address: endpoint.Address, Port: endpoint.Port,
						Weight: endpoint.Weight, Probe: endpoint.Probe,
					})
				}
				if len(intent.Spec.Backends) > 1024 {
					intent.Spec.Backends = nil
				}
				if len(intent.Spec.Backends) != 0 {
					intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentValidate
				}
				intent.Name = networkLoadBalancerCellIntentName(intent.Spec)
				desired = append(desired, intent)
			}
		}
	}
	return desired, nil
}

func networkLoadBalancerCellIntentName(spec networkingv1alpha.NetworkLoadBalancerCellIntentSpec) string {
	identity := fmt.Sprintf("%s/%s/%s/%s/%s/%s/%s/%s/%d", spec.Source.ProjectCluster, spec.Source.Namespace,
		spec.Source.RouteUID, spec.Source.ParentUID, spec.Source.ListenerName, spec.Source.AddressRequest,
		spec.Location, spec.VIP, spec.ListenerPort)
	identity += "/" + string(spec.Protocol)
	sum := sha256.Sum256([]byte(identity))
	return "nlb-" + hex.EncodeToString(sum[:16])
}

func reconcileHubCellIntents(ctx context.Context, hub client.Client, namespace, project string, route *networkingv1alpha.NetworkLoadBalancerRoute, desired []networkingv1alpha.NetworkLoadBalancerCellIntent) error {
	labels := client.MatchingLabels{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + strings.ReplaceAll(project, "/", "_"),
		downstreamclient.UpstreamOwnerNamespaceLabel:   route.Namespace,
		nlbIntentRouteUIDLabel:                         string(route.UID),
	}
	var existing networkingv1alpha.NetworkLoadBalancerCellIntentList
	if err := hub.List(ctx, &existing, client.InNamespace(namespace), labels); err != nil {
		return fmt.Errorf("list published cell intents: %w", err)
	}
	wanted := make(map[string]networkingv1alpha.NetworkLoadBalancerCellIntent, len(desired))
	for _, intent := range desired {
		wanted[intent.Name] = intent
	}
	for i := range existing.Items {
		intent := &existing.Items[i]
		if _, ok := wanted[intent.Name]; ok {
			continue
		}
		if intent.Spec.State == networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn && len(intent.Spec.Backends) == 0 {
			continue
		}
		intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn
		intent.Spec.Backends = nil
		if err := hub.Update(ctx, intent); err != nil {
			return fmt.Errorf("withdraw old cell intent %s: %w", intent.Name, err)
		}
	}
	for _, wantedIntent := range desired {
		intent := &networkingv1alpha.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Name: wantedIntent.Name, Namespace: namespace}}
		_, err := controllerutil.CreateOrUpdate(ctx, hub, intent, func() error {
			if intent.CreationTimestamp.IsZero() && intent.UID == "" || intent.Spec.Source.RouteUID == wantedIntent.Spec.Source.RouteUID {
				intent.Labels = wantedIntent.Labels
				intent.Spec = wantedIntent.Spec
				return nil
			}
			return fmt.Errorf("cell intent name %s belongs to another route", intent.Name)
		})
		if err != nil {
			return fmt.Errorf("publish cell intent %s: %w", wantedIntent.Name, err)
		}
	}
	return nil
}

func (r *NetworkLoadBalancerCellIntentReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.HubCluster == nil {
		return errors.New("a hub cluster is required")
	}
	r.mgr = mgr
	watch := func(mapper func(context.Context, client.Client, client.Object) []ctrl.Request) func(multicluster.ClusterName, cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		return func(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
			return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
				requests := mapper(ctx, cl.GetClient(), obj)
				out := make([]mcreconcile.Request, 0, len(requests))
				for _, req := range requests {
					out = append(out, mcreconcile.Request{ClusterName: clusterName, Request: req})
				}
				return out
			})
		}
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancerRoute{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Watches(&networkingv1alpha.NetworkLoadBalancerRoute{}, watch(siblingRoutesForRouteEvent)).
		Watches(&networkingv1alpha.NetworkLoadBalancer{}, watch(routesForLoadBalancerEvent)).
		Watches(&networkingv1alpha.NetworkService{}, watch(routesForNetworkServiceEvent)).
		Watches(&networkingv1alpha.NetworkInterface{}, watch(routesForInterfaceEvent)).
		Watches(&networkingv1alpha.Network{}, watch(routesForInterfaceEvent)).
		Named("networkloadbalancercellintent").
		Complete(r)
}
