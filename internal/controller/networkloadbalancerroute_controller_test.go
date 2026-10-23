// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"sync/atomic"
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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
				Name: "regional", Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, Locations: &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll},
			}}},
			Listeners: []networkingv1alpha.NetworkLoadBalancerListener{{
				Name: "tcp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"regional"},
			}},
		},
		Status: networkingv1alpha.NetworkLoadBalancerStatus{Endpoints: []networkingv1alpha.NetworkLoadBalancerEndpointStatus{{
			RequestName: "regional", Location: "dfw", Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{{
				Family: networkingv1alpha.IPv6Protocol, Value: "2001:db8::1",
				ClaimRef:   &networkingv1alpha.NetworkLoadBalancerIPClaimRef{Namespace: "test", Name: "claim"},
				Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkLoadBalancerAllocated, Status: metav1.ConditionTrue, Reason: "Test"}},
			}},
		}}},
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

func TestNetworkLoadBalancerRouteCanonicalListenerOwnership(t *testing.T) {
	for _, tt := range []struct {
		name     string
		olderRef networkingv1alpha.NetworkLoadBalancerRouteParentRef
		newerRef networkingv1alpha.NetworkLoadBalancerRouteParentRef
	}{
		{name: "port selector", olderRef: networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge", Port: ptr.To(int32(443))},
			newerRef: networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge", Listener: "tcp"}},
		{name: "explicit local namespace", olderRef: networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge", Listener: "tcp", Namespace: "test"},
			newerRef: networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge", Listener: "tcp"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			older := testLoadBalancerRoute("older", time.Unix(10, 0))
			newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
			older.Spec.ParentRefs[0] = tt.olderRef
			newer.Spec.ParentRefs[0] = tt.newerRef
			cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
				WithObjects(older, newer, testRouteLoadBalancer(), testRouteNetworkService()).
				WithStatusSubresource(older, newer).Build()
			r := &NetworkLoadBalancerRouteReconciler{}
			require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(older)))
			require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
			var gotOlder, gotNewer networkingv1alpha.NetworkLoadBalancerRoute
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(older), &gotOlder))
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &gotNewer))
			require.Equal(t, "tcp", gotOlder.Status.Parents[0].Listener)
			require.True(t, gotOlder.Status.Parents[0].Effective)
			require.False(t, gotNewer.Status.Parents[0].Effective)
		})
	}
}

func TestNetworkLoadBalancerRouteSecondReconcileDoesNotWriteStatus(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, testRouteLoadBalancer(), testRouteNetworkService()).WithStatusSubresource(route).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	key := client.ObjectKeyFromObject(route)
	require.NoError(t, r.reconcileRoute(ctx, cl, key))
	var first, second networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, key, &first))
	require.NotEmpty(t, first.ResourceVersion)
	require.NoError(t, r.reconcileRoute(ctx, cl, key))
	require.NoError(t, cl.Get(ctx, key, &second))
	assert.Equal(t, first.ResourceVersion, second.ResourceVersion)
	assert.Equal(t, first.Status, second.Status)
}

func TestNetworkLoadBalancerRouteUnsupportedOlderRouteDoesNotOwnListener(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	older.Spec.SNIHostnames = []string{"example.com"}
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, testRouteLoadBalancer(), testRouteNetworkService()).
		WithStatusSubresource(older, newer).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(older)))
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	var gotOlder, gotNewer networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(older), &gotOlder))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &gotNewer))
	assert.False(t, gotOlder.Status.Parents[0].Effective)
	assert.Equal(t, nlbRouteReasonUnsupportedCapability,
		apimeta.FindStatusCondition(gotOlder.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteAccepted).Reason)
	assert.True(t, gotNewer.Status.Parents[0].Effective)
}

