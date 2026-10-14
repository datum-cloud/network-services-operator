// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// A subnet whose spec already carries a range was allocated out of the space
// its network holds, and there is nothing left here to allocate. The table this
// reconciler falls back to answers with an IPv4 /20 chosen by city code, which
// for an IPv6 subnet is not merely a different range but a different address
// family, so the spec has to win.
func TestSubnetPublishesTheRangeItsSpecCarries(t *testing.T) {
	cl, _ := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	namespace := &corev1.Namespace{}
	namespace.Name = "ns-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, cl.Create(ctx, namespace))

	subnet := &networkingv1alpha.Subnet{}
	subnet.Namespace = namespace.Name
	subnet.Name = "vpc-us-central-1-ipv6"
	subnet.Spec = networkingv1alpha.SubnetSpec{
		SubnetClass:    privateSubnetClass,
		IPFamily:       networkingv1alpha.IPv6Protocol,
		NetworkContext: networkingv1alpha.LocalNetworkContextRef{Name: "vpc-us-central-1"},
		Location:       locationsv1alpha1.LocationReference{Name: testLocationName},
		StartAddress:   "fd20:1000:1:2::",
		PrefixLength:   64,
	}
	require.NoError(t, cl.Create(ctx, subnet))

	reconciler := &SubnetReconciler{}
	err := reconciler.reconcileSubnet(ctx, cl, subnet, nil)
	require.NoError(t, err, "a subnet carrying its own range needs neither its context nor its location")

	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(subnet), subnet))
	require.NotNil(t, subnet.Status.StartAddress)
	require.Equal(t, "fd20:1000:1:2::", *subnet.Status.StartAddress)
	require.Equal(t, int32(64), *subnet.Status.PrefixLength)
	require.True(t, apimeta.IsStatusConditionTrue(subnet.Status.Conditions, networkingv1alpha.SubnetAllocated))

	require.Equal(t, "fd20:1000:1:2::1", subnetGateway(subnet),
		"the gateway an interface is handed is ::1 of the range the subnet publishes")
}

// The path the subnet claim API drives is unchanged: a subnet declaring no
// range of its own is still allocated one here, from its location.
func TestSubnetWithoutARangeIsStillAllocatedFromItsLocation(t *testing.T) {
	cl, _ := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	namespace := &corev1.Namespace{}
	namespace.Name = "ns-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, cl.Create(ctx, namespace))

	location := &locationsv1alpha1.Location{}
	location.Name = "dfw"
	location.Spec = locationsv1alpha1.LocationSpec{
		LocationClassRef: locationsv1alpha1.LocationClassReference{Name: "datum-managed"},
		Topology:         map[string]string{locationsv1alpha1.TopologyCityCodeKey: "DFW"},
	}
	require.NoError(t, cl.Create(ctx, location))

	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = namespace.Name
	networkContext.Name = "vpc-dfw"
	networkContext.Spec = networkingv1alpha.NetworkContextSpec{
		Network:  networkingv1alpha.LocalNetworkRef{Name: testNetworkName},
		Location: locationsv1alpha1.LocationReference{Name: location.Name},
	}
	require.NoError(t, cl.Create(ctx, networkContext))
	apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:    networkingv1alpha.NetworkContextReady,
		Status:  metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkContextReadyReasonReady,
		Message: "ready",
	})
	require.NoError(t, cl.Status().Update(ctx, networkContext))

	subnet := &networkingv1alpha.Subnet{}
	subnet.Namespace = namespace.Name
	subnet.Name = "vpc-dfw-0"
	subnet.Spec = networkingv1alpha.SubnetSpec{
		SubnetClass:    privateSubnetClass,
		IPFamily:       networkingv1alpha.IPv4Protocol,
		NetworkContext: networkingv1alpha.LocalNetworkContextRef{Name: networkContext.Name},
		Location:       locationsv1alpha1.LocationReference{Name: location.Name},
	}
	require.NoError(t, cl.Create(ctx, subnet))

	reconciler := &SubnetReconciler{}
	err := reconciler.reconcileSubnet(ctx, cl, subnet, nil)
	require.NoError(t, err)

	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(subnet), subnet))
	require.NotNil(t, subnet.Status.StartAddress)
	require.Equal(t, "10.128.0.0", *subnet.Status.StartAddress)
	require.Equal(t, int32(20), *subnet.Status.PrefixLength)
}

