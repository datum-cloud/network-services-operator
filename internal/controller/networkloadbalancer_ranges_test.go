// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"
	api "go.datum.net/network-services-operator/api/v1alpha"
	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNLBRangeResolution(t *testing.T) {
	for _, tc := range []struct {
		name              string
		frontend, backend api.PortRange
		valid             bool
	}{
		{"PreserveRange", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 9000, End: 11000}, true},
		{"MissingFirst", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 10001, End: 10127}, false},
		{"MissingLast", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 10000, End: 10126}, false},
		{"OffsetTranslation", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 20000, End: 20127}, false},
		{"RangeToSingleton", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 10000, End: 10000}, false},
		{"SingletonRemapping", api.PortRange{Start: 5001, End: 5001}, api.PortRange{Start: 6001, End: 6001}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lb := testRouteLoadBalancer()
			listener := &lb.Spec.Listeners[0]
			listener.Protocol = api.NetworkLoadBalancerProtocolUDP
			listener.Port = 0
			listener.PortRange = &tc.frontend
			route := testLoadBalancerRoute("media", time.Unix(1, 0))
			route.Spec.Backends[0].HealthCheck = &api.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "health"}
			service := testRouteNetworkService()
			service.Spec.Ports = []api.NetworkServicePort{{Name: "http", Protocol: api.NetworkServiceProtocolUDP, PortRange: &tc.backend}, {Name: "health", Port: 8080}}
			cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(service, testRouteMember("member", true, "fd00::1/128")).Build()
			endpoints, err := resolveNetworkLoadBalancerRouteBackends(context.Background(), cl, route, lb, listener)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, endpoints, 1)
			expected := tc.frontend
			if tc.frontend.Singleton() {
				expected = tc.backend
			}
			require.Equal(t, expected, api.PortInterval(endpoints[0].Port, endpoints[0].PortRange))
			require.EqualValues(t, 8080, endpoints[0].Probe.Port)
		})
	}
}

func TestNLBRangeRequiresNamedParentAndSingletonProbe(t *testing.T) {
	lb := testRouteLoadBalancer()
	listener := &lb.Spec.Listeners[0]
	listener.Port = 0
	listener.PortRange = &api.PortRange{Start: 443, End: 443}
	port := int32(443)
	_, ok := ResolveNetworkLoadBalancerRouteListener(lb, api.NetworkLoadBalancerRouteParentRef{Port: &port})
	require.False(t, ok)
	_, ok = ResolveNetworkLoadBalancerRouteListener(lb, api.NetworkLoadBalancerRouteParentRef{Listener: listener.Name})
	require.True(t, ok)
	service := testRouteNetworkService()
	service.Spec.Ports[0].Port = 0
	service.Spec.Ports[0].PortRange = &api.PortRange{Start: 8000, End: 8001}
	backend := api.NetworkLoadBalancerRouteBackend{HealthCheck: &api.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "http"}}
	require.Error(t, validateNetworkLoadBalancerHealthCheck(service, &backend, api.NetworkServiceProtocolUDP))
}

