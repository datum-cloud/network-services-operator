// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type clusterMapManager struct {
	mcmanager.Manager
	clusters map[multicluster.ClusterName]client.Client
}

func (m *clusterMapManager) GetCluster(_ context.Context, name multicluster.ClusterName) (cluster.Cluster, error) {
	cl, ok := m.clusters[name]
	if !ok {
		return nil, errors.New("cluster not found")
	}
	return &fakeCluster{cl: cl}, nil
}

func mapperTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	return s
}

func indexedRouteClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(mapperTestScheme(t)).
		WithObjects(objs...).
		WithIndex(&gatewayv1.HTTPRoute{}, httpRouteEndpointSliceBackendIndex, httpRouteEndpointSliceBackendIndexFunc).
		Build()
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

func vpcHubSlice(name, sourceName, project, projectNamespace string) *discoveryv1.EndpointSlice {
	labels := map[string]string{
		VPCPodTenantIDLabel:                            "vpc-a-attach-a",
		VPCEndpointSliceProjectionLabel:                "true",
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + project,
		downstreamclient.UpstreamOwnerNamespaceLabel:   projectNamespace,
	}
	annotations := map[string]string{}
	if sourceName != "" {
		annotations[VPCEndpointSliceSourceNameAnnotation] = sourceName
	}
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ns-downstream",
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		AddressType: discoveryv1.AddressTypeIPv6,
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

func preciseMappingReconciler(enabled bool) *GatewayReconciler {
	r := &GatewayReconciler{}
	r.Config.Gateway.PreciseEndpointSliceMapping = enabled
	return r
}

func TestListGatewaysForDownstreamVPCPodEndpointSlice(t *testing.T) {
	const project = "proj-a"
	const projectNamespace = "default"

	projectClient := indexedRouteClient(t,
		routeWithEndpointSliceBackend("route-a", "gw-a", nil, "pod-a"),
		routeWithEndpointSliceBackend("route-b", "gw-b", nil, "pod-b"),
	)

	originalRoute := routeWithEndpointSliceBackend("route-original", "gw-original", nil, "cni-pod")
	originalRoute.Namespace = "ns-downstream"
	originalRoute.Labels = map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + project,
		downstreamclient.UpstreamOwnerNamespaceLabel:   projectNamespace,
	}

	downstreamClient := fake.NewClientBuilder().WithScheme(mapperTestScheme(t)).WithObjects(
		originalRoute,
		&gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-downstream",
			Name:      "orphan",
			Labels: map[string]string{
				downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + project,
				downstreamclient.UpstreamOwnerNamespaceLabel:   projectNamespace,
				downstreamclient.UpstreamOwnerNameLabel:        "orphan",
			},
		}},
	).Build()

	r := preciseMappingReconciler(true)
	r.mgr = &clusterMapManager{clusters: map[multicluster.ClusterName]client.Client{project: projectClient}}
	h := r.endpointSliceMappers().vpcPod("", &fakeCluster{cl: downstreamClient})

	t.Run("enqueues only the parent of the route referencing the slice", func(t *testing.T) {
		reqs := createEvent(t, h, vpcHubSlice("vpc-loc-pod-a", "pod-a", project, projectNamespace))
		require.Len(t, reqs, 1)
		require.Equal(t, multicluster.ClusterName(project), reqs[0].ClusterName)
		require.Equal(t, types.NamespacedName{Namespace: projectNamespace, Name: "gw-a"}, reqs[0].NamespacedName)
	})

	t.Run("falls back to the slice name without the source annotation", func(t *testing.T) {
		reqs := createEvent(t, h, vpcHubSlice("pod-b", "", project, projectNamespace))
		require.Len(t, reqs, 1)
		require.Equal(t, "gw-b", reqs[0].Name)
	})

	t.Run("slice no route references enqueues nothing", func(t *testing.T) {
		require.Empty(t, createEvent(t, h, vpcHubSlice("vpc-loc-pod-z", "pod-z", project, projectNamespace)))
	})

	t.Run("slice without tenant label is ignored", func(t *testing.T) {
		slice := vpcHubSlice("vpc-loc-pod-a", "pod-a", project, projectNamespace)
		delete(slice.Labels, VPCPodTenantIDLabel)
		require.Empty(t, createEvent(t, h, slice))
	})

	t.Run("slice without upstream owner labels maps through the downstream route naming it", func(t *testing.T) {
		slice := vpcHubSlice("cni-pod", "", project, projectNamespace)
		delete(slice.Labels, downstreamclient.UpstreamOwnerClusterNameLabel)
		delete(slice.Labels, downstreamclient.UpstreamOwnerNamespaceLabel)
		reqs := createEvent(t, h, slice)
		require.Len(t, reqs, 1)
		require.Equal(t, multicluster.ClusterName(project), reqs[0].ClusterName)
		require.Equal(t, types.NamespacedName{Namespace: projectNamespace, Name: "gw-original"}, reqs[0].NamespacedName)
	})

	t.Run("slice without owner labels that no route names enqueues nothing", func(t *testing.T) {
		slice := vpcHubSlice("vpc-loc-pod-z", "", project, projectNamespace)
		delete(slice.Labels, downstreamclient.UpstreamOwnerClusterNameLabel)
		require.Empty(t, createEvent(t, h, slice))
	})

	t.Run("unknown project cluster returns nothing", func(t *testing.T) {
		require.Empty(t, createEvent(t, h, vpcHubSlice("vpc-loc-pod-a", "pod-a", "proj-missing", projectNamespace)))
	})

	t.Run("route in another project namespace is not matched", func(t *testing.T) {
		require.Empty(t, createEvent(t, h, vpcHubSlice("vpc-loc-pod-a", "pod-a", project, "other")))
	})
}