// hubCopyWithReport is the hub copy of a subnet after Karmada aggregated what
// the cells said: the upstream generation the replicator stamped on it, and
// the Programmed condition the cells wrote for that generation.
func hubCopyWithReport(upstreamGeneration string, status metav1.ConditionStatus, reason string, observedGeneration int64) *networkingv1alpha.Subnet {
	hubCopy := &networkingv1alpha.Subnet{}
	hubCopy.Namespace = "ns-hub"
	hubCopy.Name = "vpc-us-central-1-ipv6"
	if upstreamGeneration != "" {
		hubCopy.Annotations = map[string]string{networkingv1alpha1.UpstreamGenerationAnnotation: upstreamGeneration}
	}
	if reason != "" {
		hubCopy.Status.Conditions = []metav1.Condition{{
			Type: networkingv1alpha.SubnetProgrammed, Status: status, Reason: reason,
			ObservedGeneration: observedGeneration, Message: "2/2 cells carry this range",
		}}
	}
	return hubCopy
}

// Programmed is what the cells said, taken only for the generation they said
// it about. Anything else reads as not yet reported, never as a verdict.
func TestSubnetProgrammedFollowsWhatTheCellsReported(t *testing.T) {
	cases := []struct {
		name    string
		hubCopy *networkingv1alpha.Subnet
		status  metav1.ConditionStatus
		reason  string
	}{
		{"no hub copy", nil, metav1.ConditionUnknown, networkingv1alpha.SubnetProgrammedReasonPending},
		{"copy of an older generation", hubCopyWithReport("1", metav1.ConditionTrue, networkingv1alpha.SubnetProgrammedReasonProgrammed, 1),
			metav1.ConditionUnknown, networkingv1alpha.SubnetProgrammedReasonPending},
		{"nothing reported yet", hubCopyWithReport("2", "", "", 0),
			metav1.ConditionUnknown, networkingv1alpha.SubnetProgrammedReasonPending},
		{"report for an older generation", hubCopyWithReport("2", metav1.ConditionTrue, networkingv1alpha.SubnetProgrammedReasonProgrammed, 1),
			metav1.ConditionUnknown, networkingv1alpha.SubnetProgrammedReasonPending},
		{"in place on every cell", hubCopyWithReport("2", metav1.ConditionTrue, networkingv1alpha.SubnetProgrammedReasonProgrammed, 2),
			metav1.ConditionTrue, networkingv1alpha.SubnetProgrammedReasonProgrammed},
		{"still being put in place", hubCopyWithReport("2", metav1.ConditionFalse, networkingv1alpha.SubnetProgrammedReasonProgrammingInProgress, 2),
			metav1.ConditionFalse, networkingv1alpha.SubnetProgrammedReasonProgrammingInProgress},
		{"a cell refused it", hubCopyWithReport("2", metav1.ConditionFalse, networkingv1alpha.SubnetProgrammedReasonNotProgrammed, 2),
			metav1.ConditionFalse, networkingv1alpha.SubnetProgrammedReasonNotProgrammed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			condition := cellProgrammedCondition(tc.hubCopy, 2, testLocationName)
			require.Equal(t, networkingv1alpha.SubnetProgrammed, condition.Type)
			require.Equal(t, tc.status, condition.Status)
			require.Equal(t, tc.reason, condition.Reason)
			require.Equal(t, int64(2), condition.ObservedGeneration)
			require.Contains(t, condition.Message, testLocationName, "the message names the location, not the cells")
			require.NotContains(t, condition.Message, "cells", "how many cells serve a location is the platform's")
		})
	}
}