func TestNLBSharedAddressKeepsServingAndWithdrawalObligations(t *testing.T) {
	lb, healthy := dataPlaneFixture()
	healthy.Spec.Source.ListenerName = "web"
	healthy.Spec.Source.ParentGeneration = lb.Generation
	lb.Spec.Listeners = []api.NetworkLoadBalancerListener{{Name: "web", Protocol: api.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"public"}}, {Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, Port: 5001, AddressRefs: []string{"public"}}}
	failed := healthy
	failed.Name = "media"
	failed.Spec.Source.ListenerName = "media"
	reportIntent(&healthy, metav1.ConditionTrue, metav1.ConditionTrue)
	reportIntent(&failed, metav1.ConditionFalse, metav1.ConditionFalse)
	retiring := failed
	retiring.Name = "old-media"
	retiring.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	retiring.Status = api.NetworkLoadBalancerCellIntentStatus{}
	applyNetworkLoadBalancerDataPlane(lb, []api.NetworkLoadBalancerCellIntent{healthy, failed, retiring}, []api.NetworkLoadBalancerRoute{
		rejectedRoute("media-route", lb.Name, "media", true, metav1.Condition{
			Type: api.NetworkLoadBalancerRouteResolvedRefs, Status: metav1.ConditionFalse, Reason: nlbRouteReasonBackendClaimConflict,
		}),
	})
	address := lb.Status.Endpoints[0].Addresses[0]
	require.True(t, address.Announced)
	require.True(t, address.WithdrawalPending)
	require.True(t, addressPendingWithdrawal(address))
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, api.NetworkLoadBalancerReady))
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, "Degraded"))
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Endpoints[0].Listeners[0].Conditions, api.NetworkLoadBalancerProgrammed))
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Endpoints[0].Listeners[1].Conditions, api.NetworkLoadBalancerProgrammed))
	healthy.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	failed.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	reportIntent(&healthy, metav1.ConditionFalse, metav1.ConditionFalse)
	applyNetworkLoadBalancerDataPlane(lb, []api.NetworkLoadBalancerCellIntent{healthy, failed, retiring}, nil)
	require.True(t, lb.Status.Endpoints[0].Addresses[0].WithdrawalPending)
	reportIntent(&retiring, metav1.ConditionFalse, metav1.ConditionFalse)
	applyNetworkLoadBalancerDataPlane(lb, []api.NetworkLoadBalancerCellIntent{healthy, failed, retiring}, nil)
	require.False(t, addressPendingWithdrawal(lb.Status.Endpoints[0].Addresses[0]))
}

func TestNLBListenerStatusRequiresEveryCurrentLocation(t *testing.T) {
	lb := testRouteLoadBalancer()
	lb.Generation = 4
	listener := &lb.Spec.Listeners[0]
	listener.PortRange = &api.PortRange{Start: 443, End: 443}
	listener.Port = 0
	good := api.NetworkLoadBalancerListenerStatus{Name: listener.Name, Conditions: []metav1.Condition{{Type: api.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue, ObservedGeneration: 4}}}
	lb.Status.Endpoints[0].Listeners = []api.NetworkLoadBalancerListenerStatus{good}
	second := lb.Status.Endpoints[0]
	second.Location = "iad"
	second.Listeners = nil
	lb.Status.Endpoints = append(lb.Status.Endpoints, second)
	c, ok := listenerAddressesProgrammed(lb, listener)
	require.True(t, ok)
	require.Equal(t, metav1.ConditionUnknown, c.Status)
	stale := good
	stale.Conditions = append([]metav1.Condition(nil), good.Conditions...)
	stale.Conditions[0].ObservedGeneration = 3
	lb.Status.Endpoints[1].Listeners = []api.NetworkLoadBalancerListenerStatus{stale}
	c, _ = listenerAddressesProgrammed(lb, listener)
	require.Equal(t, metav1.ConditionUnknown, c.Status)
	lb.Status.Endpoints[1].Listeners = []api.NetworkLoadBalancerListenerStatus{good}
	c, _ = listenerAddressesProgrammed(lb, listener)
	require.Equal(t, metav1.ConditionTrue, c.Status)
}