func TestNetworkLoadBalancerRouteUnsupportedListenerDoesNotHaveOwner(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*networkingv1alpha.NetworkLoadBalancer)
	}{
		{name: "TLS", mutate: func(parent *networkingv1alpha.NetworkLoadBalancer) {
			parent.Spec.Listeners[0].Protocol = networkingv1alpha.NetworkLoadBalancerProtocolTLS
		}},
		{name: "shared address", mutate: func(parent *networkingv1alpha.NetworkLoadBalancer) {
			parent.Spec.Listeners = append(parent.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{
				Name: "other", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP,
				Port: 444, AddressRefs: []string{"regional"},
			})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := testRouteLoadBalancer()
			tt.mutate(parent)
			older := testLoadBalancerRoute("older", time.Unix(10, 0))
			newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
			assert.Nil(t, oldestRouteForListener([]networkingv1alpha.NetworkLoadBalancerRoute{*older, *newer}, parent, "tcp"))
		})
	}
}

func TestNetworkLoadBalancerRouteRejectsForeignParentNamespace(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.ParentRefs[0].Namespace = "foreign"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, testRouteLoadBalancer(), testRouteNetworkService()).WithStatusSubresource(route).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
	require.Len(t, got.Status.Parents, 1)
	assert.False(t, got.Status.Parents[0].Effective)
	for _, conditionType := range []string{networkingv1alpha.NetworkLoadBalancerRouteAccepted, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs} {
		condition := apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, conditionType)
		require.NotNil(t, condition)
		assert.Equal(t, metav1.ConditionFalse, condition.Status)
		assert.Equal(t, nlbRouteReasonUnsupportedCapability, condition.Reason)
	}
}

func TestNetworkLoadBalancerRouteAmbiguousPortSelector(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.ParentRefs[0] = networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge", Port: ptr.To(int32(443))}
	parent := testRouteLoadBalancer()
	parent.Spec.Listeners = append(parent.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{
		Name: "udp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 443,
		AddressRefs: []string{"regional"},
	})
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent).WithStatusSubresource(route).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
	require.Equal(t, "port:443", got.Status.Parents[0].Listener)
	require.False(t, got.Status.Parents[0].Effective)
	require.Equal(t, nlbRouteReasonListenerNotFound,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteAccepted).Reason)
}

func TestNetworkLoadBalancerRouteDeduplicatesResolvedParentStatus(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.ParentRefs = append(route.Spec.ParentRefs,
		networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge", Port: ptr.To(int32(443))})
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, testRouteLoadBalancer(), testRouteNetworkService()).WithStatusSubresource(route).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
	require.Len(t, got.Status.Parents, 1)
	require.Equal(t, "tcp", got.Status.Parents[0].Listener)
}

func TestNetworkLoadBalancerRouteExplicitLocalBackendNamespace(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.Backends[0].NetworkService.Namespace = "test"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, testRouteLoadBalancer(), testRouteNetworkService()).WithStatusSubresource(route).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
	require.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}

func TestNetworkLoadBalancerRouteRejectsBackendSocketReuseAcrossNLBs(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	newer.Spec.ParentRefs[0].Name = "edge-2"
	firstParent := testRouteLoadBalancer()
	secondParent := testRouteLoadBalancer()
	secondParent.Name = "edge-2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, firstParent, secondParent, testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(older, newer).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(older)))
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &got))
	require.Equal(t, nlbRouteReasonBackendClaimConflict,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)

	require.NoError(t, cl.Delete(ctx, older))
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &got))
	require.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}

func TestNetworkLoadBalancerRouteUnallocatedParentDoesNotClaimSocket(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	newer.Spec.ParentRefs[0].Name = "edge-2"
	firstParent := testRouteLoadBalancer()
	firstParent.Status.Endpoints = nil
	secondParent := testRouteLoadBalancer()
	secondParent.Name = "edge-2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, firstParent, secondParent, testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(older, newer).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &got))
	require.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}

