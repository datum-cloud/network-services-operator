// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

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
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
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
			BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: ptr.To(gatewayv1.Group("networking.datumapis.com")),
				Kind:  ptr.To(gatewayv1.Kind(KindNetworkService)),
				Name:  gatewayv1.ObjectName(service.Name),
				Port:  ptr.To(gatewayv1.PortNumber(8080)),
			}},
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
	require.Len(t, slices, 2, "101 selected members must be split rather than truncated")
	var endpointCount int
	for _, slice := range slices {
		require.Equal(t, downstreamService.Name, slice.Labels[discoveryv1.LabelServiceName])
		require.Equal(t, service.Name, slice.Labels[NetworkServiceBackendLabel])
		endpointCount += len(slice.Endpoints)
	}
	require.Equal(t, maxEndpointsPerSlice+1, endpointCount)

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
	require.Equal(t, types.NamespacedName{Namespace: "tenant", Name: "gw"}, requests[0].Request.NamespacedName)
	require.Empty(t, reconciler.listGatewaysForNetworkService(context.Background(), multicluster.ClusterName("tenant"), cl, "tenant", "other"))
}