func rangeProgrammingFixture(t *testing.T) (client.Client, *NetworkLoadBalancerCellProgrammingReconciler, *api.NetworkLoadBalancerCellIntent) {
	cl, r, intent := nlbProgrammingFixture(t)
	r.SharedAddressListeners = true
	r.UDPPortRanges = true
	intent.Spec.State = api.NetworkLoadBalancerCellIntentValidateAllocation
	intent.Spec.Source.ParentGeneration = 3
	intent.Spec.Source.ListenerName = "media"
	intent.Spec.Protocol = api.NetworkLoadBalancerProtocolUDP
	intent.Spec.ListenerPort = 0
	intent.Spec.ListenerPortRange = &api.PortRange{Start: 10000, End: 10127}
	intent.Spec.Backends[0].Port = 0
	intent.Spec.Backends[0].PortRange = intent.Spec.ListenerPortRange
	intent.Spec.AddressGroup = &api.NetworkLoadBalancerCellIntentAddressGroup{UID: "allocation-1", Generation: 3, Revision: "revision-3", ListenerName: "media", Listeners: []api.NetworkLoadBalancerCellIntentAddressListener{{Name: "web", Protocol: api.NetworkLoadBalancerProtocolTCP, Port: 443}, {Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, PortRange: intent.Spec.ListenerPortRange}}}
	require.NoError(t, cl.Update(context.Background(), intent))
	createTestAllocationAuthority(t, cl, intent)
	return cl, r, intent
}

func TestNLBRangeCellFailsClosedUntilEveryGatewayCapable(t *testing.T) {
	cl, r, intent := rangeProgrammingFixture(t)
	ctx := context.Background()
	reconcileNLBProgramming(t, r, intent)
	require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(intent), &networkv1alpha1.NetworkService{})))
	for _, name := range []string{"gateway-a", "gateway-b"} {
		require.NoError(t, cl.Create(ctx, &networkv1alpha1.NetworkGateway{ObjectMeta: metav1.ObjectMeta{Namespace: "galactic-system", Name: name, Generation: 2}, Status: networkv1alpha1.NetworkGatewayStatus{ObservedGeneration: 2, BackendHealthFiltering: true, Capabilities: []string{networkv1alpha1.CapabilityTerminalAddressRetirement, networkv1alpha1.CapabilitySharedAddressListeners}}}))
	}
	reconcileNLBProgramming(t, r, intent)
	require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(intent), &networkv1alpha1.NetworkService{})))
	var gateways networkv1alpha1.NetworkGatewayList
	require.NoError(t, cl.List(ctx, &gateways))
	for i := range gateways.Items {
		gateway := &gateways.Items[i]
		gateway.Status.Capabilities = append(gateway.Status.Capabilities, networkv1alpha1.CapabilityUDPPortRanges)
		require.NoError(t, cl.Update(ctx, gateway))
	}
	reconcileNLBProgramming(t, r, intent)
	var service networkv1alpha1.NetworkService
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(intent), &service))
	require.Equal(t, &networkv1alpha1.PortRange{Start: 10000, End: 10127}, service.Spec.PortRange)
	require.Zero(t, service.Spec.Port)
	require.Zero(t, service.Spec.BackendPort)
	var anchor networkv1alpha1.NetworkServiceAddress
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: intent.Namespace, Name: networkLoadBalancerAddressName(intent.Spec.AddressGroup.UID)}, &anchor))
	require.Len(t, anchor.Spec.Group.Listeners, 2)
	gateway := &gateways.Items[0]
	gateway.Generation = 3
	require.NoError(t, cl.Update(ctx, gateway))
	reconcileNLBProgramming(t, r, intent)
	require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(intent), &service)))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(&anchor), &anchor))
}

func TestNLBAddressInventoryRejectsStaleAndConflictingWriters(t *testing.T) {
	cl, r, intent := rangeProgrammingFixture(t)
	ctx := context.Background()
	backends, _, err := r.verifiedBackends(ctx, intent)
	require.NoError(t, err)
	service, reason := desiredNetworkLoadBalancerService(intent, backends)
	require.Empty(t, reason)
	require.NoError(t, r.ensureAddress(ctx, service))
	service.Spec.AddressGroup.Generation--
	require.Error(t, r.ensureAddress(ctx, service))
	service.Spec.AddressGroup.Generation++
	service.Spec.AddressGroup.Revision = "conflict"
	require.Error(t, r.ensureAddress(ctx, service))
	var anchor networkv1alpha1.NetworkServiceAddress
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: intent.Namespace, Name: networkLoadBalancerAddressName(intent.Spec.AddressGroup.UID)}, &anchor))
	require.EqualValues(t, 3, anchor.Spec.Group.Generation)
	anchor.Finalizers = []string{"test.galactic/withdrawal"}
	require.NoError(t, cl.Update(ctx, &anchor))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(intent), intent))
	intent.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	intent.Spec.Backends = nil
	require.NoError(t, cl.Update(ctx, intent))
	done, err := r.cleanupAddresses(ctx, intent.Namespace)
	require.NoError(t, err)
	require.False(t, done)
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(&anchor), &anchor))
	require.False(t, anchor.DeletionTimestamp.IsZero())
	anchor.Finalizers = nil
	require.NoError(t, cl.Update(ctx, &anchor))
	done, err = r.cleanupAddresses(ctx, intent.Namespace)
	require.NoError(t, err)
	require.True(t, done)
}

