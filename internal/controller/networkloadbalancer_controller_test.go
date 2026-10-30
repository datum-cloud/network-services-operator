// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type fixedIPAMClient struct{ client.Client }

func (f fixedIPAMClient) ClientForProject(project string) (client.Client, error) {
	if project != testProject {
		return nil, fmt.Errorf("unexpected project %q", project)
	}
	return f.Client, nil
}

type loadBalancerAllocationScenario struct {
	t       *testing.T
	ctx     context.Context
	client  client.Client
	ipam    client.Client
	lb      *networkingv1alpha.NetworkLoadBalancer
	control *NetworkLoadBalancerReconciler
}

func newLoadBalancerAllocationScenario(t *testing.T) *loadBalancerAllocationScenario {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, locationsv1alpha1.AddToScheme(scheme))
	require.NoError(t, ipamv1alpha1.AddToScheme(scheme))

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "tenant",
		Labels: map[string]string{
			downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + testProject,
			downstreamclient.UpstreamOwnerNamespaceLabel:   testProjectNS,
		},
	}}
	network := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns.Name}}
	network.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	lb := &networkingv1alpha.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{
		Name: "public", Namespace: ns.Name, UID: types.UID("f96c4e18-cc50-45a8-aead-b368210f13ca"),
		Finalizers: []string{networkLoadBalancerFinalizer},
	}}
	lb.Spec.NetworkRef.Name = network.Name
	lb.Spec.Exposure.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressRequest{
		{Name: "regional",
			Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, Locations: &networkingv1alpha.NetworkLoadBalancerLocationSelection{
				From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"iad", "fra"},
			}},
		{Name: "edge",
			Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, Locations: &networkingv1alpha.NetworkLoadBalancerLocationSelection{
				From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"iad"},
			}},
	}
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{
		Name: "web", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443,
		AddressRefs: []string{"regional", "edge"},
	}}
	ready := locationsv1alpha1.LocationStatus{Conditions: []metav1.Condition{{Type: locationsv1alpha1.LocationConditionReady, Status: metav1.ConditionTrue, Reason: "Test"}}}
	locationIAD := &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "iad"}, Status: ready}
	locationFRA := &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "fra"}, Status: ready}
	cl := fakeclient.NewClientBuilder().WithScheme(scheme).
		WithObjects(ns, network, lb, locationIAD, locationFRA).
		WithStatusSubresource(lb, &networkingv1alpha.NetworkInterface{}).Build()
	ipam := fakeclient.NewClientBuilder().WithScheme(scheme).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testProjectNS}}).
		WithStatusSubresource(&ipamv1alpha1.IPClaim{}).Build()
	return &loadBalancerAllocationScenario{
		t: t, ctx: context.Background(), client: cl, ipam: ipam, lb: lb,
		control: &NetworkLoadBalancerReconciler{IPAM: fixedIPAMClient{ipam}, PublicIPv6Class: "public-v6", ManagedDomain: "datumproxy.net"},
	}
}

func (s *loadBalancerAllocationScenario) reconcile() networkingv1alpha.NetworkLoadBalancer {
	s.t.Helper()
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(s.t, ignoreAwaitingIPAM(err))
	var lb networkingv1alpha.NetworkLoadBalancer
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKeyFromObject(s.lb), &lb))
	return lb
}

func ignoreAwaitingIPAM(err error) error {
	if errors.Is(err, errAwaitingIPAM) {
		return nil
	}
	return err
}

func (s *loadBalancerAllocationScenario) claims() []ipamv1alpha1.IPClaim {
	s.t.Helper()
	var list ipamv1alpha1.IPClaimList
	require.NoError(s.t, s.ipam.List(s.ctx, &list, client.InNamespace(testProjectNS)))
	return list.Items
}

