// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

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

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers/status,verbs=get;patch;update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerroutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers;networkservices;networkinterfaces;networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancercellintents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *NetworkLoadBalancerCellIntentReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	return r.reconcileParentIntents(ctx, string(req.ClusterName), nlbAuthoritativeClient(cl.GetClient(), cl.GetAPIReader()), req.NamespacedName)
}

func buildNetworkLoadBalancerCellIntents(ctx context.Context, cl client.Client, project, hubNamespace string, route *networkingv1alpha.NetworkLoadBalancerRoute) ([]networkingv1alpha.NetworkLoadBalancerCellIntent, error) {
	var peers networkingv1alpha.NetworkLoadBalancerRouteList
	if err := cl.List(ctx, &peers, client.InNamespace(route.Namespace)); err != nil {
		return nil, fmt.Errorf("list route owners: %w", err)
	}
	var desired []networkingv1alpha.NetworkLoadBalancerCellIntent
	claims := newBackendClaimCache()
	for _, ref := range route.Spec.ParentRefs {
		if ref.Namespace != "" && ref.Namespace != route.Namespace {
			continue
		}
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
		listenerName, ok := ResolveNetworkLoadBalancerRouteListener(&parent, ref)
		if !ok {
			continue
		}
		winner := oldestRouteForListener(peers.Items, &parent, listenerName)
		if winner == nil || winner.UID != route.UID {
			continue
		}
		listenerIndex := slices.IndexFunc(parent.Spec.Listeners, func(l networkingv1alpha.NetworkLoadBalancerListener) bool { return l.Name == listenerName })
		if listenerIndex < 0 {
			continue
		}
		listener := &parent.Spec.Listeners[listenerIndex]
		if unsupportedRouteListenerCapability(route, listener) != "" || len(listener.AddressRefs) != 1 {
			continue
		}
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
		owner, err := olderRouteClaimingBackend(ctx, cl, route, &parent, listener, endpoints, peers.Items, claims)
		if err != nil {
			return nil, err
		}
		if owner != "" {
			continue
		}
		sharedVIPs := map[string]bool{}
		seenVIPs := map[string]bool{}
		for _, endpoint := range parent.Status.Endpoints {
			for _, allocation := range endpoint.Addresses {
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
		}
		for _, addressRef := range listener.AddressRefs {
			requestIndex := slices.IndexFunc(parent.Spec.Exposure.Addresses, func(a networkingv1alpha.NetworkLoadBalancerAddressRequest) bool { return a.Name == addressRef })
			if requestIndex < 0 {
				continue
			}
			request := parent.Spec.Exposure.Addresses[requestIndex]
			for _, endpointStatus := range parent.Status.Endpoints {
				if endpointStatus.RequestName != addressRef {
					continue
				}
				for _, allocation := range endpointStatus.Addresses {
					// The public ingress path currently supports IPv6 only.
					if !slices.Contains(request.Families, allocation.Family) ||
						allocation.Family != networkingv1alpha.IPv6Protocol || allocation.Value == "" ||
						allocation.ClaimRef == nil || allocation.ClaimRef.Name == "" {
						continue
					}
					if !allocationPublicationAllowed(&parent, addressRef, allocation) {
						continue
					}
					allocated := apimeta.FindStatusCondition(allocation.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
					if allocated == nil || allocated.Status != metav1.ConditionTrue || allocated.ObservedGeneration != parent.Generation {
						continue
					}
					vip, parseErr := netip.ParseAddr(allocation.Value)
					if parseErr != nil || !vip.Is6() || vip.Is4In6() || vip.Zone() != "" || !vip.IsGlobalUnicast() ||
						endpointStatus.Location == "" || sharedVIPs[vip.String()] {
						continue
					}
					location := endpointStatus.Location
					if request.Locations != nil && request.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceNames && !slices.Contains(request.Locations.Names, location) {
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
						VIPFamily: allocation.Family, VIP: vip.String(), Protocol: listener.Protocol, ListenerPort: listener.Port,
						State:             networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn,
						ListenerPortRange: listener.PortRange,
						AddressGroup:      networkLoadBalancerAddressGroup(&parent, listener, addressRef, location, allocation),
					}}
					if intent.Spec.AddressGroup != nil && allocation.ClaimRef.UID == "" {
						continue
					}
					for _, endpoint := range endpoints {
						if endpoint.Location != location {
							continue
						}
						intent.Spec.Backends = append(intent.Spec.Backends, networkingv1alpha.NetworkLoadBalancerCellIntentBackend{
							SourceNamespace: endpoint.SourceNamespace, SourceName: endpoint.Interface,
							SourceUID: endpoint.SourceUID, Family: networkingv1alpha.IPv6Protocol,
							Address: endpoint.Address, Port: endpoint.Port, PortRange: endpoint.PortRange,
							Probe: endpoint.Probe,
						})
					}
					// Galactic currently accepts at most 64 explicit backend addresses.
					// Keep the consumer and hub APIs open for larger endpoint sets,
					// but do not mark such an intent ready for this cell adapter.
					if len(intent.Spec.Backends) > 64 {
						intent.Spec.Backends = nil
					}
					if len(intent.Spec.Backends) != 0 {
						intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentValidate
						if intent.Spec.AddressGroup != nil {
							intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentValidateAllocation
						}
					}
					intent.Name = networkLoadBalancerCellIntentName(intent.Spec)
					desired = append(desired, intent)
				}
			}
		}
	}
	withdrawConflictingCellIntents(desired)
	return desired, nil
}

// Status can temporarily contain two distinct VIP allocations for one
// address request during replacement. Withdraw all conflicting intents
// until Galactic can reuse backends across VIPs.
func withdrawConflictingCellIntents(desired []networkingv1alpha.NetworkLoadBalancerCellIntent) {
	conflicts := make([]bool, len(desired))
	for i := range desired {
		for j := 0; j < i; j++ {
			a, b := &desired[i], &desired[j]
			if !cellIntentRequestsServing(a) || !cellIntentRequestsServing(b) || a.Spec.NetworkName != b.Spec.NetworkName || a.Spec.Location != b.Spec.Location || a.Spec.Protocol != b.Spec.Protocol {
				continue
			}
			for _, x := range a.Spec.Backends {
				for _, y := range b.Spec.Backends {
					if x.Address == y.Address && networkingv1alpha.PortInterval(x.Port, x.PortRange).Overlaps(networkingv1alpha.PortInterval(y.Port, y.PortRange)) {
						conflicts[i] = true
						conflicts[j] = true
					}
				}
			}
		}
	}
	for i, conflict := range conflicts {
		if conflict {
			desired[i].Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn
			desired[i].Spec.Backends = nil
		}
	}
}

func networkLoadBalancerCellIntentName(spec networkingv1alpha.NetworkLoadBalancerCellIntentSpec) string {
	identity := fmt.Sprintf("%s/%s/%s/%s/%s/%s/%s/%s/%d", spec.Source.ProjectCluster, spec.Source.Namespace,
		spec.Source.RouteUID, spec.Source.ParentUID, spec.Source.ListenerName, spec.Source.AddressRequest,
		spec.Location, spec.VIP, spec.ListenerPort)
	identity += "/" + string(spec.Protocol)
	if spec.ListenerPortRange != nil {
		identity += fmt.Sprintf("/%d-%d", spec.ListenerPortRange.Start, spec.ListenerPortRange.End)
	}
	sum := sha256.Sum256([]byte(identity))
	return "nlb-" + hex.EncodeToString(sum[:16])
}

func (r *NetworkLoadBalancerCellIntentReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.HubCluster == nil {
		return errors.New("a hub cluster is required")
	}
	r.mgr = mgr
	intentSource, _, err := mcsource.TypedKind(
		&networkingv1alpha.NetworkLoadBalancerCellIntent{},
		func(multicluster.ClusterName, cluster.Cluster) handler.TypedEventHandler[*networkingv1alpha.NetworkLoadBalancerCellIntent, mcreconcile.Request] {
			return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, intent *networkingv1alpha.NetworkLoadBalancerCellIntent) []mcreconcile.Request {
				src := intent.Spec.Source
				if src.ProjectCluster == "" || src.Namespace == "" || src.ParentName == "" {
					return nil
				}
				return []mcreconcile.Request{{ClusterName: multicluster.ClusterName(src.ProjectCluster), Request: ctrl.Request{NamespacedName: types.NamespacedName{Namespace: src.Namespace, Name: src.ParentName}}}}
			})
		},
	).ForCluster("", r.HubCluster)
	if err != nil {
		return fmt.Errorf("watch hub cell intents: %w", err)
	}
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
		For(&networkingv1alpha.NetworkLoadBalancer{}, mcbuilder.WithEngageWithLocalCluster(false)).
		WatchesRawSource(intentSource).
		Watches(&networkingv1alpha.NetworkLoadBalancerRoute{}, watch(loadBalancersForIntentEvent)).
		Watches(&networkingv1alpha.NetworkService{}, watch(loadBalancersForIntentEvent)).
		Watches(&networkingv1alpha.NetworkInterface{}, watch(loadBalancersForIntentEvent)).
		Watches(&networkingv1alpha.Network{}, watch(loadBalancersForIntentEvent)).
		Named("networkloadbalancercellintent").
		Complete(r)
}