func TestNLBNewListenerDoesNotInheritOldProgramming(t *testing.T) {
	lb, healthy := dataPlaneFixture()
	lb.Spec.Listeners = []api.NetworkLoadBalancerListener{{Name: "web", Protocol: api.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"public"}}, {Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, Port: 5001, AddressRefs: []string{"public"}}}
	healthy.Spec.Source.ListenerName = "web"
	healthy.Spec.Source.ParentGeneration = lb.Generation
	reportIntent(&healthy, metav1.ConditionTrue, metav1.ConditionTrue)
	lb.Generation++
	applyNetworkLoadBalancerDataPlane(lb, []api.NetworkLoadBalancerCellIntent{healthy}, nil)
	require.True(t, lb.Status.Endpoints[0].Addresses[0].Announced)
	require.True(t, lb.Status.Endpoints[0].Addresses[0].WithdrawalPending)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, api.NetworkLoadBalancerProgrammed))
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, "Degraded"))
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Endpoints[0].Listeners[1].Conditions, api.NetworkLoadBalancerProgrammed))
}

func TestNLBRangeBackendOwnershipIntervals(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second api.PortRange
		protocol      api.NetworkLoadBalancerProtocol
		conflict      bool
	}{
		{"shared endpoint", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 10127, End: 10200}, api.NetworkLoadBalancerProtocolUDP, true},
		{"adjacent", api.PortRange{Start: 10000, End: 10127}, api.PortRange{Start: 10128, End: 10200}, api.NetworkLoadBalancerProtocolUDP, false},
		{"same port different protocol", api.PortRange{Start: 443, End: 443}, api.PortRange{Start: 443, End: 443}, api.NetworkLoadBalancerProtocolTCP, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intents := []api.NetworkLoadBalancerCellIntent{{Spec: api.NetworkLoadBalancerCellIntentSpec{NetworkName: "net", Location: "iad", Protocol: api.NetworkLoadBalancerProtocolUDP, State: api.NetworkLoadBalancerCellIntentValidate, Backends: []api.NetworkLoadBalancerCellIntentBackend{{Address: "fd00::1", PortRange: &tc.first}}}}, {Spec: api.NetworkLoadBalancerCellIntentSpec{NetworkName: "net", Location: "iad", Protocol: tc.protocol, State: api.NetworkLoadBalancerCellIntentValidate, Backends: []api.NetworkLoadBalancerCellIntentBackend{{Address: "fd00::1", PortRange: &tc.second}}}}}
			withdrawConflictingCellIntents(intents)
			require.Equal(t, tc.conflict, intents[0].Spec.State == api.NetworkLoadBalancerCellIntentWithdrawn)
			require.Equal(t, tc.conflict, intents[1].Spec.State == api.NetworkLoadBalancerCellIntentWithdrawn)
		})
	}
}

func TestNLBMissingIntentCannotRecreateAnchor(t *testing.T) {
	cl, r, intent := rangeProgrammingFixture(t)
	backends, _, err := r.verifiedBackends(context.Background(), intent)
	require.NoError(t, err)
	service, reason := desiredNetworkLoadBalancerService(intent, backends)
	require.Empty(t, reason)
	require.NoError(t, cl.Delete(context.Background(), intent))
	require.ErrorContains(t, r.ensureAddress(context.Background(), service), "no longer authorizes")
}

