// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func sharedAddressLoadBalancer() *networkingv1alpha.NetworkLoadBalancer {
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{
		{Name: "tcp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"regional"}},
		{Name: "udp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 443, AddressRefs: []string{"regional"}},
		{Name: "tcp-alt", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 8443, AddressRefs: []string{"regional"}},
	}
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", parent.Generation)}
	return parent
}

func sharedAddressNetworkService() *networkingv1alpha.NetworkService {
	service := testRouteNetworkService()
	service.Spec.Ports = []networkingv1alpha.NetworkServicePort{
		{Name: "http", Port: 8080},
		{Name: "media", Port: 8080, Protocol: networkingv1alpha.NetworkServiceProtocolUDP},
		{Name: "alt", Port: 9443},
		{Name: "health", Port: 8081},
	}
	return service
}

func sharedAddressRoute(name string, created int64, listener, port string) *networkingv1alpha.NetworkLoadBalancerRoute {
	route := testLoadBalancerRoute(name, time.Unix(created, 0))
	route.Spec.ParentRefs[0].Listener = listener
	route.Spec.Backends[0].NetworkService.Port = port
	if listener == "udp" {
		route.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "health"}
	}
	return route
}

func sharedAddressMember() *networkingv1alpha.NetworkInterface {
	member := testRouteMember("member", true, "fd00::1/128")
	member.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: "member"}
	return member
}

func TestSharedAddressListenersPublishIntentsForOneVIP(t *testing.T) {
	for _, tt := range []struct {
		name   string
		second *networkingv1alpha.NetworkLoadBalancerRoute
	}{
		{name: "TCP and UDP on one port", second: sharedAddressRoute("media", 20, "udp", "media")},
		{name: "two TCP ports", second: sharedAddressRoute("alt", 20, "tcp-alt", "alt")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			first := sharedAddressRoute("https", 10, "tcp", "http")
			cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
				WithObjects(first, tt.second, sharedAddressLoadBalancer(), readyIntentNetwork(),
					sharedAddressNetworkService(), sharedAddressMember()).Build()

			var intents []networkingv1alpha.NetworkLoadBalancerCellIntent
			for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{first, tt.second} {
				desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
				require.NoError(t, err)
				require.Len(t, desired, 1, route.Name)
				require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired[0].Spec.State, route.Name)
				require.Equal(t, "2001:db8::1", desired[0].Spec.VIP)
				require.Equal(t, "regional", desired[0].Spec.Source.AddressRequest)
				intents = append(intents, desired...)
			}
			require.NotEqual(t, intents[0].Name, intents[1].Name)
			require.NotEqual(t,
				[2]any{intents[0].Spec.Protocol, intents[0].Spec.ListenerPort},
				[2]any{intents[1].Spec.Protocol, intents[1].Spec.ListenerPort})
		})
	}
}

func TestSharedAddressRouteWithdrawalKeepsSiblingServing(t *testing.T) {
	ctx := context.Background()
	https := sharedAddressRoute("https", 10, "tcp", "http")
	media := sharedAddressRoute("media", 20, "udp", "media")
	parent := sharedAddressLoadBalancer()
	parent.Spec.Exposure.Addresses[0].Locations = nil
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: types.UID("project-ns")}}
	project := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(namespace, https, media, parent, readyIntentNetwork(), sharedAddressNetworkService(), sharedAddressMember()).
		WithStatusSubresource(https, media, parent).Build()
	hub := fake.NewClientBuilder().WithScheme(project.Scheme()).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}).Build()
	reconciler := &NetworkLoadBalancerCellIntentReconciler{HubCluster: &fakeCluster{cl: hub}}
	for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{https, media} {
		_, err := reconciler.reconcileIntent(ctx, "project", project, client.ObjectKeyFromObject(route))
		require.NoError(t, err)
	}
	var published networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &published))
	require.Len(t, published.Items, 2)

	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(media), media))
	require.NoError(t, project.Delete(ctx, media))
	for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{media, https} {
		_, err := reconciler.reconcileIntent(ctx, "project", project, client.ObjectKeyFromObject(route))
		require.NoError(t, err)
	}
	require.NoError(t, hub.List(ctx, &published))
	require.Len(t, published.Items, 2)
	for i := range published.Items {
		intent := &published.Items[i]
		require.Equal(t, "2001:db8::1", intent.Spec.VIP)
		switch intent.Spec.Source.ListenerName {
		case "tcp":
			require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, intent.Spec.State)
			require.NotEmpty(t, intent.Spec.Backends)
			reportIntent(intent, metav1.ConditionTrue, metav1.ConditionTrue)
		case "udp":
			require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
			require.Empty(t, intent.Spec.Backends)
		default:
			t.Fatalf("unexpected listener %q", intent.Spec.Source.ListenerName)
		}
	}

	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	locations, err := backendLocationsForAddress(ctx, project, parent, "regional")
	require.NoError(t, err)
	require.Equal(t, []string{"dfw"}, locations, "a withdrawn listener must not release the shared address")

	applyNetworkLoadBalancerDataPlane(parent, published.Items, nil)
	address := parent.Status.Endpoints[0].Addresses[0]
	require.True(t, apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
	require.True(t, apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced))
	require.True(t, address.Announced)
}

