// SPDX-License-Identifier: AGPL-3.0-only

package retrigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"strings"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// VPCBindingAnnotationKey is patched onto a Gateway to make Envoy Gateway
// re-translate when the VPC an HTTPProxy's backends resolve to changes.
const VPCBindingAnnotationKey = "networking.datumapis.com/vpc-binding-generation"

// VPCBindingReconciler stamps each HTTPProxy's Gateway with the VPC its
// backends resolve to on this edge.
//
// Envoy Gateway ignores the tenant-labelled EndpointSlices the VPC binding is
// resolved from, because they name no Service. A slice that reaches the edge
// after the route was translated would otherwise leave the route unbound
// until an unrelated change re-translated it.
type VPCBindingReconciler struct {
	Client client.Client
}

func (r *VPCBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var proxy networkingv1alpha.HTTPProxy
	if err := r.Client.Get(ctx, req.NamespacedName, &proxy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !extcache.HasVPCBackend(&proxy) {
		return ctrl.Result{}, nil
	}

	tenants, err := extcache.VPCBackendTenants(ctx, r.Client, &proxy)
	if err != nil {
		return ctrl.Result{}, err
	}

	value := vpcBindingValue(tenants)
	if err := touchGatewayAnnotation(ctx, r.Client, req.NamespacedName, VPCBindingAnnotationKey, value); err != nil {
		return ctrl.Result{}, fmt.Errorf("touch gateway %s: %w", req.NamespacedName, err)
	}
	log.FromContext(ctx).V(1).Info("stamped gateway with vpc binding", "gateway", req.NamespacedName, "binding", value)
	return ctrl.Result{}, nil
}

func vpcBindingValue(tenants map[extcache.VPCPodBackendKey]string) string {
	entries := make([]string, 0, len(tenants))
	for key, tenant := range tenants {
		entries = append(entries, fmt.Sprintf("%d/%d=%s", key.RuleIndex, key.BackendIndex, tenant))
	}
	slices.Sort(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, ",")))
	return hex.EncodeToString(sum[:])[:16]
}

func (r *VPCBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.HTTPProxy{}, builder.WithPredicates(vpcBackendPredicate())).
		Watches(
			&discoveryv1.EndpointSlice{},
			handler.EnqueueRequestsFromMapFunc(r.proxiesForEndpointSlice),
			builder.WithPredicates(endpointSlicePredicate()),
		).
		Named("extension-server-vpc-binding-retrigger").
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

func vpcBackendPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		proxy, ok := obj.(*networkingv1alpha.HTTPProxy)
		return ok && extcache.HasVPCBackend(proxy)
	})
}