func TestNLBInventoryRejectsMalformedListener(t *testing.T) {
	_, _, intent := rangeProgrammingFixture(t)
	require.Empty(t, validateAddressGroup(intent))
	intent.Spec.AddressGroup.Listeners[1].PortRange = &api.PortRange{Start: 10001, End: 10127}
	require.Contains(t, validateAddressGroup(intent), "does not match")
}

func TestNLBInventoryChangeKeepsAllocationIdentity(t *testing.T) {
	lb := testRouteLoadBalancer()
	lb.Spec.Listeners = append(lb.Spec.Listeners, api.NetworkLoadBalancerListener{Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, PortRange: &api.PortRange{Start: 10000, End: 10127}, AddressRefs: []string{"regional"}})
	allocation := api.NetworkLoadBalancerAddressStatus{ClaimRef: &api.NetworkLoadBalancerIPClaimRef{UID: "claim-a"}}
	before := networkLoadBalancerAddressGroup(lb, &lb.Spec.Listeners[0], "regional", "iad", allocation)
	lb.Generation++
	lb.Spec.Listeners[1].PortRange.End++
	after := networkLoadBalancerAddressGroup(lb, &lb.Spec.Listeners[0], "regional", "iad", allocation)
	require.Equal(t, before.UID, after.UID)
	require.NotEqual(t, before.Revision, after.Revision)
	require.Equal(t, lb.Generation, after.Generation)
	sibling := networkLoadBalancerAddressGroup(lb, &lb.Spec.Listeners[1], "regional", "iad", allocation)
	require.Equal(t, after.UID, sibling.UID)
	require.Equal(t, after.Revision, sibling.Revision)
	allocation.ClaimRef.UID = "claim-b"
	replacement := networkLoadBalancerAddressGroup(lb, &lb.Spec.Listeners[0], "regional", "iad", allocation)
	require.NotEqual(t, after.UID, replacement.UID)
}

func TestNLBAllocationPreservesListenerStatus(t *testing.T) {
	old := api.NetworkLoadBalancerEndpointStatus{RequestName: "public", Location: "iad", Listeners: []api.NetworkLoadBalancerListenerStatus{{Name: "web", Conditions: []metav1.Condition{{Type: api.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue}}}}}
	var status []api.NetworkLoadBalancerEndpointStatus
	appendAddressStatus(&status, []api.NetworkLoadBalancerEndpointStatus{old}, "public", "iad", api.NetworkLoadBalancerAddressStatus{Value: "2001:db8::1"})
	require.Equal(t, old.Listeners, status[0].Listeners)
	status[0].Listeners[0].Conditions[0].Status = metav1.ConditionFalse
	require.Equal(t, metav1.ConditionTrue, old.Listeners[0].Conditions[0].Status)
}

func TestNLBMissingSelectedEndpointIsNotProgrammed(t *testing.T) {
	lb := testRouteLoadBalancer()
	lb.Spec.Listeners[0].PortRange = &api.PortRange{Start: 443, End: 443}
	lb.Spec.Listeners[0].Port = 0
	lb.Status.Endpoints[0].Listeners = []api.NetworkLoadBalancerListenerStatus{{Name: lb.Spec.Listeners[0].Name, Conditions: []metav1.Condition{{Type: api.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue, ObservedGeneration: lb.Generation}}}}
	lb.Status.AddressRequests = []api.NetworkLoadBalancerAddressRequestStatus{{Name: lb.Status.Endpoints[0].RequestName, ResolvedLocations: []string{lb.Status.Endpoints[0].Location, "missing"}}}
	condition, known := listenerAddressesProgrammed(lb, &lb.Spec.Listeners[0])
	require.True(t, known)
	require.Equal(t, metav1.ConditionUnknown, condition.Status)
}