// A hub copy finds its way back to the project subnet by the labels the
// replicator stamps on it, so a cell's report reconciles the subnet it is about.
func TestHubCopyEnqueuesTheProjectSubnetItMirrors(t *testing.T) {
	hubCopy := &networkingv1alpha.Subnet{}
	hubCopy.Namespace = "ns-hub"
	hubCopy.Name = "vpc-us-central-1-ipv6"
	hubCopy.Labels = map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-project-abc",
		downstreamclient.UpstreamOwnerNamespaceLabel:   "default",
		downstreamclient.UpstreamOwnerNameLabel:        "vpc-us-central-1-ipv6",
	}

	requests := enqueueSubnetForHubCopy(context.Background(), hubCopy)
	require.Len(t, requests, 1)
	require.Equal(t, "project-abc", string(requests[0].ClusterName))
	require.Equal(t, client.ObjectKey{Namespace: "default", Name: "vpc-us-central-1-ipv6"}, requests[0].NamespacedName)

	delete(hubCopy.Labels, downstreamclient.UpstreamOwnerNamespaceLabel)
	require.Empty(t, enqueueSubnetForHubCopy(context.Background(), hubCopy),
		"a copy that names no owner is not something this operator mirrored")
}

// Ready follows the cells both ways: it turns true once they carry the range,
// and false again if they stop.
func TestSubnetReadyFollowsTheCellsReport(t *testing.T) {
	cl, _ := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	namespace := &corev1.Namespace{}
	namespace.Name = "ns-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, cl.Create(ctx, namespace))

	subnet := &networkingv1alpha.Subnet{}
	subnet.Namespace = namespace.Name
	subnet.Name = "vpc-us-central-1-ipv6"
	subnet.Spec = networkingv1alpha.SubnetSpec{
		SubnetClass:    privateSubnetClass,
		IPFamily:       networkingv1alpha.IPv6Protocol,
		NetworkContext: networkingv1alpha.LocalNetworkContextRef{Name: "vpc-us-central-1"},
		Location:       locationsv1alpha1.LocationReference{Name: testLocationName},
		StartAddress:   "fd20:1000:1:2::",
		PrefixLength:   64,
	}
	require.NoError(t, cl.Create(ctx, subnet))

	reconciler := &SubnetReconciler{}

	require.NoError(t, reconciler.reconcileSubnet(ctx, cl, subnet, nil))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(subnet), subnet))
	require.False(t, apimeta.IsStatusConditionTrue(subnet.Status.Conditions, networkingv1alpha.SubnetReady),
		"nothing has reported, so the range is set aside but not ready")

	inPlace := cellProgrammedCondition(
		hubCopyWithReport("1", metav1.ConditionTrue, networkingv1alpha.SubnetProgrammedReasonProgrammed, 1), 1, testLocationName)
	require.NoError(t, reconciler.reconcileSubnet(ctx, cl, subnet, &inPlace))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(subnet), subnet))
	require.True(t, apimeta.IsStatusConditionTrue(subnet.Status.Conditions, networkingv1alpha.SubnetProgrammed))
	require.True(t, apimeta.IsStatusConditionTrue(subnet.Status.Conditions, networkingv1alpha.SubnetReady))

	gone := cellProgrammedCondition(
		hubCopyWithReport("1", metav1.ConditionFalse, networkingv1alpha.SubnetProgrammedReasonNotProgrammed, 1), 1, testLocationName)
	require.NoError(t, reconciler.reconcileSubnet(ctx, cl, subnet, &gone))
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(subnet), subnet))
	require.False(t, apimeta.IsStatusConditionTrue(subnet.Status.Conditions, networkingv1alpha.SubnetReady),
		"a range the cells stopped carrying is no longer ready")
	ready := apimeta.FindStatusCondition(subnet.Status.Conditions, networkingv1alpha.SubnetReady)
	require.Equal(t, networkingv1alpha.SubnetProgrammedReasonNotProgrammed, ready.Reason)
}
