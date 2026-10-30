// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
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

	_, err := reconciler.reconcileParentIntents(ctx, "project", project, client.ObjectKeyFromObject(parent))
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
	_, err = reconciler.reconcileParentIntents(ctx, "project", project, client.ObjectKeyFromObject(parent))
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
	_, err = reconciler.reconcileParentIntents(ctx, "project", project, client.ObjectKeyFromObject(parent))
	require.NoError(t, err)
	var withdrawn networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &withdrawn))
	require.Len(t, withdrawn.Items, 2)
	for _, intent := range withdrawn.Items {
		require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
		require.Empty(t, intent.Spec.Backends)
	}
	// The route must remain until both withdrawal conditions acknowledge the current generation.
	require.NoError(t, project.Get(ctx, key, route))
	require.Contains(t, route.Finalizers, parentRouteIntentFinalizer(parent.Name))
	for i := range withdrawn.Items {
		intent := &withdrawn.Items[i]
		intent.Status.ObservedGeneration = intent.Generation
		intent.Status.Conditions = []metav1.Condition{
			{Type: networkingv1alpha.NetworkLoadBalancerCellIntentProgrammed, Status: metav1.ConditionFalse, Reason: "Withdrawn", ObservedGeneration: intent.Generation, LastTransitionTime: metav1.Now()},
			{Type: networkingv1alpha.NetworkLoadBalancerCellIntentAnnounced, Status: metav1.ConditionFalse, Reason: "Withdrawn", ObservedGeneration: intent.Generation, LastTransitionTime: metav1.Now()},
		}
		require.NoError(t, hub.Update(ctx, intent))
	}
	result, err := reconciler.reconcileParentIntents(ctx, "project", project, client.ObjectKeyFromObject(parent))
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	require.True(t, apierrors.IsNotFound(project.Get(ctx, key, route)))
	require.NoError(t, hub.List(ctx, &withdrawn))
	require.Empty(t, withdrawn.Items)
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

func TestCellIntentPublishesIPv4VIP(t *testing.T) {
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
	require.Len(t, desired, 1)
	require.Equal(t, networkingv1alpha.IPv4Protocol, desired[0].Spec.VIPFamily)
	require.Equal(t, "192.0.2.10", desired[0].Spec.VIP)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired[0].Spec.State)
	require.Equal(t, networkingv1alpha.IPv6Protocol, desired[0].Spec.Backends[0].Family)
}

func TestWithdrawConflictingCellIntentsKeepsOneVIPPerFamily(t *testing.T) {
	backend := networkingv1alpha.NetworkLoadBalancerCellIntentBackend{Address: "fd00::1", Port: 8080}
	intent := func(family networkingv1alpha.IPFamily, vip string) networkingv1alpha.NetworkLoadBalancerCellIntent {
		return networkingv1alpha.NetworkLoadBalancerCellIntent{Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
			NetworkName: "default", Location: "dfw", VIPFamily: family, VIP: vip,
			Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP,
			State:    networkingv1alpha.NetworkLoadBalancerCellIntentValidate,
			Backends: []networkingv1alpha.NetworkLoadBalancerCellIntentBackend{backend},
		}}
	}
	dualStack := []networkingv1alpha.NetworkLoadBalancerCellIntent{
		intent(networkingv1alpha.IPv4Protocol, "192.0.2.10"),
		intent(networkingv1alpha.IPv6Protocol, "2001:db8::10"),
	}
	withdrawConflictingCellIntents(dualStack)
	for _, desired := range dualStack {
		require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired.Spec.State)
	}

	twoIPv4 := []networkingv1alpha.NetworkLoadBalancerCellIntent{
		intent(networkingv1alpha.IPv4Protocol, "192.0.2.10"),
		intent(networkingv1alpha.IPv4Protocol, "192.0.2.11"),
	}
	withdrawConflictingCellIntents(twoIPv4)
	for _, desired := range twoIPv4 {
		require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, desired.Spec.State)
	}
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

func TestCellIntentWithdrawalRequiresBothCurrentConditions(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		programmed, announced metav1.ConditionStatus
		stale                 bool
		want                  bool
	}{
		{name: "both withdrawn", programmed: metav1.ConditionFalse, announced: metav1.ConditionFalse, want: true},
		{name: "still advertised", programmed: metav1.ConditionFalse, announced: metav1.ConditionTrue},
		{name: "unknown advertisement", programmed: metav1.ConditionFalse, announced: metav1.ConditionUnknown},
		{name: "stale acknowledgment", programmed: metav1.ConditionFalse, announced: metav1.ConditionFalse, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, intent := dataPlaneFixture()
			intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn
			reportIntent(&intent, tc.programmed, tc.announced)
			if tc.stale {
				intent.Generation++
			}
			require.Equal(t, tc.want, intentWithdrawalAcknowledged(&intent))
		})
	}
}

