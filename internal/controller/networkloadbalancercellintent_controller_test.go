// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
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
		RequestName: "regional", Location: "dfw", Family: networkingv1alpha.IPv6Protocol, Value: value,
		ClaimRef: &networkingv1alpha.NetworkLoadBalancerIPClaimRef{Project: "project", Namespace: "test", Name: "vip-claim"},
		Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkLoadBalancerAllocated,
			Status: metav1.ConditionTrue, Reason: "Allocated", ObservedGeneration: generation,
			LastTransitionTime: metav1.Now()}},
	}
}

func TestCellIntentPublishesAndWithdrawsWithoutDeletingHold(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("storefront", time.Unix(10, 0))
	route.UID = types.UID("route-1")
	parent := testRouteLoadBalancer()
	parent.UID = types.UID("parent-1")
	parent.Generation = 5
	parent.Status.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressStatus{allocatedCellIntentAddress("2001:db8::1", parent.Generation)}
	network := readyIntentNetwork()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: types.UID("project-ns")}}
	project := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(namespace, route, parent, network, testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).
		WithStatusSubresource(route, parent).Build()
	hub := fake.NewClientBuilder().WithScheme(project.Scheme()).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}).Build()
	reconciler := &NetworkLoadBalancerCellIntentReconciler{HubCluster: &hubFakeCluster{scheme: hub.Scheme(), client: hub}}
	key := client.ObjectKeyFromObject(route)

	_, err := reconciler.reconcileIntent(ctx, "project", project, key)
	require.NoError(t, err)
	var first networkingv1alpha.NetworkLoadBalancerCellIntentList
	require.NoError(t, hub.List(ctx, &first))
	require.Len(t, first.Items, 1)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, first.Items[0].Spec.State)
	require.Equal(t, "2001:db8::1", first.Items[0].Spec.VIP)
	require.Equal(t, "project", first.Items[0].Spec.Source.ProjectCluster)
	require.Equal(t, "route-1", first.Items[0].Spec.Source.RouteUID)
	require.Equal(t, "cell-test", first.Items[0].Spec.Backends[0].SourceNamespace)
	require.Equal(t, "source-member", first.Items[0].Spec.Backends[0].SourceUID)
	require.Equal(t, int32(8080), first.Items[0].Spec.Backends[0].Probe.Port)

	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	parent.Status.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressStatus{allocatedCellIntentAddress("2001:db8::2", parent.Generation)}
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
	parent.Status.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressStatus{
		allocatedCellIntentAddress("2001:db8::1", 4),
	}
	parent.Status.Addresses[0].Location = ""
	cl := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(route, parent, readyIntentNetwork(),
			testRouteNetworkService(), testRouteMember("member", true, "fd00::1/128")).Build()
	desired, err := buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)

	parent.Status.Addresses[0].Conditions[0].ObservedGeneration = 5
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)

	parent.Status.Addresses[0].Location = "dfw"
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Len(t, desired, 1)
	require.Equal(t, "dfw", desired[0].Spec.Location)
	require.Equal(t, networkingv1alpha.NetworkLoadBalancerCellIntentValidate, desired[0].Spec.State)

	duplicate := allocatedCellIntentAddress("2001:db8::1", 5)
	duplicate.Location = "ord"
	parent.Status.Addresses = append(parent.Status.Addresses, duplicate)
	require.NoError(t, cl.Update(ctx, parent))
	desired, err = buildNetworkLoadBalancerCellIntents(ctx, cl, "project", "hub", route)
	require.NoError(t, err)
	require.Empty(t, desired)
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
	parent.Status.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressStatus{allocatedCellIntentAddress("2001:db8::1", 5)}
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
