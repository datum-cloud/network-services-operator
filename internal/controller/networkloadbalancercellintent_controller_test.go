// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func allocatedCellIntentAddress(value string, generation int64) networkingv1alpha.NetworkLoadBalancerAddressStatus {
	return networkingv1alpha.NetworkLoadBalancerAddressStatus{
		Family: networkingv1alpha.IPv6Protocol, Value: value,
		ClaimRef: &networkingv1alpha.NetworkLoadBalancerIPClaimRef{Project: "project", Namespace: "test", Name: "vip-claim"},
		Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkLoadBalancerAllocated,
			Status: metav1.ConditionTrue, Reason: "Allocated", ObservedGeneration: generation,
			LastTransitionTime: metav1.Now()}},
	}
}

func allocatedCellIntentEndpoint(value string, generation int64) networkingv1alpha.NetworkLoadBalancerEndpointStatus {
	return networkingv1alpha.NetworkLoadBalancerEndpointStatus{
		RequestName: "regional", Location: "dfw",
		Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{allocatedCellIntentAddress(value, generation)},
	}
}

func TestCellIntentPublishesAndWithdrawsWithoutDeletingHold(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	route.UID = types.UID("route-1")
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", parent.Generation)}
	network := readyIntentNetwork()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: types.UID("project-ns")}}
	project := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(namespace, route, parent, network, testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(route, parent).Build()
	hub := fake.NewClientBuilder().WithScheme(project.Scheme()).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}).Build()
	reconciler := &NetworkLoadBalancerCellIntentReconciler{HubCluster: &fakeCluster{cl: hub}}
	key := client.ObjectKeyFromObject(route)

	_, err := reconciler.reconcileIntent(ctx, "project", project, key)
	require.NoError(t, err)
	var first networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &first))
	require.Len(t, first.Items, 1)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, first.Items[0].Spec.State)
	require.Equal(t, "2001:db8::1", first.Items[0].Spec.VIP)
	require.Equal(t, networkingv1alpha.IPv6Protocol, first.Items[0].Spec.VIPFamily)
	require.Equal(t, networkingv1alpha.IPv6Protocol, first.Items[0].Spec.Backends[0].Family)
	require.Equal(t, "project", first.Items[0].Spec.Source.ProjectCluster)
	require.Equal(t, "route-1", first.Items[0].Spec.Source.RouteUID)
	require.Equal(t, "cell-test", first.Items[0].Spec.Backends[0].SourceNamespace)
	require.Equal(t, "source-member", first.Items[0].Spec.Backends[0].SourceUID)
	require.Equal(t, int32(8080), first.Items[0].Spec.Backends[0].Probe.Port)

	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::2", parent.Generation)}
	require.NoError(t, project.Status().Update(ctx, parent))
	_, err = reconciler.reconcileIntent(ctx, "project", project, key)
	require.NoError(t, err)
	var changed networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &changed))
	require.Len(t, changed.Items, 2)
	for _, intent := range changed.Items {
		if intent.Spec.VIP == "2001:db8::1" {
			require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
			require.Empty(t, intent.Spec.Backends)
		} else {
			require.Equal(t, "2001:db8::2", intent.Spec.VIP)
			require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, intent.Spec.State)
		}
	}

	require.NoError(t, project.Get(ctx, key, route))
	require.NoError(t, project.Delete(ctx, route))
	_, err = reconciler.reconcileIntent(ctx, "project", project, key)
	require.NoError(t, err)
	var withdrawn networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &withdrawn))
	require.Len(t, withdrawn.Items, 2)
	for _, intent := range withdrawn.Items {
		require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
		require.Empty(t, intent.Spec.Backends)
	}
}

func TestCellIntentRequiresFreshPerLocationAllocation(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{
		allocatedCellIntentEndpoint("2001:db8::1", 4),
	}
	parent.Status.Endpoints[0].Location = ""
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(),
			testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)

	parent.Status.Endpoints[0].Addresses[0].Conditions[0].ObservedGeneration = 5
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)

	parent.Status.Endpoints[0].Location = "dfw"
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Equal(t, "dfw", desired[0].Spec.Location)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired[0].Spec.State)

	duplicate := allocatedCellIntentEndpoint("2001:db8::1", 5)
	duplicate.Location = "ord"
	parent.Status.Endpoints = append(parent.Status.Endpoints, duplicate)
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestCellIntentRejectsUnsupportedVIPFamily(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	allocation := allocatedCellIntentAddress("192.0.2.10", parent.Generation)
	allocation.Family = networkingv1alpha.IPv4Protocol
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{{RequestName: "regional", Location: "dfw", Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{allocation}}}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(), testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).Build()

	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestCellIntentResolvesPortParentAndEquivalentNamespace(t *testing.T) {
	ctx := context.Background()
	port := int32(443)
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	older.Spec.ParentRefs[0] = networkingv1alpha.NetworkLoadBalancerRouteParentRef{
		Name: "edge", Port: &port, Namespace: "test",
	}
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", 5)}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, parent, readyIntentNetwork(), testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).Build()

	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", older)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Equal(t, "tcp", desired[0].Spec.Source.ListenerName)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired[0].Spec.State)

	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	require.NoError(t, cl.Create(ctx, newer))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", newer)
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestCellIntentWithholdsOversizedGalacticBackendSet(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", 5)}
	objects := make([]client.Object, 0, 69)
	objects = append(objects, route, parent, readyIntentNetwork(), testRouteNetworkService())
	for i := range 65 {
		objects = append(objects, testRouteMember(fmt.Sprintf("member-%d", i), true,
			fmt.Sprintf("fd00::%x/128", i+1)))
	}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(objects...).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, desired[0].Spec.State)
	require.Empty(t, desired[0].Spec.Backends)
}