func TestNetworkLoadBalancerAllocationHoldsPerLocationAddresses(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	require.Len(t, s.claims(), 3)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))

	for i, claim := range s.claims() {
		require.Equal(t, "public-v6", claim.Spec.ClassName)
		require.NotNil(t, claim.Spec.PrefixLength)
		require.Equal(t, int32(128), *claim.Spec.PrefixLength)
		require.Equal(t, ipamv1alpha1.ReclaimDelete, claim.Spec.ReclaimPolicy)
		require.NotEmpty(t, claim.Spec.Scope[ipamScopeRoleLocation].Name)
		claim.Status.AllocatedCIDR = fmt.Sprintf("2001:db8::%x/128", i+1)
		require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	}
	lb = s.reconcile()
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
	require.Len(t, lb.Status.Endpoints, 3)
	require.Len(t, lb.Status.AddressRequests, 2)
	require.Equal(t, []string{"fra", "iad"}, lb.Status.AddressRequests[0].ResolvedLocations)
	for _, endpoint := range lb.Status.Endpoints {
		require.NotEmpty(t, endpoint.Location)
		require.Regexp(t, "^"+endpoint.Location+`\.[a-z]+-[a-z]+-[a-z0-9]{5}\.datumproxy\.net$`, endpoint.CanonicalHostname)
		for _, address := range endpoint.Addresses {
			require.NotEmpty(t, address.Value)
			require.True(t, apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
		}
	}
	require.Len(t, s.claims(), 3)
	require.Equal(t, lb.Status.Endpoints, s.reconcile().Status.Endpoints, "retry must reuse claims and addresses")
}

func TestNetworkLoadBalancerRejectsIPv4WithoutIPv4Class(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := *s.lb.DeepCopy()
	lb.Spec.Exposure.Addresses[1].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	condition := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
	require.NotNil(t, condition)
	require.Equal(t, "UnsupportedCapability", condition.Reason)
	require.Empty(t, s.claims())
}

func TestNetworkLoadBalancerHoldsDualStackAddresses(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.control.PublicIPv4Class = "public-v4"
	lb := *s.lb.DeepCopy()
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[1:]
	lb.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol}
	lb.Spec.Listeners[0].AddressRefs = []string{"edge"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.reconcile()

	claims := s.claims()
	require.Len(t, claims, 2)
	for _, claim := range claims {
		switch claim.Spec.IPFamily {
		case ipamv1alpha1.IPFamily(networkingv1alpha.IPv4Protocol):
			require.Equal(t, "public-v4", claim.Spec.ClassName)
			require.Equal(t, int32(32), *claim.Spec.PrefixLength)
			claim.Status.AllocatedCIDR = "192.0.2.10/32"
		case ipamv1alpha1.IPFamily(networkingv1alpha.IPv6Protocol):
			require.Equal(t, "public-v6", claim.Spec.ClassName)
			require.Equal(t, int32(128), *claim.Spec.PrefixLength)
			claim.Status.AllocatedCIDR = "2001:db8::10/128"
		default:
			t.Fatalf("unexpected claim family %q", claim.Spec.IPFamily)
		}
		require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	}

	lb = s.reconcile()
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
	require.Len(t, lb.Status.Endpoints, 1)
	values := map[networkingv1alpha.IPFamily]string{}
	for _, address := range lb.Status.Endpoints[0].Addresses {
		values[address.Family] = address.Value
	}
	require.Equal(t, map[networkingv1alpha.IPFamily]string{
		networkingv1alpha.IPv4Protocol: "192.0.2.10",
		networkingv1alpha.IPv6Protocol: "2001:db8::10",
	}, values)
}

func TestNetworkLoadBalancerRejectsAddressOfWrongFamily(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.control.PublicIPv4Class = "public-v4"
	lb := *s.lb.DeepCopy()
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[1:]
	lb.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	lb.Spec.Listeners[0].AddressRefs = []string{"edge"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.reconcile()

	claims := s.claims()
	require.Len(t, claims, 1)
	claims[0].Status.Address = "::ffff:192.0.2.10"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claims[0]))
	lb = s.reconcile()
	condition := apimeta.FindStatusCondition(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "invalid IPv4 address")
}