func TestCellIntentOrphanCleanupWaitsForAcknowledgment(t *testing.T) {
	ctx := context.Background()
	_, intent := dataPlaneFixture()
	intent.Namespace = "hub"
	intent.Spec.Source.ProjectCluster = "project"
	intent.Spec.Source.Namespace = "test"
	intent.Spec.Source.RouteName = "gone"
	intent.Spec.Source.ParentName = "gone"
	intent.Labels = map[string]string{downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-project", downstreamclient.UpstreamOwnerNamespaceLabel: "test"}
	other := intent.DeepCopy()
	other.Name = "other-route"
	other.Spec.Source.RouteName = "other"
	other.Spec.Source.ParentName = "other"
	hub := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(&intent, other).Build()
	r := NetworkLoadBalancerCellIntentReconciler{HubCluster: &fakeCluster{cl: hub}}
	result, err := r.reconcileParentIntents(ctx, "project", hub, client.ObjectKey{Namespace: "test", Name: "gone"})
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(&intent), &intent))
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
	reportIntent(&intent, metav1.ConditionFalse, metav1.ConditionFalse)
	require.NoError(t, hub.Update(ctx, &intent))
	result, err = r.reconcileParentIntents(ctx, "project", hub, client.ObjectKey{Namespace: "test", Name: "gone"})
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	require.True(t, apierrors.IsNotFound(hub.Get(ctx, client.ObjectKeyFromObject(&intent), &intent)))
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(other), other))
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, other.Spec.State)
}

// karmadaClaimLabel is the label Karmada's detector puts on a hub object to
// claim it for nso-resources, and puts back whenever it is missing.
const karmadaClaimLabel = "clusterpropagationpolicy.karmada.io/permanent-id"

// Publishing converges the intent's own labels and leaves the rest. A publish
// that removed Karmada's claim would be followed by Karmada claiming it again,
// and the hub watch would bring that update straight back here.
func TestCellIntentPublishKeepsLabelsItDoesNotOwn(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	route.UID = types.UID("route-1")
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{allocatedCellIntentEndpoint("2001:db8::1", parent.Generation)}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: types.UID("project-ns")}}
	project := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(namespace, route, parent, readyIntentNetwork(), testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(route, parent).Build()
	hub, _ := startNetworkInterfaceEnv(t)
	require.NoError(t, hub.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}))
	reconciler := &NetworkLoadBalancerCellIntentReconciler{HubCluster: &fakeCluster{cl: hub}}
	key := client.ObjectKeyFromObject(parent)

	_, err := reconciler.reconcileParentIntents(ctx, "project", project, key)
	require.NoError(t, err)
	var published networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &published))
	require.Len(t, published.Items, 1)
	claimed := &published.Items[0]
	claimed.Labels[karmadaClaimLabel] = "nso-resources-id"
	require.NoError(t, hub.Update(ctx, claimed))

	_, err = reconciler.reconcileParentIntents(ctx, "project", project, key)
	require.NoError(t, err)

	var after networkingv1alpha.NetworkLoadBalancerCellIntent
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(claimed), &after))
	require.Equal(t, "nso-resources-id", after.Labels[karmadaClaimLabel], "Karmada's claim stays")
	require.Equal(t, claimed.ResourceVersion, after.ResourceVersion,
		"an intent that differs from what is published only by a label NSO does not own is not written")

	after.Labels["networking.datumapis.com/stale"] = "left-behind"
	require.NoError(t, hub.Update(ctx, &after))
	_, err = reconciler.reconcileParentIntents(ctx, "project", project, key)
	require.NoError(t, err)
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(claimed), &after))
	require.NotContains(t, after.Labels, "networking.datumapis.com/stale", "a key NSO owns and no longer wants goes")
	require.Equal(t, "nso-resources-id", after.Labels[karmadaClaimLabel])
}

func TestWithdrawConflictingCellIntentsScopesRangesByFamily(t *testing.T) {
	shared := networkingv1alpha.NetworkLoadBalancerCellIntent{Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
		NetworkName: "default", Location: "dfw", VIPFamily: networkingv1alpha.IPv6Protocol,
		Protocol:     networkingv1alpha.NetworkLoadBalancerProtocolUDP,
		State:        networkingv1alpha.NetworkLoadBalancerCellIntentValidateAllocation,
		AddressGroup: &networkingv1alpha.NetworkLoadBalancerCellIntentAddressGroup{},
		Backends: []networkingv1alpha.NetworkLoadBalancerCellIntentBackend{{
			Address: "fd00::1", PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127},
		}},
	}}
	legacy := *shared.DeepCopy()
	legacy.Spec.AddressGroup = nil
	legacy.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentValidate
	legacy.Spec.VIPFamily = networkingv1alpha.IPv4Protocol
	legacy.Spec.Backends[0].Port, legacy.Spec.Backends[0].PortRange = 10050, nil
	intents := []networkingv1alpha.NetworkLoadBalancerCellIntent{shared, legacy}
	withdrawConflictingCellIntents(intents)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidateAllocation, intents[0].Spec.State)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, intents[1].Spec.State)
	intents[1].Spec.VIPFamily = networkingv1alpha.IPv6Protocol
	withdrawConflictingCellIntents(intents)
	for _, intent := range intents {
		require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn, intent.Spec.State)
	}
}
