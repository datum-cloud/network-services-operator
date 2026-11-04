// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"
	"go.miloapis.com/locations/pkg/locationidentity"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

// keepInterface leaves an interface named kept with no claim, holding its addresses.
func (s *scenario) keepInterface() {
	s.t.Helper()
	const name = "kept"
	claim := s.createClaim(name, networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(claim)
	s.deleteClaim(s.getClaim(name))

	kept, err := s.getInterface(name)
	require.NoError(s.t, err)
	require.Nil(s.t, kept.Spec.ClaimRef)
}

func (s *scenario) markNetworkDeleting() {
	s.t.Helper()
	networkContext, err := s.getNetworkContext()
	require.NoError(s.t, err)
	networkContext.Spec.NetworkDeleting = true
	require.NoError(s.t, s.client.Update(s.ctx, networkContext))
}

// servedByDelivery leaves the cell's location unconfigured and delivers it, as
// every staging and production cell learns its own.
func (s *scenario) servedByDelivery() *locationsv1alpha1.ServingLocation {
	s.t.Helper()
	s.reconciler.Location = config.LocationConfig{}
	served := &locationsv1alpha1.ServingLocation{}
	served.Name = testLocationName
	served.Spec.Topology = map[string]string{"topology.datum.net/city-code": "DFW"}
	require.NoError(s.t, s.client.Create(s.ctx, served))
	// A cell has one ServingLocation, and the planes outlive this test.
	s.t.Cleanup(func() { require.NoError(s.t, client.IgnoreNotFound(s.client.Delete(s.ctx, served))) })
	return served
}

func TestACellThatLearnsItsLocationGivesUpAKeptInterface(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.servedByDelivery()
	s.keepInterface()
	s.markNetworkDeleting()

	s.reconcileInterface("kept")
	s.reconcileInterface("kept")

	_, err := s.getInterface("kept")
	require.True(t, apierrors.IsNotFound(err), "the kept interface goes with its network: %v", err)
	require.Equal(t, []string{"kept-f-ipv6"}, s.ipam.deleted()[testProject])
}

// A cell that has lost its location cannot name the context it would read. It
// returns the error, whose retry is what picks the interface up again once the
// location is delivered.
func TestAKeptInterfaceWaitsUntilItsLocationResolves(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	served := s.servedByDelivery()
	s.keepInterface()
	s.markNetworkDeleting()
	require.NoError(t, s.client.Delete(s.ctx, served))

	interfaces := &NetworkInterfaceReconciler{Location: s.reconciler.Location, IPAM: s.ipam, claims: s.reconciler}
	err := interfaces.reconcileInterface(s.ctx, s.client, client.ObjectKey{Namespace: s.namespace, Name: "kept"})

	var unresolved *locationidentity.LocationUnresolved
	require.ErrorAs(t, err, &unresolved)
	kept, err := s.getInterface("kept")
	require.NoError(t, err)
	require.True(t, kept.DeletionTimestamp.IsZero(), "nothing goes on a location the cell cannot name")

	s.servedByDelivery()
	s.reconcileInterface("kept")
	s.reconcileInterface("kept")
	_, err = s.getInterface("kept")
	require.True(t, apierrors.IsNotFound(err), "the retry finds the mark once the location resolves: %v", err)
}

func TestAKeptInterfaceWhoseContextIsGoneStays(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.servedByDelivery()
	s.keepInterface()
	networkContext, err := s.getNetworkContext()
	require.NoError(t, err)
	networkContext.Finalizers = nil
	require.NoError(t, s.client.Update(s.ctx, networkContext))
	require.NoError(t, s.client.Delete(s.ctx, networkContext))

	s.reconcileInterface("kept")

	kept, err := s.getInterface("kept")
	require.NoError(t, err)
	require.True(t, kept.DeletionTimestamp.IsZero(), "no context, no mark")
}

func TestAnInterfaceReachesItsHoldAtTheCellsLocation(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, locationsv1alpha1.AddToScheme(scheme))
	iface := &networkingv1alpha.NetworkInterface{}
	iface.Namespace, iface.Spec.Network.Name = "project", "net"
	served := &locationsv1alpha1.ServingLocation{}
	served.Name = "delivered"

	for _, tc := range []struct {
		name       string
		configured string
		delivered  []client.Object
		want       []reconcile.Request
	}{
		{name: "delivered", delivered: []client.Object{served},
			want: []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: "project", Name: "net-delivered"}}}},
		{name: "configured", configured: "configured",
			want: []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: "project", Name: "net-configured"}}}},
		{name: "neither"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hold := &NetworkContextHoldReconciler{
				Location:    config.LocationConfig{Name: tc.configured},
				localReader: fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(tc.delivered...).Build(),
			}
			require.Equal(t, tc.want, hold.contextOfInterface(context.Background(), iface))
		})
	}
}