func TestVPCPodEndpointSliceRoutingChangedPredicate(t *testing.T) {
	predicates := preciseMappingReconciler(true).endpointSliceMappers().vpcPodPredicates
	require.Len(t, predicates, 1)
	p := predicates[0]
	base := vpcHubSlice("vpc-loc-pod-a", "pod-a", "proj-a", "default")

	require.True(t, p.Create(event.TypedCreateEvent[*discoveryv1.EndpointSlice]{Object: base}))
	require.True(t, p.Delete(event.TypedDeleteEvent[*discoveryv1.EndpointSlice]{Object: base}))

	update := func(mutate func(*discoveryv1.EndpointSlice)) bool {
		newer := base.DeepCopy()
		mutate(newer)
		return p.Update(event.TypedUpdateEvent[*discoveryv1.EndpointSlice]{ObjectOld: base, ObjectNew: newer})
	}

	ready := func(r, serving, terminating bool) discoveryv1.Endpoint {
		return discoveryv1.Endpoint{
			Addresses: []string{"fd00::1"},
			Conditions: discoveryv1.EndpointConditions{
				Ready: ptr.To(r), Serving: ptr.To(serving), Terminating: ptr.To(terminating),
			},
		}
	}
	base.Endpoints = []discoveryv1.Endpoint{ready(true, true, false)}

	require.False(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Endpoints = []discoveryv1.Endpoint{ready(false, false, true)}
		s.ResourceVersion = "2"
	}), "condition-only change must not pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Endpoints[0].Addresses = []string{"fd00::2"}
	}), "address change must pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Endpoints[0].Addresses = []string{"fd00::2"}
		s.Endpoints[0].Conditions.Ready = ptr.To(false)
	}), "address change alongside a condition change must pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Endpoints[0].NodeName = ptr.To("node-b")
	}), "other endpoint field change must pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Ports = []discoveryv1.EndpointPort{{Port: ptr.To(int32(8080))}}
	}), "port change must pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Annotations["galactic.datumapis.com/srv6-sid"] = "fd00:1::1"
	}), "annotation change must pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Labels["unrelated"] = "x"
	}), "label change must pass")
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Labels[VPCPodTenantIDLabel] = "other"
	}))
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		delete(s.Labels, VPCPodTenantIDLabel)
	}))
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] = "cluster-proj-b"
	}))
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Labels[downstreamclient.UpstreamOwnerNamespaceLabel] = "other"
	}))
	require.True(t, update(func(s *discoveryv1.EndpointSlice) {
		s.Annotations[VPCEndpointSliceSourceNameAnnotation] = "pod-b"
	}))
}

