// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

func TestProcessDownstreamHTTPRouteRulesNetworkServiceBackend(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, k8sscheme.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))
	require.NoError(t, discoveryv1.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: types.UID("tenant-uid")}}
	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace.Name, Name: "api"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{
				Name: "http", Port: 8080, AppProtocol: ptr.To(networkingv1alpha.NetworkServiceAppProtocolH2C),
			}},
		},
	}
	objects := []client.Object{namespace, service}
	for i := 0; i < maxEndpointsPerSlice+1; i++ {
		objects = append(objects, &networkingv1alpha.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace.Name,
				Name:      fmt.Sprintf("nic-%03d", i),
				Labels:    map[string]string{"app": "api"},
			},
			Spec: networkingv1alpha.NetworkInterfaceSpec{
				Network:   networkingv1alpha.LocalNetworkRef{Name: "network"},
				Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Address: fmt.Sprintf("10.0.0.%d/24", i+1)}},
			},
			Status: networkingv1alpha.NetworkInterfaceStatus{
				Phase: networkingv1alpha.NetworkInterfacePhaseBound,
				Conditions: []metav1.Condition{{
					Type: networkingv1alpha.NetworkInterfaceHolderAvailable, Status: metav1.ConditionTrue,
				}},
			},
		})
	}
	objects = append(objects, &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name, Name: "released-nic", Labels: map[string]string{"app": "api"},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:   networkingv1alpha.LocalNetworkRef{Name: "network"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Address: "10.0.1.1/24"}},
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{Phase: networkingv1alpha.NetworkInterfacePhaseAvailable},
	})
	upstreamClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	downstreamClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	upstreamGateway := newGateway(config.NetworkServicesOperator{}, namespace.Name, "gw")
	downstreamGateway := newGateway(config.NetworkServicesOperator{}, "ns-tenant-uid", "gw")
	strategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", upstreamClient, downstreamClient)

	route := newHTTPRoute(namespace.Name, "route", func(route *gatewayv1.HTTPRoute) {
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{
			BackendRef: gatewayv1.BackendRef{
				Weight: ptr.To(int32(7)),
				BackendObjectReference: gatewayv1.BackendObjectReference{
					Group: ptr.To(gatewayv1.Group("networking.datumapis.com")),
					Kind:  ptr.To(gatewayv1.Kind(KindNetworkService)),
					Name:  gatewayv1.ObjectName(service.Name),
					Port:  ptr.To(gatewayv1.PortNumber(8080)),
				},
			},
		}}}}
	})

	reconciler := &GatewayReconciler{DownstreamCluster: &fakeCluster{cl: downstreamClient}}
	rules, resources, unresolved, err := reconciler.processDownstreamHTTPRouteRules(
		ctx, upstreamClient, upstreamGateway, *route, downstreamGateway, strategy,
	)
	require.NoError(t, err)
	require.Empty(t, unresolved)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].BackendRefs, 1)
	ref := rules[0].BackendRefs[0].BackendObjectReference
	require.Equal(t, KindService, string(ptr.Deref(ref.Kind, "")))
	require.Equal(t, gatewayv1.PortNumber(8080), *ref.Port)
	require.EqualValues(t, 7, ptr.Deref(rules[0].BackendRefs[0].Weight, 0), "the source backend weight must survive translation")

	var downstreamService *corev1.Service
	var slices []*discoveryv1.EndpointSlice
	for _, resource := range resources {
		switch typed := resource.(type) {
		case *corev1.Service:
			downstreamService = typed
		case *discoveryv1.EndpointSlice:
			slices = append(slices, typed)
		}
	}
	require.NotNil(t, downstreamService)
	require.Equal(t, "kubernetes.io/h2c", ptr.Deref(downstreamService.Spec.Ports[0].AppProtocol, ""))
	require.Len(t, slices, 2, "101 selected members must be split rather than truncated")
	var endpointCount int
	for _, slice := range slices {
		require.Equal(t, downstreamService.Name, slice.Labels[discoveryv1.LabelServiceName])
		require.Equal(t, service.Name, slice.Labels[NetworkServiceBackendLabel])
		require.Equal(t, "kubernetes.io/h2c", ptr.Deref(slice.Ports[0].AppProtocol, ""))
		for _, endpoint := range slice.Endpoints {
			require.True(t, ptr.Deref(endpoint.Conditions.Ready, false), "held available interfaces must resolve as ready endpoints")
		}
		endpointCount += len(slice.Endpoints)
	}
	require.Equal(t, maxEndpointsPerSlice+1, endpointCount)

	for _, tc := range []struct {
		name   string
		change func(*gatewayv1.BackendObjectReference)
	}{
		{"wrong API group", func(ref *gatewayv1.BackendObjectReference) { ref.Group = ptr.To(gatewayv1.Group("example.com")) }},
		{"cross-namespace", func(ref *gatewayv1.BackendObjectReference) { ref.Namespace = ptr.To(gatewayv1.Namespace("other")) }},
	} {
		t.Run("invalid stored reference is rejected: "+tc.name, func(t *testing.T) {
			badRoute := route.DeepCopy()
			tc.change(&badRoute.Spec.Rules[0].BackendRefs[0].BackendObjectReference)
			_, badResources, badRefs, err := reconciler.processDownstreamHTTPRouteRules(
				ctx, upstreamClient, upstreamGateway, *badRoute, downstreamGateway, strategy,
			)
			require.NoError(t, err)
			require.Len(t, badRefs, 1)
			require.Equal(t, string(gatewayv1.RouteReasonUnsupportedValue), badRefs[0].reason)
			require.Len(t, badResources, 1, "invalid source identity must not be resolved into endpoint shards")
		})
	}

	addrless := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace.Name, Name: "addrless-nic", Labels: map[string]string{"app": "api"}},
		Spec:       networkingv1alpha.NetworkInterfaceSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "network"}},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase:      networkingv1alpha.NetworkInterfacePhaseBound,
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable, Status: metav1.ConditionTrue}},
		},
	}
	require.NoError(t, upstreamClient.Create(ctx, addrless))
	_, incompleteResources, unresolved, err := reconciler.processDownstreamHTTPRouteRules(
		ctx, upstreamClient, upstreamGateway, *route, downstreamGateway, strategy,
	)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Equal(t, string(gatewayv1.RouteReasonUnsupportedValue), unresolved[0].reason)
	require.Len(t, incompleteResources, 1, "an address-less selected member must fail closed rather than silently omitting it")

	var crossNetwork networkingv1alpha.NetworkInterface
	require.NoError(t, upstreamClient.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: "nic-000"}, &crossNetwork))
	crossNetwork.Spec.Network.Name = "another-network"
	require.NoError(t, upstreamClient.Update(ctx, &crossNetwork))
	_, unsafeResources, unresolved, err := reconciler.processDownstreamHTTPRouteRules(
		ctx, upstreamClient, upstreamGateway, *route, downstreamGateway, strategy,
	)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Equal(t, string(gatewayv1.RouteReasonUnsupportedValue), unresolved[0].reason)
	require.Len(t, unsafeResources, 1, "cross-network membership must fail closed without endpoint slices")

	// A numeric port has to name a port the NetworkService actually declares.
	service.Spec.Ports = nil
	require.NoError(t, upstreamClient.Update(ctx, service))
	_, missingResources, unresolved, err := reconciler.processDownstreamHTTPRouteRules(
		ctx, upstreamClient, upstreamGateway, *route, downstreamGateway, strategy,
	)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Equal(t, string(gatewayv1.RouteReasonBackendNotFound), unresolved[0].reason)
	require.Len(t, missingResources, 1, "a missing declared port gets an empty Service, not stale endpoints")
	missingService, ok := missingResources[0].(*corev1.Service)
	require.True(t, ok)
	require.Len(t, missingService.Spec.Ports, 1)
	require.EqualValues(t, 8080, missingService.Spec.Ports[0].Port)

	require.NoError(t, upstreamClient.Delete(ctx, service))
	_, deletedResources, unresolved, err := reconciler.processDownstreamHTTPRouteRules(
		ctx, upstreamClient, upstreamGateway, *route, downstreamGateway, strategy,
	)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Equal(t, string(gatewayv1.RouteReasonBackendNotFound), unresolved[0].reason)
	require.Len(t, deletedResources, 1, "a deleted NetworkService must not leave EndpointSlices behind")
}