func TestNLBRangePublishesOneIntentWithCompleteInventory(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("media", time.Unix(10, 0))
	route.UID = "route-media"
	route.Spec.ParentRefs[0].Listener = "media"
	route.Spec.Backends[0].HealthCheck = &api.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "health"}
	parent := testRouteLoadBalancer()
	parent.UID = "parent-1"
	parent.Generation = 5
	parent.Spec.Listeners = append(parent.Spec.Listeners, api.NetworkLoadBalancerListener{Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, PortRange: &api.PortRange{Start: 10000, End: 10127}, AddressRefs: []string{"regional"}})
	parent.Status.Endpoints = []api.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", parent.Generation)}
	parent.Status.Endpoints[0].Addresses[0].ClaimRef.UID = "claim-1"
	parent.Status.Endpoints[0].Addresses[0].Coordinated = true
	parent.Status.Endpoints[0].Addresses[0].WithdrawalPending = true
	service := testRouteNetworkService()
	service.Spec.Ports = []api.NetworkServicePort{{Name: "http", Protocol: api.NetworkServiceProtocolUDP, PortRange: &api.PortRange{Start: 9000, End: 11000}}, {Name: "health", Port: 8080}}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(route, parent, readyIntentNetwork(), service, testRouteMember("member", true, "fd00::1/128")).Build()
	intents, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, intents, 1)
	require.Equal(t, api.NetworkLoadBalancerCellIntentValidateAllocation, intents[0].Spec.State)
	require.Equal(t, parent.Spec.Listeners[1].PortRange, intents[0].Spec.ListenerPortRange)
	require.Equal(t, intents[0].Spec.ListenerPortRange, intents[0].Spec.Backends[0].PortRange)
	require.Len(t, intents[0].Spec.AddressGroup.Listeners, 2)
	require.Empty(t, validateAddressGroup(&intents[0]))
	parent.Status.Endpoints[0].Addresses[0].ClaimRef.UID = ""
	require.NoError(t, cl.Update(ctx, parent))
	intents, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, intents)
}

func TestNLBCoordinationPersistsUntilAllocationRelease(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.lb.Spec.Listeners = append(s.lb.Spec.Listeners, api.NetworkLoadBalancerListener{Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, Port: 5001, AddressRefs: []string{"regional"}})
	require.NoError(t, s.client.Update(s.ctx, s.lb))
	lb := s.reconcile()
	for _, endpoint := range lb.Status.Endpoints {
		require.Equal(t, endpoint.RequestName == "regional", endpoint.Addresses[0].Coordinated)
	}
	lb.Spec.Listeners = lb.Spec.Listeners[:1]
	lb.Generation++
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	for _, endpoint := range lb.Status.Endpoints {
		if endpoint.RequestName != "regional" {
			continue
		}
		require.True(t, endpoint.Addresses[0].Coordinated)
		group := networkLoadBalancerAddressGroup(&lb, &lb.Spec.Listeners[0], endpoint.RequestName, endpoint.Location, endpoint.Addresses[0])
		require.NotNil(t, group)
		require.Len(t, group.Listeners, 1)
	}
	require.NoError(t, s.client.Delete(s.ctx, &lb))
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(&lb))
	require.NoError(t, err)
	require.Empty(t, s.claims())
}

