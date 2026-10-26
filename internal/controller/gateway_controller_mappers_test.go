// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

func mapperTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	return s
}

func routeWithEndpointSliceBackend(name, gateway string, backendNamespace *string, backends ...string) *gatewayv1.HTTPRoute {
	refs := make([]gatewayv1.HTTPBackendRef, 0, len(backends))
	for _, b := range backends {
		ref := gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
			Group: ptr.To(gatewayv1.Group(discoveryv1.GroupName)),
			Kind:  ptr.To(gatewayv1.Kind(KindEndpointSlice)),
			Name:  gatewayv1.ObjectName(b),
		}}}
		if backendNamespace != nil {
			ref.Namespace = ptr.To(gatewayv1.Namespace(*backendNamespace))
		}
		refs = append(refs, ref)
	}
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gateway)}},
			},
			Rules: []gatewayv1.HTTPRouteRule{{BackendRefs: refs}},
		},
	}
}

func drainQueue(q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) []mcreconcile.Request {
	var out []mcreconcile.Request
	for q.Len() > 0 {
		item, _ := q.Get()
		out = append(out, item)
		q.Done(item)
	}
	return out
}

func newTestQueue() workqueue.TypedRateLimitingInterface[mcreconcile.Request] {
	return workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
}

func createEvent[T client.Object](t *testing.T, h handler.TypedEventHandler[T, mcreconcile.Request], obj T) []mcreconcile.Request {
	t.Helper()
	q := newTestQueue()
	defer q.ShutDown()
	h.Create(context.Background(), event.TypedCreateEvent[T]{Object: obj}, q)
	return drainQueue(q)
}

func TestListGatewaysForHTTPRouteFilterFunc(t *testing.T) {
	routeWithFilter := func(name, gateway, filter string) *gatewayv1.HTTPRoute {
		route := routeWithEndpointSliceBackend(name, gateway, nil, "slice")
		route.Spec.Rules[0].BackendRefs[0].Filters = []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gatewayv1.LocalObjectReference{
				Group: envoygatewayv1alpha1.GroupName,
				Kind:  envoygatewayv1alpha1.KindHTTPRouteFilter,
				Name:  gatewayv1.ObjectName(filter),
			},
		}}
		return route
	}

	cl := fake.NewClientBuilder().WithScheme(mapperTestScheme(t)).WithObjects(
		routeWithFilter("route-a", "gw-a", "filter-a"),
		routeWithFilter("route-b", "gw-b", "filter-b"),
	).Build()

	r := &GatewayReconciler{}
	h := r.listGatewaysForHTTPRouteFilterFunc("proj-a", &fakeCluster{cl: cl})

	reqs := createEvent[client.Object](t, h, &envoygatewayv1alpha1.HTTPRouteFilter{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "filter-a"},
	})
	require.Len(t, reqs, 1)
	require.Equal(t, "gw-a", reqs[0].Name)
}
