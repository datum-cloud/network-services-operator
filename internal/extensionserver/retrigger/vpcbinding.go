// SPDX-License-Identifier: AGPL-3.0-only

package retrigger

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// legacyVPCBindingAnnotationKey is the Gateway annotation an earlier version
// stamped. Envoy Gateway never read it, so it is only removed.
const legacyVPCBindingAnnotationKey = "networking.datumapis.com/vpc-binding-generation"

// VPCBindingReconciler records, in an HTTPProxyVPCBinding next to each
// HTTPProxy, the VPC each of its backends resolves to on this edge.
//
// Envoy Gateway watches HTTPProxyVPCBinding as an extension policy, so a
// change re-runs translation and the extension hook. It ignores the
// tenant-labelled EndpointSlices the VPC is resolved from, because they name
// no Service, so a slice that reaches the edge after the route was translated
// would otherwise leave the route unbound until an unrelated change.
type VPCBindingReconciler struct {
	Client client.Client
}

func (r *VPCBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var proxy networkingv1alpha.HTTPProxy
	if err := r.Client.Get(ctx, req.NamespacedName, &proxy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.removeLegacyAnnotation(ctx, req.NamespacedName); err != nil {
		return ctrl.Result{}, err
	}

	binding := &networkingv1alpha.HTTPProxyVPCBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: proxy.Namespace, Name: proxy.Name},
	}
	if !extcache.HasVPCBackend(&proxy) {
		return ctrl.Result{}, client.IgnoreNotFound(r.Client.Delete(ctx, binding))
	}

	tenants, err := extcache.VPCBackendTenants(ctx, r.Client, &proxy)
	if err != nil {
		return ctrl.Result{}, err
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Spec = vpcBindingSpec(&proxy, tenants)
		return controllerutil.SetControllerReference(&proxy, binding, r.Client.Scheme())
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("write vpc binding %s: %w", req.NamespacedName, err)
	}
	if result != controllerutil.OperationResultNone {
		log.FromContext(ctx).V(1).Info("wrote vpc binding", "binding", req.NamespacedName, "result", result)
	}
	return ctrl.Result{}, nil
}