func TestNLBSharedToSingletonRetainsAnchorInEitherOrder(t *testing.T) {
	for _, withdrawFirst := range []bool{true, false} {
		name := "inventory-first"
		if withdrawFirst {
			name = "withdrawal-first"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cl, r, media := rangeProgrammingFixture(t)
			web := media.DeepCopy()
			web.Name = "web-intent"
			web.ResourceVersion = ""
			web.UID = "web-intent-uid"
			web.Spec.Source.ListenerName = "web"
			web.Spec.Protocol = api.NetworkLoadBalancerProtocolTCP
			web.Spec.ListenerPort = 443
			web.Spec.ListenerPortRange = nil
			web.Spec.Backends[0].Port = 8080
			web.Spec.Backends[0].PortRange = nil
			web.Spec.AddressGroup.ListenerName = "web"
			require.NoError(t, cl.Create(ctx, web))
			require.NoError(t, cl.Create(ctx, &networkv1alpha1.NetworkGateway{ObjectMeta: metav1.ObjectMeta{Name: "gateway", Namespace: "galactic-system", Generation: 1}, Status: networkv1alpha1.NetworkGatewayStatus{ObservedGeneration: 1, BackendHealthFiltering: true, Capabilities: []string{networkv1alpha1.CapabilityTerminalAddressRetirement, networkv1alpha1.CapabilitySharedAddressListeners, networkv1alpha1.CapabilityUDPPortRanges}}}))
			reconcileNLBProgramming(t, r, media)
			reconcileNLBProgramming(t, r, web)
			var anchor networkv1alpha1.NetworkServiceAddress
			anchorKey := client.ObjectKey{Namespace: web.Namespace, Name: networkLoadBalancerAddressName(web.Spec.AddressGroup.UID)}
			require.NoError(t, cl.Get(ctx, anchorKey, &anchor))
			require.Len(t, anchor.Spec.Group.Listeners, 2)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(media), media))
			media.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
			media.Spec.Backends = nil
			require.NoError(t, cl.Update(ctx, media))
			if withdrawFirst {
				reconcileNLBProgramming(t, r, media)
			}
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(web), web))
			web.Spec.Source.ParentGeneration = 4
			web.Spec.AddressGroup.Generation = 4
			web.Spec.AddressGroup.Revision = "single-listener"
			web.Spec.AddressGroup.Listeners = web.Spec.AddressGroup.Listeners[:1]
			require.NoError(t, cl.Update(ctx, web))
			reconcileNLBProgramming(t, r, web)
			if !withdrawFirst {
				reconcileNLBProgramming(t, r, media)
			}
			require.NoError(t, cl.Get(ctx, anchorKey, &anchor))
			require.Len(t, anchor.Spec.Group.Listeners, 1)
			require.EqualValues(t, 4, anchor.Spec.Group.Generation)
			var service networkv1alpha1.NetworkService
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(web), &service))
			require.NotNil(t, service.Spec.AddressGroup)
			require.Len(t, service.Spec.AddressGroup.Listeners, 1)
			require.Equal(t, anchor.Spec.Group.UID, service.Spec.AddressGroup.UID)
			require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(media), &networkv1alpha1.NetworkService{})))
			anchor.Finalizers = []string{"test.galactic/withdrawal"}
			require.NoError(t, cl.Update(ctx, &anchor))
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(web), web))
			web.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
			web.Spec.Backends = nil
			require.NoError(t, cl.Update(ctx, web))
			reconcileNLBProgramming(t, r, web)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(web), web))
			require.False(t, intentWithdrawalAcknowledged(web))
			require.NoError(t, cl.Get(ctx, anchorKey, &anchor))
			require.False(t, anchor.DeletionTimestamp.IsZero())
			anchor.Finalizers = nil
			require.NoError(t, cl.Update(ctx, &anchor))
			reconcileNLBProgramming(t, r, web)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(web), web))
			require.True(t, intentWithdrawalAcknowledged(web))
			require.True(t, apierrors.IsNotFound(cl.Get(ctx, anchorKey, &networkv1alpha1.NetworkServiceAddress{})))
		})
	}
}

