// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func testLoadBalancerRoute(name string, created time.Time) *networkingv1alpha.NetworkLoadBalancerRoute {
	return &networkingv1alpha.NetworkLoadBalancerRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test", Name: name, UID: types.UID(name), Generation: 3,
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: networkingv1alpha.NetworkLoadBalancerRouteSpec{
			ParentRefs: []networkingv1alpha.NetworkLoadBalancerRouteParentRef{{Name: "edge", Listener: "tcp"}},
			Backends: []networkingv1alpha.NetworkLoadBalancerRouteBackend{{
				NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: "storefront", Port: "http"},
			}},
		},
	}
}

func testRouteLoadBalancer() *networkingv1alpha.NetworkLoadBalancer {
	return &networkingv1alpha.NetworkLoadBalancer{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "edge"},
		Spec: networkingv1alpha.NetworkLoadBalancerSpec{
			NetworkRef: networkingv1alpha.LocalNetworkRef{Name: "default"},
			Exposure: networkingv1alpha.NetworkLoadBalancerExposure{Addresses: []networkingv1alpha.NetworkLoadBalancerAddressRequest{{
				Name: "regional", Family: networkingv1alpha.IPv6Protocol,
				Locations: networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll},
			}}},
			Listeners: []networkingv1alpha.NetworkLoadBalancerListener{{
				Name: "tcp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"regional"},
			}},
		},
	}
}

func testRouteNetworkService() *networkingv1alpha.NetworkService {
	return &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "storefront"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "storefront"}},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
}

func testRouteMember(name string, available bool, address string) *networkingv1alpha.NetworkInterface {
	condition := metav1.ConditionFalse
	if available {
		condition = metav1.ConditionTrue
	}
	return &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test", Name: name,
			Labels: map[string]string{
				"app": "storefront",
				networkingv1alpha.NetworkInterfaceLocationLabel:        "dfw",
				networkingv1alpha.NetworkInterfaceProjectionLabel:      "true",
				networkingv1alpha.NetworkInterfaceSourceNamespaceLabel: "cell-test",
				networkingv1alpha.NetworkInterfaceSourceUIDLabel:       "source-" + name,
			},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:   networkingv1alpha.LocalNetworkRef{Name: "default"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Address: address}},
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase:      networkingv1alpha.NetworkInterfacePhaseBound,
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable, Status: condition, Reason: "Test", LastTransitionTime: metav1.Now()}},
		},
	}
}

func TestNetworkLoadBalancerRouteOldestOwnerKeepsListener(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	older.Spec.Backends[0].NetworkService.Port = "missing"
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, testRouteLoadBalancer(), testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(older, newer).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{older, newer} {
		require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	}

	var gotOlder, gotNewer networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(older), &gotOlder))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &gotNewer))
	require.Len(t, gotOlder.Status.Parents, 1)
	require.Len(t, gotNewer.Status.Parents, 1)
	assert.True(t, gotOlder.Status.Parents[0].Effective)
	assert.False(t, gotNewer.Status.Parents[0].Effective)
	assert.Equal(t, nlbRouteReasonPortNotFound, apimeta.FindStatusCondition(gotOlder.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
	assert.Equal(t, nlbRouteReasonResolved, apimeta.FindStatusCondition(gotNewer.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
	assert.Equal(t, nlbRouteReasonSuperseded, apimeta.FindStatusCondition(gotNewer.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteProgrammed).Reason)
	assert.Equal(t, int64(3), apimeta.FindStatusCondition(gotNewer.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteAccepted).ObservedGeneration)

	requests := siblingRoutesForRouteEvent(ctx, cl, older)
	require.Len(t, requests, 2)
	require.NoError(t, cl.Delete(ctx, older))
	requests = siblingRoutesForRouteEvent(ctx, cl, older)
	require.Len(t, requests, 1)
	assert.Equal(t, "newer", requests[0].Name)
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &gotNewer))
	assert.True(t, gotNewer.Status.Parents[0].Effective)
	assert.Equal(t, nlbRouteReasonAwaitingDataPlane,
		apimeta.FindStatusCondition(gotNewer.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteProgrammed).Reason)
}

func TestNetworkLoadBalancerRouteReferenceConditions(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mutate      func(*networkingv1alpha.NetworkLoadBalancerRoute, *networkingv1alpha.NetworkLoadBalancer, *networkingv1alpha.NetworkService)
		omitParent  bool
		omitService bool
		accepted    string
		resolved    string
	}{
		{name: "missing parent", omitParent: true, accepted: nlbRouteReasonParentNotFound, resolved: nlbRouteReasonParentNotFound},
		{name: "missing listener", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute, _ *networkingv1alpha.NetworkLoadBalancer, _ *networkingv1alpha.NetworkService) {
			r.Spec.ParentRefs[0].Listener = "absent"
		}, accepted: nlbRouteReasonListenerNotFound, resolved: nlbRouteReasonListenerNotFound},
		{name: "missing backend", omitService: true, accepted: nlbRouteReasonAttached, resolved: nlbRouteReasonBackendNotFound},
		{name: "missing port", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute, _ *networkingv1alpha.NetworkLoadBalancer, _ *networkingv1alpha.NetworkService) {
			r.Spec.Backends[0].NetworkService.Port = "absent"
		}, accepted: nlbRouteReasonAttached, resolved: nlbRouteReasonPortNotFound},
		{name: "protocol mismatch", mutate: func(_ *networkingv1alpha.NetworkLoadBalancerRoute, _ *networkingv1alpha.NetworkLoadBalancer, s *networkingv1alpha.NetworkService) {
			s.Spec.Ports[0].Protocol = networkingv1alpha.NetworkServiceProtocol("UDP")
		}, accepted: nlbRouteReasonAttached, resolved: nlbRouteReasonProtocolMismatch},
		{name: "invalid probe port", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute, _ *networkingv1alpha.NetworkLoadBalancer, _ *networkingv1alpha.NetworkService) {
			r.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "absent"}
		}, accepted: nlbRouteReasonAttached, resolved: nlbRouteReasonHealthCheckInvalid},
		{name: "all backends disabled", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute, _ *networkingv1alpha.NetworkLoadBalancer, _ *networkingv1alpha.NetworkService) {
			r.Spec.Backends[0].Weight = ptr.To(int32(0))
		}, accepted: nlbRouteReasonAttached, resolved: nlbRouteReasonResolved},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			route := testLoadBalancerRoute("route", time.Unix(10, 0))
			parent := testRouteLoadBalancer()
			service := testRouteNetworkService()
			if tt.mutate != nil {
				tt.mutate(route, parent, service)
			}
			objects := []client.Object{route}
			if !tt.omitParent {
				objects = append(objects, parent)
			}
			if !tt.omitService {
				objects = append(objects, service)
			}
			cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(objects...).WithStatusSubresource(route).Build()
			require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
			var got networkingv1alpha.NetworkLoadBalancerRoute
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
			require.Len(t, got.Status.Parents, 1)
			assert.Equal(t, tt.accepted, apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteAccepted).Reason)
			assert.Equal(t, tt.resolved, apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
			assert.Equal(t, metav1.ConditionFalse, apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteProgrammed).Status)
		})
	}
}