func readyIntentNetwork() *networkingv1alpha.Network {
	return &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "default", Generation: 1},
		Status: networkingv1alpha.NetworkStatus{Conditions: []metav1.Condition{{
			Type: networkingv1alpha.NetworkReady, Status: metav1.ConditionTrue,
			Reason: "Ready", ObservedGeneration: 1, LastTransitionTime: metav1.Now(),
		}}},
	}
}

func TestCellIntentFailsClosedOnMissingIdentityAndOlderRoute(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("newer", time.Unix(20, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", 5)}
	member := testRouteMember("member", true, "fd00::1/128")
	delete(member.Labels, networkingv1alpha.NetworkInterfaceSourceUIDLabel)
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(), testRouteNetworkService(), member).Build()

	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, desired[0].Spec.State)
	require.Empty(t, desired[0].Spec.Backends)

	member.Labels[networkingv1alpha.NetworkInterfaceSourceUIDLabel] = "source-member"
	require.NoError(t, cl.Update(ctx, member))
	require.NoError(t, cl.Create(ctx, testLoadBalancerRoute("older", time.Unix(10, 0))))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestCellIntentDoesNotValidateConflictingBackendSocket(t *testing.T) {
	ctx := context.Background()
	older := testLoadBalancerRoute("older", time.Unix(10, 0))
	newer := testLoadBalancerRoute("newer", time.Unix(20, 0))
	newer.Spec.ParentRefs[0].Name = "edge-2"
	firstParent := testRouteLoadBalancer()
	firstParent.UID = types.UID("first-parent")
	firstParent.Generation = 5
	firstParent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", 5)}
	secondParent := firstParent.DeepCopy()
	secondParent.Name = "edge-2"
	secondParent.UID = types.UID("second-parent")
	secondParent.Status.Endpoints[0].Addresses[0].Value = "2001:db8::2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(older, newer, firstParent, secondParent, readyIntentNetwork(), testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", newer)
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestCellIntentWithholdsMultipleVIPsOnOneListener(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	second := parent.Spec.Exposure.Addresses[0]
	second.Name = "second"
	parent.Spec.Exposure.Addresses = append(parent.Spec.Exposure.Addresses, second)
	parent.Spec.Listeners[0].AddressRefs = []string{"regional", "second"}
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{
		allocatedCellIntentEndpoint("2001:db8::1", 5),
		allocatedCellIntentEndpoint("2001:db8::2", 5),
	}
	parent.Status.Endpoints[1].RequestName = "second"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(), testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestCellIntentWithdrawsTwoAllocatedVIPsForOneRequest(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{
		allocatedCellIntentEndpoint("2001:db8::1", 5),
		allocatedCellIntentEndpoint("2001:db8::2", 5),
	}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(), testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 2)
	for _, intent := range desired {
		require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
		require.Empty(t, intent.Spec.Backends)
	}
}

func TestCellIntentWithholdsSameRouteSecondParentBackendClaim(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	route.Spec.ParentRefs = append(route.Spec.ParentRefs,
		networkingv1alpha.NetworkLoadBalancerRouteParentRef{Name: "edge-2", Listener: "tcp"})
	firstParent := testRouteLoadBalancer()
	firstParent.UID = types.UID("first-parent")
	firstParent.Generation = 5
	firstParent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{
		allocatedCellIntentEndpoint("2001:db8::1", 5),
	}
	secondParent := firstParent.DeepCopy()
	secondParent.Name = "edge-2"
	secondParent.UID = types.UID("second-parent")
	secondParent.Status.Endpoints[0].Addresses[0].Value = "2001:db8::2"
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, firstParent, secondParent, readyIntentNetwork(), testRouteNetworkService(),
			testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Equal(t, "edge", desired[0].Spec.Source.ParentName)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired[0].Spec.State)
}

func TestCellIntentWithholdsAllocationFromOlderGeneration(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", 4)}
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(),
			testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)

	parent.Status.Endpoints[0].Addresses[0].Conditions[0].ObservedGeneration = 5
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
}

func TestCellIntentSkipsBackendsThatAreNotBound(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", 5)}
	bound := testRouteMember("bound", true, "fd00::1/128")
	released := testRouteMember("released", true, "fd00::2/128")
	released.Status.Phase = networkingv1alpha.NetworkInterfacePhaseAvailable
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(), testRouteNetworkService(), bound, released).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Len(t, desired[0].Spec.Backends, 1)
	require.Equal(t, "source-bound", desired[0].Spec.Backends[0].SourceUID)
}