func TestNetworkLoadBalancerClaimNamesKeepAddressIdentityDistinct(t *testing.T) {
	lb := &networkingv1alpha.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{UID: types.UID("fixture")}}
	a := loadBalancerClaimName(lb, loadBalancerAddressRequest{
		requestName: "a-b", location: "c",
	})
	b := loadBalancerClaimName(lb, loadBalancerAddressRequest{
		requestName: "a", location: "b-c",
	})
	c := loadBalancerClaimName(lb, loadBalancerAddressRequest{
		requestName: "a-b", location: "d",
	})
	require.NotEqual(t, a, b)
	require.NotEqual(t, a, c)
}

func TestNetworkLoadBalancerWithoutIPAMDoesNotLeaveAnUnreleasableFinalizer(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := *s.lb.DeepCopy()
	lb.Finalizers = nil
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.control.IPAM = nil
	lb = s.reconcile()
	require.Empty(t, lb.Finalizers)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
}

func TestNetworkLoadBalancerExplicitLocationRetainsClaimWhenProjectionDisappears(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.reconcile()
	before := s.claims()
	require.NoError(t, s.client.Delete(s.ctx, &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "fra"}}))
	s.reconcile()
	require.Equal(t, before, s.claims())
}

func TestNetworkLoadBalancerAllLocationsAddsRegionalClaimAsLocationsAppear(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	for i := range lb.Spec.Exposure.Addresses {
		lb.Spec.Exposure.Addresses[i].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{
			From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll,
		}
	}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.reconcile()
	require.Len(t, s.claims(), 4)
	require.NoError(t, s.client.Create(s.ctx, &locationsv1alpha1.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "sea"}, Status: locationsv1alpha1.LocationStatus{Conditions: []metav1.Condition{{Type: locationsv1alpha1.LocationConditionReady, Status: metav1.ConditionTrue, Reason: "Test"}}},
	}))
	lb = s.reconcile()
	require.Len(t, s.claims(), 6)
	require.Len(t, lb.Status.Endpoints, 6)
}

func TestNetworkLoadBalancerAllLocationsSkipsUnreadyLocations(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	lb.Spec.Exposure.Addresses[0].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll}
	lb.Spec.Exposure.Addresses[1].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	require.NoError(t, s.client.Create(s.ctx, &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "unready"},
		Status: locationsv1alpha1.LocationStatus{Conditions: []metav1.Condition{{Type: locationsv1alpha1.LocationConditionReady, Status: metav1.ConditionFalse, Reason: "Test"}}}}))
	lb = s.reconcile()
	for _, endpoint := range lb.Status.Endpoints {
		require.NotEqual(t, "unready", endpoint.Location)
	}
}

func TestNetworkLoadBalancerClaimReleaseSkipsReplacementUID(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.reconcile()
	claim := edgeClaim(t, s)
	ref := &networkingv1alpha.NetworkLoadBalancerIPClaimRef{Project: testProject, Namespace: claim.Namespace, Name: claim.Name, UID: "old-claim"}
	require.NoError(t, s.control.deleteAddressClaim(s.ctx, ref, string(s.lb.UID)))
	var existing ipamv1alpha1.IPClaim
	require.NoError(t, s.ipam.Get(s.ctx, client.ObjectKeyFromObject(&claim), &existing))
	ref.UID = string(existing.UID)
	require.NoError(t, s.control.deleteAddressClaim(s.ctx, ref, "foreign-lb"))
	require.NoError(t, s.ipam.Get(s.ctx, client.ObjectKeyFromObject(&claim), &existing))
}

