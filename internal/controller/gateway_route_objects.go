// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// routeObjectLists are the kinds ensureDownstreamHTTPRoute creates under a
// downstream HTTPRoute's control. Nothing else makes a downstream HTTPRoute a
// controller, so the objects it controls are exactly the ones created here.
var routeObjectLists = []func() client.ObjectList{
	func() client.ObjectList { return &corev1.ServiceList{} },
	func() client.ObjectList { return &discoveryv1.EndpointSliceList{} },
	func() client.ObjectList { return &gatewayv1.BackendTLSPolicyList{} },
	func() client.ObjectList { return &envoygatewayv1alpha1.BackendTrafficPolicyList{} },
}

// deleteUnneededRouteObjects deletes the downstream objects the route controls
// that desired no longer holds: those of a removed rule or backend, and a
// policy the route stopped needing. Owner garbage collection removes them only
// with the route itself. The objects are read from the cache, so a route that
// never had one sends no delete.
func deleteUnneededRouteObjects(ctx context.Context, cl client.Client, route *gatewayv1.HTTPRoute, desired []client.Object) error {
	unneeded, err := unneededRouteObjects(ctx, cl, route, desired)
	if err != nil {
		return err
	}

	logger := log.FromContext(ctx)
	for _, obj := range unneeded {
		gvk, err := apiutil.GVKForObject(obj, cl.Scheme())
		if err != nil {
			return err
		}
		if err := cl.Delete(ctx, obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed deleting stale downstream resource %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
		}
		logger.Info("stale downstream resource removed",
			jsonKeyKind, gvk.Kind,
			"namespace", obj.GetNamespace(),
			jsonKeyName, obj.GetName(),
		)
	}
	return nil
}

// unneededRouteObjects returns the objects of routeObjectLists in the route's
// namespace that the route controls and desired does not hold.
func unneededRouteObjects(ctx context.Context, cl client.Client, route *gatewayv1.HTTPRoute, desired []client.Object) ([]client.Object, error) {
	keep := sets.New[string]()
	for _, obj := range desired {
		key, err := objectKindName(cl, obj)
		if err != nil {
			return nil, err
		}
		keep.Insert(key)
	}

	var unneeded []client.Object
	for _, newList := range routeObjectLists {
		list := newList()
		if err := cl.List(ctx, list, client.InNamespace(route.Namespace)); err != nil {
			return nil, fmt.Errorf("failed listing downstream objects of route %s/%s: %w", route.Namespace, route.Name, err)
		}
		found, err := controlledAndNotKept(cl, list, route, keep)
		if err != nil {
			return nil, err
		}
		unneeded = append(unneeded, found...)
	}
	return unneeded, nil
}

// controlledAndNotKept returns the items of list the route controls whose kind
// and name keep does not hold.
func controlledAndNotKept(cl client.Client, list client.ObjectList, route *gatewayv1.HTTPRoute, keep sets.Set[string]) ([]client.Object, error) {
	items, err := apimeta.ExtractList(list)
	if err != nil {
		return nil, err
	}

	var found []client.Object
	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok || !metav1.IsControlledBy(obj, route) {
			continue
		}
		key, err := objectKindName(cl, obj)
		if err != nil {
			return nil, err
		}
		if !keep.Has(key) {
			found = append(found, obj)
		}
	}
	return found, nil
}

// objectKindName identifies an object by kind and name; a route's objects share
// its namespace.
func objectKindName(cl client.Client, obj client.Object) (string, error) {
	gvk, err := apiutil.GVKForObject(obj, cl.Scheme())
	if err != nil {
		return "", err
	}
	return gvk.GroupKind().String() + "/" + obj.GetName(), nil
}