func intentWithdrawalAcknowledged(intent *networkingv1alpha.NetworkLoadBalancerCellIntent) bool {
	if intent.Spec.Allocation != nil && reportedIntentCondition(intent, networkingv1alpha.NetworkLoadBalancerCellIntentAllocationRetired).Status != metav1.ConditionTrue {
		return false
	}
	return intent.Spec.State == networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn && len(intent.Spec.Backends) == 0 &&
		reportedIntentCondition(intent, networkingv1alpha.NetworkLoadBalancerCellIntentProgrammed).Status == metav1.ConditionFalse &&
		reportedIntentCondition(intent, networkingv1alpha.NetworkLoadBalancerCellIntentAnnounced).Status == metav1.ConditionFalse
}

func deleteAcknowledgedIntent(ctx context.Context, hub client.Client, intent *networkingv1alpha.NetworkLoadBalancerCellIntent) error {
	if intent.Spec.Allocation != nil {
		return nil
	}
	if err := hub.Delete(ctx, intent, client.Preconditions{UID: &intent.UID, ResourceVersion: &intent.ResourceVersion}); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete withdrawn cell intent %s: %w", intent.Name, err)
	}
	return nil
}

func networkLoadBalancerAddressGroup(parent *networkingv1alpha.NetworkLoadBalancer, listener *networkingv1alpha.NetworkLoadBalancerListener, request, location string, allocation networkingv1alpha.NetworkLoadBalancerAddressStatus) *networkingv1alpha.NetworkLoadBalancerCellIntentAddressGroup {
	var listeners []networkingv1alpha.NetworkLoadBalancerCellIntentAddressListener
	for _, candidate := range parent.Spec.Listeners {
		if slices.Contains(candidate.AddressRefs, request) {
			listeners = append(listeners, networkingv1alpha.NetworkLoadBalancerCellIntentAddressListener{Name: candidate.Name, Protocol: candidate.Protocol, Port: candidate.Port, PortRange: candidate.PortRange})
		}
	}
	if !allocation.Coordinated && len(listeners) < 2 && networkingv1alpha.PortInterval(listener.Port, listener.PortRange).Singleton() {
		return nil
	}
	slices.SortFunc(listeners, func(a, b networkingv1alpha.NetworkLoadBalancerCellIntentAddressListener) int {
		return strings.Compare(a.Name, b.Name)
	})
	data, _ := json.Marshal(listeners)
	revision := sha256.Sum256(data)
	claimUID := ""
	if allocation.ClaimRef != nil {
		claimUID = allocation.ClaimRef.UID
	}
	return &networkingv1alpha.NetworkLoadBalancerCellIntentAddressGroup{UID: networkLoadBalancerAllocationUID(string(parent.UID), request, location, claimUID), Generation: parent.Generation, Revision: hex.EncodeToString(revision[:]), ListenerName: listener.Name, Listeners: listeners}
}

func allocationPublicationAllowed(parent *networkingv1alpha.NetworkLoadBalancer, request string, allocation networkingv1alpha.NetworkLoadBalancerAddressStatus) bool {
	if !allocation.Coordinated && !addressRequestUsesCoordination(parent, request) {
		return true
	}
	return allocation.Coordinated && allocation.WithdrawalPending && !allocation.Retiring
}

func networkLoadBalancerAllocationUID(parentUID, request, location, claimUID string) string {
	uid := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/%s", parentUID, request, location, claimUID)))
	return hex.EncodeToString(uid[:])
}

func cellIntentRequestsServing(intent *networkingv1alpha.NetworkLoadBalancerCellIntent) bool {
	if intent.Spec.AddressGroup != nil {
		return intent.Spec.State == networkingv1alpha.NetworkLoadBalancerCellIntentValidateAllocation
	}
	return intent.Spec.State == networkingv1alpha.NetworkLoadBalancerCellIntentValidate
}