func TestNetworkServiceBackendEndpointLimit(t *testing.T) {
	require.False(t, networkServiceBackendExceedsLimit(0))
	require.False(t, networkServiceBackendExceedsLimit(maxNetworkServiceBackendSlices*maxEndpointsPerSlice))
	require.True(t, networkServiceBackendExceedsLimit(maxNetworkServiceBackendSlices*maxEndpointsPerSlice+1))
}

func TestListGatewaysForNetworkService(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.Install(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	route := newHTTPRoute("tenant", "route", func(route *gatewayv1.HTTPRoute) {
		route.Spec.ParentRefs = []gatewayv1.ParentReference{{
			Group: ptr.To(gatewayv1.Group(gatewayv1.GroupName)),
			Kind:  ptr.To(gatewayv1.Kind(KindGateway)),
			Name:  gatewayv1.ObjectName("gw"),
		}, {
			Group: ptr.To(gatewayv1.Group(gatewayv1.GroupName)),
			Kind:  ptr.To(gatewayv1.Kind(KindGateway)),
			Name:  gatewayv1.ObjectName("gw"),
		}}
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{
			BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: ptr.To(gatewayv1.Group("networking.datumapis.com")),
				Kind:  ptr.To(gatewayv1.Kind(KindNetworkService)),
				Name:  gatewayv1.ObjectName("api"),
			}}},
		}}}
	})
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(route).Build()
	reconciler := &GatewayReconciler{}
	requests := reconciler.listGatewaysForNetworkService(context.Background(), multicluster.ClusterName("tenant"), cl, "tenant", "api")
	require.Len(t, requests, 1, "duplicate parent references must not enqueue the same Gateway twice")
	require.Equal(t, "tenant", string(requests[0].ClusterName))
	require.Equal(t, types.NamespacedName{Namespace: "tenant", Name: "gw"}, requests[0].NamespacedName)
	require.Empty(t, reconciler.listGatewaysForNetworkService(context.Background(), multicluster.ClusterName("tenant"), cl, "tenant", "other"))
}