func TestNetworkLoadBalancerBackendPlacementResolvesUniquePortParent(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	lb.Spec.Exposure.Addresses[1].Locations = nil
	require.NoError(t, s.client.Update(s.ctx, &lb))
	service := &networkingv1alpha.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: lb.Namespace}}
	service.Spec.NetworkInterfaces.Selector = metav1.LabelSelector{MatchLabels: map[string]string{"app": "backend"}}
	service.Spec.Ports = []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}}
	require.NoError(t, s.client.Create(s.ctx, service))
	member := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{
		Name: "member", Namespace: lb.Namespace,
		Labels: map[string]string{"app": "backend", networkingv1alpha.NetworkInterfaceLocationLabel: "iad"},
	}}
	member.Spec.Network.Name = lb.Spec.NetworkRef.Name
	member.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: "member"}
	member.Status.Phase = networkingv1alpha.NetworkInterfacePhaseBound
	require.NoError(t, s.client.Create(s.ctx, member))
	require.NoError(t, s.client.Status().Update(s.ctx, member))
	route := &networkingv1alpha.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: lb.Namespace}}
	route.Spec.ParentRefs = []networkingv1alpha.NetworkLoadBalancerRouteParentRef{{
		Name: lb.Name, Namespace: lb.Namespace, Port: ptr.To[int32](443),
	}}
	route.Spec.Backends = []networkingv1alpha.NetworkLoadBalancerRouteBackend{{
		NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: service.Name, Port: "http"},
	}}
	require.NoError(t, s.client.Create(s.ctx, route))
	lb = s.reconcile()
	require.Equal(t, []string{"iad"}, lb.Status.AddressRequests[1].ResolvedLocations)
	require.Len(t, s.claims(), 3)

	lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{
		Name: "udp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 443,
		AddressRefs: []string{"edge"},
	})
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Equal(t, []string{"iad"}, lb.Status.AddressRequests[1].ResolvedLocations,
		"ambiguous port parent retains the prior allocation while resolution is pending")
}

func TestNetworkLoadBalancerTooManySelectedEndpointsDoesNotAllocate(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	lb.Spec.Exposure.Addresses = nil
	for i := range 16 {
		lb.Spec.Exposure.Addresses = append(lb.Spec.Exposure.Addresses, networkingv1alpha.NetworkLoadBalancerAddressRequest{
			Name: fmt.Sprintf("request-%02d", i), Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
			Locations: &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll},
		})
	}
	for i := range 63 {
		require.NoError(t, s.client.Create(s.ctx, &locationsv1alpha1.Location{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("location-%02d", i)},
			Status:     locationsv1alpha1.LocationStatus{Conditions: []metav1.Condition{{Type: locationsv1alpha1.LocationConditionReady, Status: metav1.ConditionTrue, Reason: "Test"}}},
		}))
	}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	before := s.claims()
	lb = s.reconcile()
	require.Equal(t, before, s.claims(), "overflow must not create any additional claims")
	condition := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
	require.NotNil(t, condition)
	require.Equal(t, "TooManyEndpoints", condition.Reason)
	require.Contains(t, condition.Message, "1024")
	require.LessOrEqual(t, len(lb.Status.Endpoints), 1024)
}

func TestNetworkLoadBalancerEndpointLimitIncludesProgrammedWithdrawal(t *testing.T) {
	selected := make([]loadBalancerAddressRequest, 1024)
	for i := range selected {
		selected[i] = loadBalancerAddressRequest{requestName: "a", location: fmt.Sprintf("location-%04d", i)}
	}
	prior := []networkingv1alpha.NetworkLoadBalancerEndpointStatus{{
		RequestName: "retired", Location: "old",
		Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{{
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue}},
		}},
	}}
	require.Equal(t, 1025, countAddressEndpoints(selected, prior))
}

func TestNetworkLoadBalancerBackendLocationsIncludeDisjointBackends(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[:1]
	lb.Spec.Exposure.Addresses[0].Locations = nil
	lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	for _, entry := range []struct{ name, location string }{{"east", "iad"}, {"west", "fra"}} {
		service := &networkingv1alpha.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: entry.name, Namespace: lb.Namespace}}
		service.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": entry.name}
		require.NoError(t, s.client.Create(s.ctx, service))
		iface := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{
			Name: entry.name, Namespace: lb.Namespace,
			Labels: map[string]string{"app": entry.name, networkingv1alpha.NetworkInterfaceLocationLabel: entry.location},
		}}
		iface.Spec.Network.Name = lb.Spec.NetworkRef.Name
		iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseBound
		require.NoError(t, s.client.Create(s.ctx, iface))
	}
	route := &networkingv1alpha.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: "backend-route", Namespace: lb.Namespace}}
	route.Spec.ParentRefs = []networkingv1alpha.NetworkLoadBalancerRouteParentRef{{Name: lb.Name, Listener: "web"}}
	for _, name := range []string{"east", "west"} {
		route.Spec.Backends = append(route.Spec.Backends, networkingv1alpha.NetworkLoadBalancerRouteBackend{
			NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: name, Port: "web"},
		})
	}
	require.NoError(t, s.client.Create(s.ctx, route))
	locations, err := backendLocationsForAddress(s.ctx, s.client, &lb, "regional")
	require.NoError(t, err)
	require.Equal(t, []string{"fra", "iad"}, locations)

}