func TestNLBRangeToSingletonKeepsCoordinationBeforeDataPlaneReport(t *testing.T) {
	for _, equalEndpoint := range []bool{true, false} {
		name := "scalar"
		if equalEndpoint {
			name = "equal-endpoints"
		}
		t.Run(name, func(t *testing.T) {
			s := newLoadBalancerAllocationScenario(t)
			s.lb.Spec.Listeners[0].Protocol = api.NetworkLoadBalancerProtocolUDP
			s.lb.Spec.Listeners[0].Port = 0
			s.lb.Spec.Listeners[0].PortRange = &api.PortRange{Start: 10000, End: 10127}
			require.NoError(t, s.client.Update(s.ctx, s.lb))
			lb := s.reconcile()
			lb.Spec.Listeners[0].PortRange = &api.PortRange{Start: 10000, End: 10000}
			if !equalEndpoint {
				lb.Spec.Listeners[0].PortRange = nil
				lb.Spec.Listeners[0].Port = 10000
			}
			lb.Generation++
			require.NoError(t, s.client.Update(s.ctx, &lb))
			for i, claim := range s.claims() {
				claim.Status.AllocatedCIDR = fmt.Sprintf("2001:db8::%x/128", i+1)
				require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
			}
			lb = s.reconcile()
			for _, endpoint := range lb.Status.Endpoints {
				address := endpoint.Addresses[0]
				require.True(t, address.Coordinated)
				group := networkLoadBalancerAddressGroup(&lb, &lb.Spec.Listeners[0], endpoint.RequestName, endpoint.Location, address)
				require.NotNil(t, group)
				require.Len(t, group.Listeners, 1)
				require.Equal(t, api.PortRange{Start: 10000, End: 10000}, api.PortInterval(group.Listeners[0].Port, group.Listeners[0].PortRange))
				require.Equal(t, lb.Generation, group.Generation)
			}
		})
	}
}

type coordinationIPAMClient struct {
	client.Client
	fail bool
}

func (c *coordinationIPAMClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, isClaim := obj.(*ipamv1alpha1.IPClaim); c.fail && isClaim {
		return fmt.Errorf("injected IPAM outage")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestNLBCoordinationSurvivesObservationLossAndResetsOnReplacement(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.lb.Spec.Listeners = append(s.lb.Spec.Listeners, api.NetworkLoadBalancerListener{Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, Port: 5001, AddressRefs: []string{"edge"}})
	require.NoError(t, s.client.Update(s.ctx, s.lb))
	s.reconcile()
	claim := edgeClaim(t, s)
	claim.UID = "original-claim"
	require.NoError(t, s.ipam.Update(s.ctx, &claim))
	claim.Status.Address = "2001:db8::1"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	lb := s.reconcile()
	require.True(t, edgeAddress(t, &lb).Coordinated)
	flaky := &coordinationIPAMClient{Client: s.ipam, fail: true}
	s.control.IPAM = fixedIPAMClient{flaky}
	lb.Spec.Listeners = lb.Spec.Listeners[:1]
	lb.Generation++
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	address := edgeAddress(t, &lb)
	require.True(t, address.Coordinated)
	require.Equal(t, "original-claim", address.ClaimRef.UID)
	require.Equal(t, "2001:db8::1", address.Value)
	require.False(t, apimeta.IsStatusConditionTrue(address.Conditions, api.NetworkLoadBalancerAllocated))
	flaky.fail = false
	lb = s.reconcile()
	address = edgeAddress(t, &lb)
	require.True(t, address.Coordinated)
	require.NotNil(t, networkLoadBalancerAddressGroup(&lb, &lb.Spec.Listeners[0], "edge", "iad", *address))
	require.NoError(t, s.ipam.Get(s.ctx, client.ObjectKeyFromObject(&claim), &claim))
	claim.UID = "replacement-claim"
	require.NoError(t, s.ipam.Update(s.ctx, &claim))
	lb = s.reconcile()
	require.True(t, edgeAddress(t, &lb).Retiring)
	require.NoError(t, acknowledgeRetiredAllocations(s.ctx, s.client, &lb, retirementReceipts(&lb)))
	lb = s.reconcile()
	address = edgeAddress(t, &lb)
	require.Equal(t, "replacement-claim", address.ClaimRef.UID)
	require.False(t, address.Coordinated)
	require.Nil(t, networkLoadBalancerAddressGroup(&lb, &lb.Spec.Listeners[0], "edge", "iad", *address))
}