func TestEnsureDownstreamHTTPRoutePropagatesNetworkServiceSlicesAndPrunesStaleShards(t *testing.T) {
	ctx := context.Background()
	testScheme := runtime.NewScheme()
	require.NoError(t, k8sscheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "tenant-uid"}}
	downstreamNamespace := "ns-tenant-uid"
	upstreamGateway := newGateway(config.NetworkServicesOperator{}, upstreamNamespace.Name, "gw")
	downstreamGateway := newGateway(config.NetworkServicesOperator{}, downstreamNamespace, upstreamGateway.Name)
	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: "api"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
	route := newHTTPRoute(upstreamNamespace.Name, "route", func(route *gatewayv1.HTTPRoute) {
		route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(upstreamGateway.Name)}}
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{
			BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: ptr.To(gatewayv1.Group("networking.datumapis.com")),
				Kind:  ptr.To(gatewayv1.Kind(KindNetworkService)),
				Name:  gatewayv1.ObjectName(service.Name),
				Port:  ptr.To(gatewayv1.PortNumber(8080)),
			}},
		}}}}
	})
	interfaces := make([]client.Object, 0, maxEndpointsPerSlice+1)
	for i := 0; i < maxEndpointsPerSlice+1; i++ {
		interfaces = append(interfaces, &networkingv1alpha.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: upstreamNamespace.Name, Name: fmt.Sprintf("nic-%03d", i),
				Labels: map[string]string{"app": "api"},
			},
			Spec: networkingv1alpha.NetworkInterfaceSpec{
				Network:   networkingv1alpha.LocalNetworkRef{Name: "network"},
				Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Address: fmt.Sprintf("10.1.0.%d/24", i+1)}},
			},
			Status: networkingv1alpha.NetworkInterfaceStatus{
				Phase:      networkingv1alpha.NetworkInterfacePhaseBound,
				Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable, Status: metav1.ConditionTrue}},
			},
		})
	}
	upstreamObjects := make([]client.Object, 0, 4+len(interfaces))
	upstreamObjects = append(upstreamObjects, upstreamNamespace, upstreamGateway, route, service)
	upstreamObjects = append(upstreamObjects, interfaces...)
	upstreamClient := fake.NewClientBuilder().WithScheme(testScheme).
		WithObjects(upstreamObjects...).
		WithStatusSubresource(upstreamGateway, route).
		Build()
	downstreamClient := fake.NewClientBuilder().WithScheme(testScheme).
		WithObjects(downstreamGateway).
		WithStatusSubresource(downstreamGateway, &gatewayv1.HTTPRoute{}).
		Build()
	strategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", upstreamClient, downstreamClient)
	reconciler := &GatewayReconciler{DownstreamCluster: &fakeCluster{cl: downstreamClient}}

	reconcile := func() {
		t.Helper()
		result := reconciler.ensureDownstreamGatewayHTTPRoutes(
			ctx, upstreamClient, upstreamGateway, "test", downstreamGateway, strategy, nil, nil, nil,
		)
		require.NoError(t, result.Err)
		_, err := result.Complete(ctx)
		require.NoError(t, err)
	}
	listSlices := func() []discoveryv1.EndpointSlice {
		t.Helper()
		var list discoveryv1.EndpointSliceList
		require.NoError(t, downstreamClient.List(ctx, &list, client.InNamespace(downstreamNamespace)))
		return list.Items
	}

	// The Gateway API route's ownership labels are the actual ClusterPropagationPolicy
	// selector for EndpointSlices (UpstreamOwnerClusterNameLabel Exists).
	reconcile()
	routeResourceName := routeBackendResourceName(route, 0, 0)
	downstreamRoute := &gatewayv1.HTTPRoute{}
	require.NoError(t, downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: route.Name}, downstreamRoute))
	slices := listSlices()
	require.Len(t, slices, 2)
	var endpoints int
	for _, slice := range slices {
		require.Equal(t, routeResourceName, slice.Labels[discoveryv1.LabelServiceName])
		require.Equal(t, service.Name, slice.Labels[NetworkServiceBackendLabel])
		require.Equal(t, "cluster-test", slice.Labels[downstreamclient.UpstreamOwnerClusterNameLabel])
		require.Equal(t, "gateway.networking.k8s.io", slice.Labels[downstreamclient.UpstreamOwnerGroupLabel])
		require.Equal(t, KindHTTPRoute, slice.Labels[downstreamclient.UpstreamOwnerKindLabel])
		require.Equal(t, route.Name, slice.Labels[downstreamclient.UpstreamOwnerNameLabel])
		require.Equal(t, upstreamNamespace.Name, slice.Labels[downstreamclient.UpstreamOwnerNamespaceLabel])
		require.True(t, metav1.IsControlledBy(&slice, downstreamRoute), "all backend shards remain owned by the downstream route")
		require.NotEmpty(t, slice.Labels[downstreamclient.UpstreamOwnerClusterNameLabel], "federation's EndpointSlice labelSelector requires this key")
		endpoints += len(slice.Endpoints)
	}
	require.Equal(t, maxEndpointsPerSlice+1, endpoints)

	// Shrinking membership leaves one shard; the old second shard must be removed.
	for i := 1; i <= maxEndpointsPerSlice; i++ {
		var iface networkingv1alpha.NetworkInterface
		require.NoError(t, upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: fmt.Sprintf("nic-%03d", i)}, &iface))
		iface.Labels["app"] = "other"
		require.NoError(t, upstreamClient.Update(ctx, &iface))
	}
	reconcile()
	slices = listSlices()
	require.Len(t, slices, 1)
	require.Equal(t, routeResourceName, slices[0].Name)
	err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: routeResourceName + "-1"}, &discoveryv1.EndpointSlice{})
	require.Error(t, err)

	// An unsupported selected member suppresses all endpoint publication and prunes
	// the remaining old shard rather than leaving a partial/stale backend.
	require.NoError(t, upstreamClient.Create(ctx, &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: "addrless", Labels: map[string]string{"app": "api"}},
		Spec:       networkingv1alpha.NetworkInterfaceSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "network"}},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase:      networkingv1alpha.NetworkInterfacePhaseBound,
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable, Status: metav1.ConditionTrue}},
		},
	}))
	reconcile()
	require.Empty(t, listSlices())
	var storedRoute gatewayv1.HTTPRoute
	require.NoError(t, upstreamClient.Get(ctx, client.ObjectKeyFromObject(route), &storedRoute))
	require.Len(t, storedRoute.Status.Parents, 1)
	resolvedRefs := apimeta.FindStatusCondition(storedRoute.Status.Parents[0].Conditions, string(gatewayv1.RouteConditionResolvedRefs))
	require.NotNil(t, resolvedRefs)
	require.Equal(t, metav1.ConditionFalse, resolvedRefs.Status)
	require.Equal(t, string(gatewayv1.RouteReasonUnsupportedValue), resolvedRefs.Reason)

	// Controller reads of source membership must not add ownership metadata or
	// otherwise rewrite the NetworkService or its selected interface.
	var storedService networkingv1alpha.NetworkService
	require.NoError(t, upstreamClient.Get(ctx, client.ObjectKeyFromObject(service), &storedService))
	require.Equal(t, service.Spec, storedService.Spec)
	var storedInterface networkingv1alpha.NetworkInterface
	require.NoError(t, upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: "nic-000"}, &storedInterface))
	require.Equal(t, map[string]string{"app": "api"}, storedInterface.Labels)
	require.Equal(t, interfaces[0].(*networkingv1alpha.NetworkInterface).Spec, storedInterface.Spec)
	require.Equal(t, interfaces[0].(*networkingv1alpha.NetworkInterface).Status, storedInterface.Status)
	require.Empty(t, storedInterface.Finalizers)
}

