// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type authoritativeNLBClient struct {
	client.Client
	reader client.Reader
}

func (c authoritativeNLBClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.reader.Get(ctx, key, obj, opts...)
}
func (c authoritativeNLBClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.reader.List(ctx, list, opts...)
}

func nlbAuthoritativeClient(c client.Client, reader client.Reader) client.Client {
	if reader == nil {
		return c
	}
	return authoritativeNLBClient{Client: c, reader: reader}
}

func parentRouteIntentFinalizer(parent string) string {
	sum := sha256.Sum256([]byte(parent))
	return fmt.Sprintf("networking.datumapis.com/nlb-parent-%x", sum[:8])
}

func (r *NetworkLoadBalancerCellIntentReconciler) reconcileParentIntents(ctx context.Context, project string, projectClient client.Client, key client.ObjectKey) (ctrl.Result, error) {
	hub := nlbAuthoritativeClient(r.HubCluster.GetClient(), r.HubCluster.GetAPIReader())
	var parent api.NetworkLoadBalancer
	err := projectClient.Get(ctx, key, &parent)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	present := err == nil
	var routes api.NetworkLoadBalancerRouteList
	if err := projectClient.List(ctx, &routes, client.InNamespace(key.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	var desired []api.NetworkLoadBalancerCellIntent
	namespacePending := false
	if present && parent.DeletionTimestamp.IsZero() {
		strategy := downstreamclient.NewMappedNamespaceResourceStrategy(project, projectClient, hub)
		namespace, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, key.Namespace)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		namespacePending = apierrors.IsNotFound(err)
		if err == nil {
			for i := range routes.Items {
				route := &routes.Items[i]
				if !route.DeletionTimestamp.IsZero() || !routeReferencesParent(route, key.Name) {
					continue
				}
				finalizer := parentRouteIntentFinalizer(key.Name)
				changed := controllerutil.AddFinalizer(route, finalizer)
				if route.Annotations[finalizer] != key.Name {
					if route.Annotations == nil {
						route.Annotations = map[string]string{}
					}
					route.Annotations[finalizer] = key.Name
					changed = true
				}
				if changed {
					if err := projectClient.Update(ctx, route); err != nil {
						return ctrl.Result{}, err
					}
				}
				wanted, err := buildNetworkLoadBalancerCellIntents(ctx, projectClient, project, namespace, route)
				if err != nil {
					return ctrl.Result{}, err
				}
				for _, intent := range wanted {
					if intent.Spec.Source.ParentUID == string(parent.UID) {
						desired = append(desired, intent)
					}
				}
			}
		}
	}
	retirementPending := false
	if present {
		retirementPending, err = r.publishAllocationAuthorities(ctx, projectClient, hub, project, &parent)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	pending, err := reconcileParentHubIntents(ctx, hub, project, key, desired)
	if err != nil {
		return ctrl.Result{}, err
	}
	current, err := parentPublishedIntents(ctx, hub, project, key)
	if err != nil {
		return ctrl.Result{}, err
	}
	if present {
		if err := acknowledgeRetiredAllocations(ctx, projectClient, &parent, current); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := releaseParentRouteFinalizers(ctx, projectClient, hub, project, key, routes.Items, current, !present || !parent.DeletionTimestamp.IsZero()); err != nil {
		return ctrl.Result{}, err
	}
	if pending || namespacePending || retirementPending {
		return ctrl.Result{RequeueAfter: nlbIntentRetry}, nil
	}
	return ctrl.Result{}, nil
}

func routeReferencesParent(route *api.NetworkLoadBalancerRoute, parent string) bool {
	return slices.ContainsFunc(route.Spec.ParentRefs, func(ref api.NetworkLoadBalancerRouteParentRef) bool {
		return ref.Name == parent && (ref.Namespace == "" || ref.Namespace == route.Namespace)
	})
}

func parentPublishedIntents(ctx context.Context, hub client.Client, project string, key client.ObjectKey) ([]api.NetworkLoadBalancerCellIntent, error) {
	var list api.NetworkLoadBalancerCellIntentList
	if err := hub.List(ctx, &list, client.MatchingLabels{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + strings.ReplaceAll(project, "/", "_"),
		downstreamclient.UpstreamOwnerNamespaceLabel:   key.Namespace,
	}); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(list.Items, func(intent api.NetworkLoadBalancerCellIntent) bool {
		src := intent.Spec.Source
		return src.ProjectCluster != project || src.Namespace != key.Namespace || src.ParentName != key.Name
	}), nil
}

func reconcileParentHubIntents(ctx context.Context, hub client.Client, project string, key client.ObjectKey, desired []api.NetworkLoadBalancerCellIntent) (bool, error) {
	existing, err := parentPublishedIntents(ctx, hub, project, key)
	if err != nil {
		return false, err
	}
	wanted := make(map[string]api.NetworkLoadBalancerCellIntent, len(desired))
	for _, intent := range desired {
		wanted[intent.Name] = intent
	}
	pending := false
	for i := range existing {
		intent := &existing[i]
		if intent.Spec.Allocation != nil {
			continue
		}
		if _, keep := wanted[intent.Name]; keep {
			continue
		}
		if intentWithdrawalAcknowledged(intent) {
			if err := deleteAcknowledgedIntent(ctx, hub, intent); err != nil {
				return false, err
			}
			continue
		}
		pending = true
		if intent.Spec.State == api.NetworkLoadBalancerCellIntentWithdrawn && len(intent.Spec.Backends) == 0 {
			continue
		}
		intent.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
		intent.Spec.Backends = nil
		if err := hub.Update(ctx, intent); err != nil {
			return false, err
		}
	}
	for _, want := range desired {
		current := &api.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Name: want.Name, Namespace: want.Namespace}}
		_, err := controllerutil.CreateOrUpdate(ctx, hub, current, func() error {
			if current.UID != "" && (current.Spec.Source.RouteUID != want.Spec.Source.RouteUID || current.Spec.Source.ParentUID != want.Spec.Source.ParentUID) {
				return fmt.Errorf("cell intent %s belongs to another source", current.Name)
			}
			current.Labels = want.Labels
			current.Spec = want.Spec
			return nil
		})
		if err != nil {
			return false, err
		}
	}
	return pending, nil
}

func acknowledgeRetiredAllocations(ctx context.Context, cl client.Client, parent *api.NetworkLoadBalancer, intents []api.NetworkLoadBalancerCellIntent) error {
	before := parent.DeepCopy()
	for i := range parent.Status.Endpoints {
		endpoint := &parent.Status.Endpoints[i]
		for j := range endpoint.Addresses {
			address := &endpoint.Addresses[j]
			if !address.Coordinated || !address.Retiring || !address.WithdrawalPending {
				continue
			}
			if !allocationRetirementConfirmed(parent, endpoint, address, intents) {
				continue
			}
			address.WithdrawalPending = false
			address.Announced = false
			for _, typ := range []string{api.NetworkLoadBalancerProgrammed, api.NetworkLoadBalancerAnnounced} {
				setLoadBalancerCondition(&address.Conditions, parent.Generation, typ, metav1.ConditionFalse, "Withdrawn", "The parent publisher confirmed every serving intent has withdrawn")
			}
		}
	}
	if equality.Semantic.DeepEqual(before.Status, parent.Status) {
		return nil
	}
	return cl.Status().Patch(ctx, parent, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func releaseParentRouteFinalizers(ctx context.Context, cl, hub client.Client, project string, key client.ObjectKey, routes []api.NetworkLoadBalancerRoute, intents []api.NetworkLoadBalancerCellIntent, parentGone bool) error {
	for i := range routes {
		route := &routes[i]
		if !parentGone && route.DeletionTimestamp.IsZero() && routeReferencesParent(route, key.Name) {
			continue
		}
		if slices.ContainsFunc(intents, func(intent api.NetworkLoadBalancerCellIntent) bool {
			return intent.Spec.Source.RouteUID == string(route.UID) && !intentWithdrawalAcknowledged(&intent)
		}) {
			continue
		}
		finalizer := parentRouteIntentFinalizer(key.Name)
		changed := controllerutil.RemoveFinalizer(route, finalizer)
		if _, exists := route.Annotations[finalizer]; exists {
			delete(route.Annotations, finalizer)
			changed = true
		}
		if !route.DeletionTimestamp.IsZero() && !slices.ContainsFunc(route.Finalizers, func(value string) bool { return strings.HasPrefix(value, "networking.datumapis.com/nlb-parent-") }) {
			var all api.NetworkLoadBalancerCellIntentList
			if err := hub.List(ctx, &all, client.MatchingLabels{nlbIntentRouteUIDLabel: string(route.UID)}); err != nil {
				return err
			}
			pending := slices.ContainsFunc(all.Items, func(intent api.NetworkLoadBalancerCellIntent) bool {
				return intent.Spec.Source.ProjectCluster == project && intent.Spec.Source.Namespace == key.Namespace && !intentWithdrawalAcknowledged(&intent)
			})
			if !pending {
				changed = controllerutil.RemoveFinalizer(route, nlbIntentFinalizer) || changed
			}
		}
		if changed {
			if err := cl.Update(ctx, route); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return err
			}
		}
	}
	return nil
}

func loadBalancersForIntentEvent(ctx context.Context, cl client.Client, obj client.Object) []ctrl.Request {
	var parents api.NetworkLoadBalancerList
	if err := cl.List(ctx, &parents, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, parent := range parents.Items {
		names[parent.Name] = true
	}
	if route, ok := obj.(*api.NetworkLoadBalancerRoute); ok {
		for annotation, parent := range route.Annotations {
			if strings.HasPrefix(annotation, "networking.datumapis.com/nlb-parent-") && parent != "" {
				names[parent] = true
			}
		}
		for _, ref := range route.Spec.ParentRefs {
			if ref.Namespace == "" || ref.Namespace == route.Namespace {
				names[ref.Name] = true
			}
		}
	}
	out := make([]ctrl.Request, 0, len(names))
	for name := range names {
		out = append(out, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: name}})
	}
	return out
}