func TestListGatewaysForEndpointSliceFunc(t *testing.T) {
	other := "other"
	cl := indexedRouteClient(t,
		routeWithEndpointSliceBackend("route-a", "gw-a", nil, "slice-a"),
		routeWithEndpointSliceBackend("route-b", "gw-b", nil, "slice-b"),
		routeWithEndpointSliceBackend("route-c", "gw-c", &other, "slice-a"),
		routeWithEndpointSliceBackend("route-d", "gw-d", nil, "slice-b", "slice-a"),
	)

	h := preciseMappingReconciler(true).endpointSliceMappers().upstream("proj-a", &fakeCluster{cl: cl})

	reqs := createEvent[client.Object](t, h, &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "slice-a"},
	})
	got := map[types.NamespacedName]bool{}
	for _, req := range reqs {
		require.Equal(t, multicluster.ClusterName("proj-a"), req.ClusterName)
		got[req.NamespacedName] = true
	}
	require.Equal(t, map[types.NamespacedName]bool{
		{Namespace: "default", Name: "gw-a"}: true,
		{Namespace: "default", Name: "gw-d"}: true,
	}, got)

	reqs = createEvent[client.Object](t, h, &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "slice-a"},
	})
	require.Len(t, reqs, 1, "same-named slice in another namespace matches only the route pointing there")
	require.Equal(t, "gw-c", reqs[0].Name)
}

func TestHTTPRouteEndpointSliceBackendIndexFunc(t *testing.T) {
	other := "other"
	route := routeWithEndpointSliceBackend("route", "gw", nil, "slice-a", "slice-a")
	route.Spec.Rules = append(route.Spec.Rules,
		routeWithEndpointSliceBackend("", "", &other, "slice-b").Spec.Rules[0],
		gatewayv1.HTTPRouteRule{BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{Name: "svc"},
		}}}},
	)

	require.ElementsMatch(t, []string{
		endpointSliceBackendIndexKey("default", "slice-a"),
		endpointSliceBackendIndexKey("other", "slice-b"),
	}, httpRouteEndpointSliceBackendIndexFunc(route))
	require.Equal(t, "default/slice-a", endpointSliceBackendIndexKey("default", "slice-a"))
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

func TestEndpointSliceMappersPreciseMappingDisabled(t *testing.T) {
	const project = "proj-a"

	downstreamRoute := func(name, gateway, slice string) *gatewayv1.HTTPRoute {
		route := routeWithEndpointSliceBackend(name, gateway, nil, slice)
		route.Namespace = "ns-downstream"
		route.Labels = map[string]string{
			downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + project,
			downstreamclient.UpstreamOwnerNamespaceLabel:   "default",
		}
		return route
	}
	downstreamClient := fake.NewClientBuilder().WithScheme(mapperTestScheme(t)).WithObjects(
		downstreamRoute("route-a", "gw-a", "vpc-loc-pod-a"),
		downstreamRoute("route-b", "gw-unrelated", "vpc-loc-pod-b"),
	).Build()

	mappers := preciseMappingReconciler(false).endpointSliceMappers()

	t.Run("vpc pod slice enqueues the gateways whose downstream routes name it", func(t *testing.T) {
		reqs := createEvent(t, mappers.vpcPod("", &fakeCluster{cl: downstreamClient}),
			vpcHubSlice("vpc-loc-pod-a", "pod-a", project, "default"))
		require.Len(t, reqs, 1)
		require.Equal(t, multicluster.ClusterName(project), reqs[0].ClusterName)
		require.Equal(t, types.NamespacedName{Namespace: "default", Name: "gw-a"}, reqs[0].NamespacedName)
	})

	t.Run("vpc pod slice updates are not filtered", func(t *testing.T) {
		require.Empty(t, mappers.vpcPodPredicates)
	})

	t.Run("upstream slice mapping needs no index", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(mapperTestScheme(t)).WithObjects(
			routeWithEndpointSliceBackend("route-a", "gw-a", nil, "slice-a"),
			routeWithEndpointSliceBackend("route-b", "gw-b", nil, "slice-b"),
		).Build()

		reqs := createEvent[client.Object](t, mappers.upstream("proj-a", &fakeCluster{cl: cl}), &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "slice-a"},
		})
		require.Len(t, reqs, 1)
		require.Equal(t, "gw-a", reqs[0].Name)
	})
}