func TestNetworkInterfaceUpdateHandlerMapsBothOldAndNewLabels(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "api"},
		Spec: networkingv1alpha.NetworkServiceSpec{NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
			Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		}},
	}
	route := newHTTPRoute("tenant", "route", func(route *gatewayv1.HTTPRoute) {
		route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: "gw"}}
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{
			BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: ptr.To(gatewayv1.Group("networking.datumapis.com")),
				Kind:  ptr.To(gatewayv1.Kind(KindNetworkService)),
				Name:  gatewayv1.ObjectName(service.Name),
			}}},
		}}}
	})
	cl := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(service, route).Build()
	handler := (&GatewayReconciler{}).listGatewaysForNetworkInterfaceFunc("tenant", &fakeCluster{cl: cl})
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
	t.Cleanup(queue.ShutDown)
	oldInterface := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{
		Namespace: "tenant", Name: "nic", Labels: map[string]string{"app": "api"},
	}}
	newInterface := oldInterface.DeepCopy()
	newInterface.Labels["app"] = "other"
	handler.Update(context.Background(), event.TypedUpdateEvent[client.Object]{ObjectOld: oldInterface, ObjectNew: newInterface}, queue)
	require.Equal(t, 1, queue.Len(), "removing the selector label must still enqueue from the old object")
	request, shutdown := queue.Get()
	require.False(t, shutdown)
	require.Equal(t, types.NamespacedName{Namespace: "tenant", Name: "gw"}, request.NamespacedName)
	queue.Done(request)
	oldInterface, newInterface = newInterface, oldInterface
	handler.Update(context.Background(), event.TypedUpdateEvent[client.Object]{ObjectOld: oldInterface, ObjectNew: newInterface}, queue)
	require.Equal(t, 1, queue.Len(), "adding the selector label must enqueue from the new object")
}
