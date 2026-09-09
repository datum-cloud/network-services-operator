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

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
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
		Location:       networkingv1alpha.LocationReference{Name: testLocationName},
		StartAddress:   "fd20:1000:1:2::",
		PrefixLength:   64,
	}
	require.NoError(t, cl.Create(ctx, subnet))

	reconciler := &SubnetReconciler{}
	err := reconciler.reconcileSubnet(ctx, cl, subnet)
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

	location := &networkingv1alpha.Location{}
	location.Name = "dfw"
	location.Spec = networkingv1alpha.LocationSpec{
		LocationClassName: "datum-managed",
		Topology:          map[string]string{networkingv1alpha.TopologyCityCodeKey: "DFW"},
	}
	require.NoError(t, cl.Create(ctx, location))

	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = namespace.Name
	networkContext.Name = "vpc-dfw"
	networkContext.Spec = networkingv1alpha.NetworkContextSpec{
		Network:  networkingv1alpha.LocalNetworkRef{Name: testNetworkName},
		Location: networkingv1alpha.LocationReference{Name: location.Name},
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
		Location:       networkingv1alpha.LocationReference{Name: location.Name},
	}
	require.NoError(t, cl.Create(ctx, subnet))

	reconciler := &SubnetReconciler{}
	err := reconciler.reconcileSubnet(ctx, cl, subnet)
	require.NoError(t, err)

	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(subnet), subnet))
	require.NotNil(t, subnet.Status.StartAddress)
	require.Equal(t, "10.128.0.0", *subnet.Status.StartAddress)
	require.Equal(t, int32(20), *subnet.Status.PrefixLength)
}