func TestNetworkLoadBalancerRetainsAnnouncedAddressUntilWithdrawn(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	setEdgeAnnouncement(&lb, metav1.ConditionTrue)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[:1]
	lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, s.claims(), 3, "announced IP must remain held")
	require.Len(t, lb.Status.Endpoints, 3, "withdrawal must see the old address")
	require.Len(t, lb.Status.AddressRequests, 1)
	require.Equal(t, []string{"fra", "iad"}, lb.Status.AddressRequests[0].ResolvedLocations)

	setEdgeAnnouncement(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, lb.Status.Endpoints, 2)
	require.Len(t, s.claims(), 2)
}

func TestNetworkLoadBalancerRetainsProgrammedAddressUntilWithdrawn(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	setEdgeProgramming(&lb, metav1.ConditionTrue)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[:1]
	lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, s.claims(), 3, "programmed IP must remain held before announcement")
	require.Len(t, lb.Status.Endpoints, 3, "withdrawal must see the programmed address")
	require.False(t, edgeAddress(t, &lb).Announced)
	require.True(t, apimeta.IsStatusConditionTrue(edgeAddress(t, &lb).Conditions,
		networkingv1alpha.NetworkLoadBalancerProgrammed))

	setEdgeProgramming(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, lb.Status.Endpoints, 2)
	require.Len(t, s.claims(), 2)
}

func TestNetworkLoadBalancerOrphanCleanupRetainsProgrammedClaim(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	claim := edgeClaim(t, s)
	setEdgeProgramming(&lb, metav1.ConditionTrue)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	wanted := map[string]bool{}
	require.NoError(t, s.control.releaseOrphanedClaims(s.ctx, s.ipam, testProjectNS, &lb, wanted))
	require.NoError(t, s.ipam.Get(s.ctx, client.ObjectKeyFromObject(&claim), &ipamv1alpha1.IPClaim{}))
	setEdgeProgramming(&lb, metav1.ConditionFalse)
	require.NoError(t, s.control.releaseOrphanedClaims(s.ctx, s.ipam, testProjectNS, &lb, wanted))
	var remaining ipamv1alpha1.IPClaimList
	require.NoError(t, s.ipam.List(s.ctx, &remaining, client.InNamespace(testProjectNS)))
	require.Empty(t, remaining.Items)
}