func TestNetworkLoadBalancerRouteBlockedRouteDoesNotClaimAnotherSocket(t *testing.T) {
	ctx := context.Background()
	objects := make([]client.Object, 0, 12)
	for index, name := range []string{"a", "b", "c"} {
		route := testLoadBalancerRoute(name, time.Unix(int64(index+1), 0))
		parent := testRouteLoadBalancer()
		parent.Name = "edge-" + name
		route.Spec.ParentRefs[0].Name = parent.Name
		service := testRouteNetworkService()
		service.Name = "service-" + name
		service.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{name: "yes"}
		route.Spec.Backends[0].NetworkService.Name = service.Name
		objects = append(objects, route, parent, service)
	}
	for _, member := range []struct {
		name, address string
		labels        map[string]string
	}{
		{"x", "fd00::1/128", map[string]string{"a": "yes"}},
		{"y", "fd00::2/128", map[string]string{"a": "yes", "b": "yes"}},
		{"z", "fd00::3/128", map[string]string{"b": "yes", "c": "yes"}},
	} {
		iface := testRouteMember(member.name, true, member.address)
		for key, value := range member.labels {
			iface.Labels[key] = value
		}
		objects = append(objects, iface)
	}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(objects...).
		WithStatusSubresource(&networkingv1alpha.NetworkLoadBalancerRoute{}).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKey{Namespace: "test", Name: name}))
	}
	for name, reason := range map[string]string{"a": nlbRouteReasonResolved, "b": nlbRouteReasonBackendClaimConflict, "c": nlbRouteReasonResolved} {
		var route networkingv1alpha.NetworkLoadBalancerRoute
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: "test", Name: name}, &route))
		require.Equal(t, reason, apimeta.FindStatusCondition(route.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason, name)
	}
}

func TestNetworkLoadBalancerRouteRejectsMultipleVIPsOnOneListener(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	second := parent.Spec.Exposure.Addresses[0]
	second.Name = "second"
	parent.Spec.Exposure.Addresses = append(parent.Spec.Exposure.Addresses, second)
	parent.Spec.Listeners[0].AddressRefs = []string{"regional", "second"}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(route).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
	require.Equal(t, nlbRouteReasonBackendClaimConflict,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}

func TestNetworkLoadBalancerRouteRejectsSameRouteBackendSocketReuseAcrossNLBs(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.ParentRefs = append(route.Spec.ParentRefs,
		networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge-2", Listener: "tcp"})
	secondParent := testRouteLoadBalancer()
	secondParent.Name = "edge-2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, testRouteLoadBalancer(), secondParent, testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).WithStatusSubresource(route).Build()
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(route), &got))
	require.Len(t, got.Status.Parents, 2)
	require.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
	require.Equal(t, nlbRouteReasonBackendClaimConflict,
		apimeta.FindStatusCondition(got.Status.Parents[1].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}

func TestNetworkLoadBalancerRouteIgnoresRejectedSharedAddressClaim(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	newer.Spec.ParentRefs[0].Name = "edge-2"
	firstParent := testRouteLoadBalancer()
	firstParent.Spec.Listeners = append(firstParent.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{
		Name: "other", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP,
		Port: 444, AddressRefs: []string{"regional"},
	})
	secondParent := testRouteLoadBalancer()
	secondParent.Name = "edge-2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, firstParent, secondParent, testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).WithStatusSubresource(older, newer).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(older)))
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	var gotOlder, gotNewer networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(older), &gotOlder))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &gotNewer))
	require.Equal(t, nlbRouteReasonUnsupportedCapability,
		apimeta.FindStatusCondition(gotOlder.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteAccepted).Reason)
	require.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(gotNewer.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
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
		{name: "multiple backends gated", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute, _ *networkingv1alpha.NetworkLoadBalancer, _ *networkingv1alpha.NetworkService) {
			r.Spec.Backends = append(r.Spec.Backends, r.Spec.Backends[0])
		}, accepted: nlbRouteReasonAttached, resolved: nlbRouteReasonUnsupportedCapability},
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

	parent.Spec.Exposure.Addresses[0].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{
		From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"ord"},
	}
	endpoints, err = resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	require.NoError(t, err)
	assert.Empty(t, endpoints)

	parent.Spec.Exposure.Addresses[0].Locations = nil
	endpoints, err = resolveNetworkLoadBalancerRouteBackends(ctx, cl, route, parent, &parent.Spec.Listeners[0])
	require.NoError(t, err)
	require.Len(t, endpoints, 1, "omitted locations must follow backend membership")

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