func (r *VPCBindingReconciler) removeLegacyAnnotation(ctx context.Context, key client.ObjectKey) error {
	gw := extcache.GatewayMetadata()
	if err := r.Client.Get(ctx, key, gw); err != nil {
		return client.IgnoreNotFound(err)
	}
	if _, ok := gw.Annotations[legacyVPCBindingAnnotationKey]; !ok {
		return nil
	}
	body := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:null}}}`, legacyVPCBindingAnnotationKey)
	return client.IgnoreNotFound(r.Client.Patch(ctx, gw, client.RawPatch(types.MergePatchType, body)))
}

func vpcBindingSpec(proxy *networkingv1alpha.HTTPProxy, tenants map[extcache.VPCPodBackendKey]string) networkingv1alpha.HTTPProxyVPCBindingSpec {
	spec := networkingv1alpha.HTTPProxyVPCBindingSpec{
		TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
			LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
				Group: gatewayv1.GroupName,
				Kind:  "Gateway",
				Name:  gatewayv1.ObjectName(proxy.Name),
			},
		}},
	}
	for key, tenant := range tenants {
		spec.Backends = append(spec.Backends, networkingv1alpha.HTTPProxyVPCBindingBackend{
			Rule:     int32(key.RuleIndex),
			Backend:  int32(key.BackendIndex),
			TenantID: tenant,
		})
	}
	slices.SortFunc(spec.Backends, func(a, b networkingv1alpha.HTTPProxyVPCBindingBackend) int {
		return cmp.Or(cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Backend, b.Backend))
	})
	return spec
}

func (r *VPCBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.HTTPProxy{}).
		Owns(&networkingv1alpha.HTTPProxyVPCBinding{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&discoveryv1.EndpointSlice{},
			handler.EnqueueRequestsFromMapFunc(r.proxiesForEndpointSlice),
			builder.WithPredicates(endpointSlicePredicate()),
		).
		Named("extension-server-vpc-binding").
		Complete(r)
}

func (r *VPCBindingReconciler) proxiesForEndpointSlice(ctx context.Context, obj client.Object) []reconcile.Request {
	slice, ok := obj.(*discoveryv1.EndpointSlice)
	if !ok {
		return nil
	}
	logger := log.FromContext(ctx)

	owners := map[client.ObjectKey]struct{}{}
	if owner := slice.Labels[downstreamclient.UpstreamOwnerNameLabel]; owner != "" {
		owners[client.ObjectKey{Namespace: slice.Namespace, Name: owner}] = struct{}{}
	}

	if _, ok := slice.Labels[extcache.VPCPodTenantIDLabel]; ok {
		addresses := sliceAddresses(slice)
		var copies discoveryv1.EndpointSliceList
		if err := r.Client.List(ctx, &copies, client.HasLabels{downstreamclient.UpstreamOwnerNameLabel}); err != nil {
			logger.Error(err, "list backend endpointslices for vpc binding")
			return nil
		}
		for i := range copies.Items {
			if slices.ContainsFunc(sliceAddresses(&copies.Items[i]), func(a string) bool { return slices.Contains(addresses, a) }) {
				owners[client.ObjectKey{
					Namespace: copies.Items[i].Namespace,
					Name:      copies.Items[i].Labels[downstreamclient.UpstreamOwnerNameLabel],
				}] = struct{}{}
			}
		}
	}

	namespaces := map[string]struct{}{slice.Namespace: {}}
	for owner := range owners {
		namespaces[owner.Namespace] = struct{}{}
	}

	var requests []reconcile.Request
	for namespace := range namespaces {
		var proxies networkingv1alpha.HTTPProxyList
		if err := r.Client.List(ctx, &proxies, client.InNamespace(namespace)); err != nil {
			logger.Error(err, "list HTTPProxies for vpc binding", "namespace", namespace)
			continue
		}
		for i := range proxies.Items {
			if proxyUsesSlice(&proxies.Items[i], slice, owners) {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&proxies.Items[i])})
			}
		}
	}
	return requests
}

func proxyUsesSlice(proxy *networkingv1alpha.HTTPProxy, slice *discoveryv1.EndpointSlice, owners map[client.ObjectKey]struct{}) bool {
	for ruleIndex, rule := range proxy.Spec.Rules {
		for backendIndex, backend := range rule.Backends {
			switch {
			case backend.Instance != nil:
				if proxy.Namespace == slice.Namespace && backend.Instance.Name == slice.Name {
					return true
				}
			case backend.NetworkService != nil:
				if _, ok := owners[extcache.NetworkServiceBackendSliceKey(proxy, ruleIndex, backendIndex)]; ok {
					return true
				}
			}
		}
	}
	return false
}

func sliceAddresses(slice *discoveryv1.EndpointSlice) []string {
	var addresses []string
	for _, endpoint := range slice.Endpoints {
		for _, address := range endpoint.Addresses {
			if ip := net.ParseIP(address); ip != nil {
				addresses = append(addresses, ip.String())
			}
		}
	}
	return addresses
}

func isVPCBindingInput(obj client.Object) bool {
	labels := obj.GetLabels()
	if _, ok := labels[extcache.VPCPodTenantIDLabel]; ok {
		return true
	}
	return labels[downstreamclient.UpstreamOwnerNameLabel] != ""
}

func vpcBindingInputChanged(oldSlice, newSlice *discoveryv1.EndpointSlice) bool {
	return oldSlice.Labels[extcache.VPCPodTenantIDLabel] != newSlice.Labels[extcache.VPCPodTenantIDLabel] ||
		oldSlice.Labels[downstreamclient.UpstreamOwnerNameLabel] != newSlice.Labels[downstreamclient.UpstreamOwnerNameLabel] ||
		!equality.Semantic.DeepEqual(sliceAddresses(oldSlice), sliceAddresses(newSlice))
}

func endpointSlicePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isVPCBindingInput(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isVPCBindingInput(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isVPCBindingInput(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldSlice, ok1 := e.ObjectOld.(*discoveryv1.EndpointSlice)
			newSlice, ok2 := e.ObjectNew.(*discoveryv1.EndpointSlice)
			if !ok1 || !ok2 {
				return true
			}
			if !isVPCBindingInput(oldSlice) && !isVPCBindingInput(newSlice) {
				return false
			}
			return vpcBindingInputChanged(oldSlice, newSlice)
		},
	}
}