func TestANetworksDeletionGivesUpItsKeptInterfaces(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.keepInterface()
	s.markNetworkDeleting()

	s.reconcileInterface("kept")
	s.reconcileInterface("kept")

	_, err := s.getInterface("kept")
	require.True(t, apierrors.IsNotFound(err), "the kept interface goes with its network: %v", err)
	require.Equal(t, []string{"kept-f-ipv6"}, s.ipam.deleted()[testProject])
	require.Empty(t, s.ipam.orphanedAllocations())
}

func TestALocationTheNetworkLeavesKeepsItsKeptInterfaces(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.keepInterface()
	networkContext, err := s.getNetworkContext()
	require.NoError(t, err)
	require.NoError(t, s.client.Delete(s.ctx, networkContext))

	s.reconcileInterface("kept")

	kept, err := s.getInterface("kept")
	require.NoError(t, err)
	require.True(t, kept.DeletionTimestamp.IsZero(), "Retain holds the addresses while the network itself stays")
	require.Empty(t, s.ipam.deleted()[testProject])
}

func TestABoundInterfaceStaysWhenItsNetworkIsDeleted(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	claim := s.createClaim("bound", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(claim)
	s.markNetworkDeleting()

	s.reconcileInterface("bound")

	bound, err := s.getInterface("bound")
	require.NoError(t, err)
	require.True(t, bound.DeletionTimestamp.IsZero(), "a running instance's interface goes with its workload")
	require.Empty(t, s.ipam.deleted()[testProject])
}

// bindingClient binds the interface to a claim just before the reconciler's
// delete reaches the API server, as a returning claim of the same name would.
type bindingClient struct {
	client.Client
	claim string
}

func (c *bindingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if iface, ok := obj.(*networkingv1alpha.NetworkInterface); ok {
		var live networkingv1alpha.NetworkInterface
		if err := c.Get(ctx, client.ObjectKeyFromObject(iface), &live); err != nil {
			return err
		}
		live.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: c.claim}
		if err := c.Update(ctx, &live); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestAClaimThatBindsTheKeptInterfaceFirstKeepsIt(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.keepInterface()
	s.markNetworkDeleting()

	interfaces := &NetworkInterfaceReconciler{Location: s.reconciler.Location, IPAM: s.ipam, claims: s.reconciler}
	err := interfaces.reconcileInterface(s.ctx, &bindingClient{Client: s.client, claim: "kept"},
		client.ObjectKey{Namespace: s.namespace, Name: "kept"})
	require.True(t, apierrors.IsConflict(err), "the delete names the interface it read: %v", err)

	s.reconcileInterface("kept")

	rebound, err := s.getInterface("kept")
	require.NoError(t, err)
	require.True(t, rebound.DeletionTimestamp.IsZero(), "a claim that bound it first keeps it")
	require.Empty(t, s.ipam.deleted()[testProject])
}

// The mark can arrive after a kept interface's last event, and nothing about
// the interface changes then, so the context's own event has to reach it.
func TestAMarkedNetworkContextEnqueuesItsKeptInterfaces(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	interfaceOn := func(name, namespace, network string, bound bool) *networkingv1alpha.NetworkInterface {
		iface := &networkingv1alpha.NetworkInterface{}
		iface.Namespace = namespace
		iface.Name = name
		iface.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: network}
		if bound {
			iface.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: name}
		}
		return iface
	}

	cl := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(
		interfaceOn("kept", "ns-a", "default", false),
		interfaceOn("bound", "ns-a", "default", true),
		interfaceOn("other-network", "ns-a", "other", false),
		interfaceOn("elsewhere", "ns-b", "default", false),
	).Build()

	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = "ns-a"
	networkContext.Name = "default-us-central-1"
	networkContext.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	networkContext.Spec.NetworkDeleting = true

	require.Equal(t, []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: "ns-a", Name: "kept"}}},
		keptInterfacesOnNetworkContext(context.Background(), cl, networkContext),
		"only the kept interfaces of that network, in that namespace, are enqueued")

	networkContext.Spec.NetworkDeleting = false
	require.Empty(t, keptInterfacesOnNetworkContext(context.Background(), cl, networkContext),
		"an unmarked context gives up nothing")
}