func TestNetworkLoadBalancerRouteSocketCheckScalesWithRouteCount(t *testing.T) {
	ctx := context.Background()
	const routes = 32
	objects := make([]client.Object, 0, routes*4)
	for index := range routes {
		name := fmt.Sprintf("r%02d", index)
		route := testLoadBalancerRoute(name, time.Unix(int64(index+1), 0))
		parent := testRouteLoadBalancer()
		parent.Name = "edge-" + name
		route.Spec.ParentRefs[0].Name = parent.Name
		service := testRouteNetworkService()
		service.Name = "service-" + name
		service.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{name: "yes"}
		route.Spec.Backends[0].NetworkService.Name = service.Name
		member := testRouteMember("member-"+name, true, fmt.Sprintf("fd00::%x/128", index+1))
		member.Labels[name] = "yes"
		objects = append(objects, route, parent, service, member)
	}
	var interfaceLists atomic.Int64
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(objects...).
		WithStatusSubresource(&networkingv1alpha.NetworkLoadBalancerRoute{}).
		WithInterceptorFuncs(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*networkingv1alpha.NetworkInterfaceList); ok {
				interfaceLists.Add(1)
			}
			return c.List(ctx, list, opts...)
		}}).Build()

	newest := client.ObjectKey{Namespace: "test", Name: fmt.Sprintf("r%02d", routes-1)}
	require.NoError(t, (&NetworkLoadBalancerRouteReconciler{}).reconcileRoute(ctx, cl, newest))
	assert.LessOrEqual(t, interfaceLists.Load(), int64(routes*2), "socket ownership must resolve each route's backends once per reconcile")

	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, newest, &got))
	assert.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}

func TestNetworkLoadBalancerRouteListenerOwnerTieBreaksByName(t *testing.T) {
	ctx := context.Background()
	created := time.Unix(10, 0)
	first := testLoadBalancerRoute("a", created)
	second := testLoadBalancerRoute("b", created)
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(second, first, testRouteLoadBalancer(), testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(first, second).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{second, first} {
		require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	}
	for name, effective := range map[string]bool{"a": true, "b": false} {
		var got networkingv1alpha.NetworkLoadBalancerRoute
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: "test", Name: name}, &got))
		assert.Equal(t, effective, got.Status.Parents[0].Effective, name)
	}
}

func TestNetworkLoadBalancerRouteSocketOwnerTieBreaksByName(t *testing.T) {
	ctx := context.Background()
	created := time.Unix(10, 0)
	first := testLoadBalancerRoute("a", created)
	second := testLoadBalancerRoute("b", created)
	second.Spec.ParentRefs[0].Name = "edge-2"
	secondParent := testRouteLoadBalancer()
	secondParent.Name = "edge-2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(second, first, testRouteLoadBalancer(), secondParent, testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(first, second).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{second, first} {
		require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
	}
	for name, reason := range map[string]string{"a": nlbRouteReasonResolved, "b": nlbRouteReasonBackendClaimConflict} {
		var got networkingv1alpha.NetworkLoadBalancerRoute
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: "test", Name: name}, &got))
		assert.Equal(t, reason, apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason, name)
	}
}

func TestNetworkLoadBalancerRouteSocketClaimsAreScopedToOneNetwork(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	newer.Spec.ParentRefs[0].Name = "edge-2"
	newer.Spec.Backends[0].NetworkService.Name = "storefront-2"
	secondParent := testRouteLoadBalancer()
	secondParent.Name = "edge-2"
	secondParent.Spec.NetworkRef.Name = "other"
	secondService := testRouteNetworkService()
	secondService.Name = "storefront-2"
	secondService.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": "storefront-2"}
	secondMember := testRouteMember("member-2", true, "fd00::1/128")
	secondMember.Labels["app"] = "storefront-2"
	secondMember.Spec.Network.Name = "other"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, testRouteLoadBalancer(), secondParent, testRouteNetworkService(), secondService,
			testRouteMember("member", true, "fd00::1/128"), secondMember).
		WithStatusSubresource(older, newer).Build()
	r := &NetworkLoadBalancerRouteReconciler{}
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(older)))
	require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(newer)))
	var got networkingv1alpha.NetworkLoadBalancerRoute
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(newer), &got))
	assert.Equal(t, nlbRouteReasonResolved,
		apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason)
}