func TestNetworkLoadBalancerRecreatedClaimDoesNotInheritPublication(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.reconcile()
	claim := edgeClaim(t, s)
	claim.UID = types.UID("original-claim")
	require.NoError(t, s.ipam.Update(s.ctx, &claim))
	claim.Status.Address = "2001:db8::1"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	lb := s.reconcile()
	old := edgeAddress(t, &lb)
	require.Equal(t, "2001:db8::1", old.Value)
	require.Equal(t, "original-claim", old.ClaimRef.UID)
	old.Announced = true
	old.DNSPublished = true
	apimeta.SetStatusCondition(&old.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue,
		Reason: "Observed", Message: "programmed",
	})
	apimeta.SetStatusCondition(&lb.Status.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue,
		Reason: "Observed", Message: "programmed",
	})
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))

	require.NoError(t, s.ipam.Delete(s.ctx, &claim))
	lb = s.reconcile()
	require.Equal(t, "AddressReplacementPendingWithdrawal",
		apimeta.FindStatusCondition(edgeAddress(t, &lb).Conditions, networkingv1alpha.NetworkLoadBalancerAllocated).Reason)
	replacement := edgeClaim(t, s)
	replacement.UID = types.UID("replacement-claim")
	require.NoError(t, s.ipam.Update(s.ctx, &replacement))
	replacement.Status.Address = "2001:db8::2"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &replacement))
	lb = s.reconcile()
	old = edgeAddress(t, &lb)
	require.Equal(t, "2001:db8::1", old.Value, "old address must remain visible for withdrawal")
	require.Equal(t, "original-claim", old.ClaimRef.UID)
	require.True(t, old.Announced)
	require.True(t, old.DNSPublished)
	require.Equal(t, "AddressReplacementPendingWithdrawal",
		apimeta.FindStatusCondition(old.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated).Reason)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))

	old.Announced = false
	old.DNSPublished = false
	apimeta.SetStatusCondition(&old.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: metav1.ConditionFalse,
		Reason: "Withdrawn", Message: "withdrawn",
	})
	apimeta.SetStatusCondition(&old.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: metav1.ConditionFalse,
		Reason: "Withdrawn", Message: "withdrawn",
	})
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb = s.reconcile()
	current := edgeAddress(t, &lb)
	require.Equal(t, "2001:db8::2", current.Value)
	require.Equal(t, "replacement-claim", current.ClaimRef.UID)
	require.False(t, current.Announced)
	require.False(t, current.DNSPublished)
	require.Nil(t, apimeta.FindStatusCondition(current.Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced))
	require.Nil(t, apimeta.FindStatusCondition(current.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
}

func TestNetworkLoadBalancerReassignedAddressResetsServingConditions(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.reconcile()
	claim := edgeClaim(t, s)
	claim.Status.Address = "2001:db8::1"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	lb := s.reconcile()
	old := edgeAddress(t, &lb)
	apimeta.SetStatusCondition(&old.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue,
		Reason: "Observed", Message: "programmed",
	})
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	claim.Status.Address = "2001:db8::2"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	lb = s.reconcile()
	require.Equal(t, "2001:db8::1", edgeAddress(t, &lb).Value)
	require.True(t, apimeta.IsStatusConditionTrue(edgeAddress(t, &lb).Conditions,
		networkingv1alpha.NetworkLoadBalancerProgrammed))
	setEdgeProgramming(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb = s.reconcile()
	current := edgeAddress(t, &lb)
	require.Equal(t, "2001:db8::2", current.Value)
	require.Nil(t, apimeta.FindStatusCondition(current.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
}

func TestNetworkLoadBalancerRecreatedClaimWithSameAddressResetsServingConditions(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.reconcile()
	claim := edgeClaim(t, s)
	claim.UID = types.UID("first-claim")
	require.NoError(t, s.ipam.Update(s.ctx, &claim))
	claim.Status.Address = "2001:db8::1"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	lb := s.reconcile()
	old := edgeAddress(t, &lb)
	apimeta.SetStatusCondition(&old.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: metav1.ConditionTrue,
		Reason: "Observed", Message: "programmed",
	})
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	require.NoError(t, s.ipam.Delete(s.ctx, &claim))
	s.reconcile()
	replacement := edgeClaim(t, s)
	replacement.UID = types.UID("second-claim")
	require.NoError(t, s.ipam.Update(s.ctx, &replacement))
	replacement.Status.Address = "2001:db8::1"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &replacement))
	lb = s.reconcile()
	require.Equal(t, "first-claim", edgeAddress(t, &lb).ClaimRef.UID)
	setEdgeProgramming(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb = s.reconcile()
	current := edgeAddress(t, &lb)
	require.Equal(t, "second-claim", current.ClaimRef.UID)
	require.Nil(t, apimeta.FindStatusCondition(current.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
}

func edgeClaim(t *testing.T, s *loadBalancerAllocationScenario) ipamv1alpha1.IPClaim {
	t.Helper()
	for _, claim := range s.claims() {
		if claim.Spec.Scope[ipamScopeRoleLocation].Name == "iad" &&
			claim.Name == loadBalancerClaimName(s.lb, loadBalancerAddressRequest{
				requestName: "edge", location: "iad", family: networkingv1alpha.IPv6Protocol,
			}) {
			return claim
		}
	}
	t.Fatal("edge IPClaim in iad not found")
	return ipamv1alpha1.IPClaim{}
}

func edgeAddress(t *testing.T, lb *networkingv1alpha.NetworkLoadBalancer) *networkingv1alpha.NetworkLoadBalancerAddressStatus {
	t.Helper()
	for i := range lb.Status.Endpoints {
		if lb.Status.Endpoints[i].RequestName == "edge" {
			return &lb.Status.Endpoints[i].Addresses[0]
		}
	}
	t.Fatal("edge address not found")
	return nil
}

func TestNetworkLoadBalancerResolvedLocationsExcludeWithdrawingLocation(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	for i := range lb.Status.Endpoints {
		endpoint := &lb.Status.Endpoints[i]
		if endpoint.RequestName == "regional" && endpoint.Location == "fra" {
			for j := range endpoint.Addresses {
				endpoint.Addresses[j].Announced = true
			}
		}
	}
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb.Spec.Exposure.Addresses[0].Locations.Names = []string{"iad"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, lb.Status.Endpoints, 3, "the removed location remains until withdrawal")
	require.Equal(t, []string{"iad"}, lb.Status.AddressRequests[0].ResolvedLocations)
	require.Len(t, s.claims(), 3)

}

func TestNetworkLoadBalancerDeletionWaitsForWithdrawal(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	setEdgeAnnouncement(&lb, metav1.ConditionTrue)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	require.NoError(t, s.client.Delete(s.ctx, &lb))
	lb = s.reconcile()
	require.NotNil(t, lb.DeletionTimestamp)
	require.Len(t, s.claims(), 3)

	setEdgeAnnouncement(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Empty(t, s.claims())
}

func TestNetworkLoadBalancerDeletionWaitsForProgrammingWithdrawal(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	setEdgeProgramming(&lb, metav1.ConditionTrue)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	require.NoError(t, s.client.Delete(s.ctx, &lb))
	lb = s.reconcile()
	require.NotNil(t, lb.DeletionTimestamp)
	require.Len(t, s.claims(), 3)

	setEdgeProgramming(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Empty(t, s.claims())
}

func TestNetworkLoadBalancerRejectsDriftedIPClaimSpec(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ipamv1alpha1.IPClaim)
	}{
		{name: "family", mutate: func(claim *ipamv1alpha1.IPClaim) {
			claim.Spec.IPFamily = ipamv1alpha1.IPFamily(networkingv1alpha.IPv4Protocol)
		}},
		{name: "reclaim policy", mutate: func(claim *ipamv1alpha1.IPClaim) {
			claim.Spec.ReclaimPolicy = "Retain"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoadBalancerAllocationScenario(t)
			s.reconcile()
			claim := edgeClaim(t, s)
			tc.mutate(&claim)
			require.NoError(t, s.ipam.Update(s.ctx, &claim))
			lb := s.reconcile()
			condition := apimeta.FindStatusCondition(edgeAddress(t, &lb).Conditions,
				networkingv1alpha.NetworkLoadBalancerAllocated)
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Contains(t, condition.Message, "does not match")
			require.Len(t, s.claims(), 3, "drifted claim must not be replaced or released")
		})
	}
}

func setEdgeProgramming(lb *networkingv1alpha.NetworkLoadBalancer, status metav1.ConditionStatus) {
	for i := range lb.Status.Endpoints {
		if lb.Status.Endpoints[i].RequestName != "edge" {
			continue
		}
		for j := range lb.Status.Endpoints[i].Addresses {
			apimeta.SetStatusCondition(&lb.Status.Endpoints[i].Addresses[j].Conditions, metav1.Condition{
				Type: networkingv1alpha.NetworkLoadBalancerProgrammed, Status: status,
				Reason: "Observed", Message: "data plane programming status", ObservedGeneration: lb.Generation,
			})
		}
	}
}

func setEdgeAnnouncement(lb *networkingv1alpha.NetworkLoadBalancer, status metav1.ConditionStatus) {
	for i := range lb.Status.Endpoints {
		if lb.Status.Endpoints[i].RequestName != "edge" {
			continue
		}
		for j := range lb.Status.Endpoints[i].Addresses {
			address := &lb.Status.Endpoints[i].Addresses[j]
			address.Announced = status == metav1.ConditionTrue
			apimeta.SetStatusCondition(&address.Conditions, metav1.Condition{
				Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: status,
				Reason: "Observed", Message: "BGP announcement status", ObservedGeneration: lb.Generation,
			})
		}
	}
}

func TestNetworkLoadBalancerDependencyEventsEnqueueAffectedLoadBalancers(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	key := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(s.lb)}

	other := &networkingv1alpha.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: s.lb.Namespace}}
	other.Spec.NetworkRef.Name = "elsewhere"
	require.NoError(t, s.client.Create(s.ctx, other))

	route := &networkingv1alpha.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: s.lb.Namespace}}
	route.Spec.ParentRefs = []networkingv1alpha.NetworkLoadBalancerRouteParentRef{
		{Name: s.lb.Name, Listener: "web"}, {Name: s.lb.Name, Port: ptr.To[int32](443)},
	}
	route.Spec.Backends = []networkingv1alpha.NetworkLoadBalancerRouteBackend{{
		NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: "backend", Port: "http"},
	}}
	require.NoError(t, s.client.Create(s.ctx, route))
	require.Equal(t, []reconcile.Request{key}, loadBalancersForRouteEvent(s.ctx, s.client, route))

	service := &networkingv1alpha.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: s.lb.Namespace}}
	require.Equal(t, []reconcile.Request{key}, loadBalancersForNetworkServiceEvent(s.ctx, s.client, service))
	unrelated := &networkingv1alpha.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: s.lb.Namespace}}
	require.Empty(t, loadBalancersForNetworkServiceEvent(s.ctx, s.client, unrelated))

	member := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{Name: "member", Namespace: s.lb.Namespace}}
	member.Spec.Network.Name = s.lb.Spec.NetworkRef.Name
	require.Equal(t, []reconcile.Request{key}, loadBalancersForInterfaceEvent(s.ctx, s.client, member))

	network := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Name: s.lb.Spec.NetworkRef.Name, Namespace: s.lb.Namespace}}
	require.Equal(t, []reconcile.Request{key}, loadBalancersForNetworkEvent(s.ctx, s.client, network))

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: s.lb.Namespace}}
	require.ElementsMatch(t, []reconcile.Request{key, {NamespacedName: client.ObjectKeyFromObject(other)}},
		loadBalancersForNamespaceEvent(s.ctx, s.client, namespace))

	location := &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "iad"}}
	require.Empty(t, loadBalancersForLocationEvent(s.ctx, s.client, location))
	var lb networkingv1alpha.NetworkLoadBalancer
	require.NoError(t, s.client.Get(s.ctx, key.NamespacedName, &lb))
	lb.Spec.Exposure.Addresses[0].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{
		From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll,
	}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	require.Equal(t, []reconcile.Request{key}, loadBalancersForLocationEvent(s.ctx, s.client, location))
}

func TestNetworkLoadBalancerReliesOnEventsInsteadOfPeriodicResync(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	result, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.ErrorIs(t, err, errAwaitingIPAM, "a claim IPAM has not answered retries with backoff")
	require.Zero(t, result.RequeueAfter)

	for i, claim := range s.claims() {
		claim.Status.AllocatedCIDR = fmt.Sprintf("2001:db8::%x/128", i+1)
		require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	}
	for range 2 {
		result, err = s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
		require.NoError(t, err)
		require.Equal(t, ctrl.Result{}, result, "an allocated load balancer waits for the next event")
	}
}