func TestNetworkLoadBalancerRouteEndpointResolution(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.Backends[0].Weight = ptr.To(int32(7))
	service := testRouteNetworkService()
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(
		service,
		testRouteMember("ready-v6", true, "fd00::1/128"),
		testRouteMember("unready-v6", false, "fd00::2/128"),
		testRouteMember("ready-v4", true, "10.0.0.1/32"),
	).Build()
	parent := testRouteLoadBalancer()
	endpoints, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	assert.Equal(t, "fd00::1", endpoints[0].Address)
	assert.Equal(t, "cell-test", endpoints[0].SourceNamespace)
	assert.Equal(t, "source-ready-v6", endpoints[0].SourceUID)
	assert.Equal(t, "dfw", endpoints[0].Location)
	assert.Equal(t, int32(7), endpoints[0].Weight)

	parent.Spec.Exposure.Addresses[0].Locations = networkingv1alpha.NetworkLoadBalancerLocationSelection{
		From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"ord"},
	}
	endpoints, err = resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	require.NoError(t, err)
	assert.Empty(t, endpoints)

	parent.Spec.Exposure.Addresses[0].Locations.From = networkingv1alpha.NetworkLoadBalancerLocationSourceAll
	member := testRouteMember("missing-source-uid", true, "fd00::3/128")
	delete(member.Labels, networkingv1alpha.NetworkInterfaceSourceUIDLabel)
	cl = fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(service, member).Build()
	endpoints, err = resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	require.NoError(t, err)
	assert.Empty(t, endpoints)
}

func TestNetworkLoadBalancerRouteUDPRequiresTCPProbe(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.Spec.Listeners[0].Protocol = networkingv1alpha.NetworkLoadBalancerProtocolUDP
	service := testRouteNetworkService()
	service.Spec.Ports[0].Protocol = networkingv1alpha.NetworkServiceProtocol("UDP")
	service.Spec.Ports = append(service.Spec.Ports, networkingv1alpha.NetworkServicePort{Name: "health", Port: 8081})
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(service, testRouteMember("ready", true, "fd00::1/128")).Build()

	_, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	var referenceErr *networkLoadBalancerRouteReferenceError
	require.ErrorAs(t, err, &referenceErr)
	assert.Equal(t, nlbRouteReasonHealthCheckInvalid, referenceErr.reason)

	route.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "health"}
	endpoints, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	assert.Equal(t, int32(8080), endpoints[0].Port)
}

func TestNetworkLoadBalancerRouteRejectsForeignNetwork(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	member := testRouteMember("foreign", true, "fd00::1/128")
	member.Spec.Network.Name = "other"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(testRouteNetworkService(), member).Build()
	_, err := resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	var referenceErr *networkLoadBalancerRouteReferenceError
	require.ErrorAs(t, err, &referenceErr)
	assert.Equal(t, nlbRouteReasonNetworkMismatch, referenceErr.reason)
}