func TestSharedAddressPlacementIgnoresListenerWithoutRoute(t *testing.T) {
	ctx := context.Background()
	parent := sharedAddressLoadBalancer()
	parent.Spec.Exposure.Addresses[0].Locations = nil
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(sharedAddressRoute("https", 10, "tcp", "http"), parent,
			sharedAddressNetworkService(), sharedAddressMember()).Build()
	locations, err := backendLocationsForAddress(ctx, cl, parent, "regional")
	require.NoError(t, err)
	require.Equal(t, []string{"dfw"}, locations)

	parent.Spec.Listeners = parent.Spec.Listeners[1:]
	locations, err = backendLocationsForAddress(ctx, cl, parent, "regional")
	require.NoError(t, err)
	require.Empty(t, locations)
}

func TestSharedAddressProgrammedRequiresEveryServingListener(t *testing.T) {
	lb, tcp := dataPlaneFixture()
	tcp.Spec.Source.ListenerName = "tcp"
	udp := *tcp.DeepCopy()
	udp.Name = "nlb-2"
	udp.Spec.Source.ListenerName = "udp"
	reportIntent(&tcp, metav1.ConditionTrue, metav1.ConditionTrue)
	reportIntent(&udp, metav1.ConditionFalse, metav1.ConditionFalse)
	udp.Status.Conditions[0].Message = "no backend passed verification"

	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{tcp, udp}, nil)
	programmed := apimeta.FindStatusCondition(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Contains(t, programmed.Message, `Listener "udp"`)
	route, ok := listenerAddressesProgrammed(lb, &networkingv1alpha.NetworkLoadBalancerListener{AddressRefs: []string{"public"}})
	require.True(t, ok)
	require.Equal(t, metav1.ConditionFalse, route.Status)

	reportIntent(&udp, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{tcp, udp}, nil)
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
}

func TestSharedAddressKeepsBackendSocketOwnership(t *testing.T) {
	for _, tt := range []struct {
		name     string
		families []networkingv1alpha.IPFamily
	}{
		{name: "IPv6", families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}},
		{name: "IPv4", families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}},
		{name: "dual stack", families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			parent := sharedAddressLoadBalancer()
			parent.Spec.Exposure.Addresses[0].Families = tt.families
			var addresses []networkingv1alpha.NetworkLoadBalancerAddressStatus
			for _, family := range tt.families {
				value := "2001:db8::1"
				if family == networkingv1alpha.IPv4Protocol {
					value = "192.0.2.10"
				}
				address := allocatedCellIntentAddress(value, parent.Generation)
				address.Family = family
				addresses = append(addresses, address)
			}
			parent.Status.Endpoints[0].Addresses = addresses
			older := sharedAddressRoute("https", 10, "tcp", "http")
			newer := sharedAddressRoute("alt", 20, "tcp-alt", "http")
			media := sharedAddressRoute("media", 30, "udp", "media")
			cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
				WithObjects(older, newer, media, parent, readyIntentNetwork(), sharedAddressNetworkService(), sharedAddressMember()).
				WithStatusSubresource(older, newer, media).Build()

			r := &NetworkLoadBalancerRouteReconciler{}
			for _, route := range []*networkingv1alpha.NetworkLoadBalancerRoute{older, newer, media} {
				require.NoError(t, r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)))
			}
			for route, reason := range map[string]string{
				"https": nlbRouteReasonResolved,
				"alt":   nlbRouteReasonBackendClaimConflict,
				"media": nlbRouteReasonResolved,
			} {
				var got networkingv1alpha.NetworkLoadBalancerRoute
				require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: "test", Name: route}, &got))
				require.Equal(t, reason,
					apimeta.FindStatusCondition(got.Status.Parents[0].Conditions, networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs).Reason, route)
			}

			for route, count := range map[*networkingv1alpha.NetworkLoadBalancerRoute]int{older: len(tt.families), newer: 0, media: len(tt.families)} {
				desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
				require.NoError(t, err)
				require.Len(t, desired, count, route.Name)
				for _, intent := range desired {
					require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, intent.Spec.State, route.Name)
				}
			}
		})
	}
}
