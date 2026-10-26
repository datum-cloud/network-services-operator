// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func (s *scenario) keepInterface(name string) {
	s.t.Helper()
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

func TestANetworksDeletionGivesUpItsKeptInterfaces(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.keepInterface("kept")
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
	s.keepInterface("kept")
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
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(iface), &live); err != nil {
			return err
		}
		live.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: c.claim}
		if err := c.Client.Update(ctx, &live); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestAClaimThatBindsTheKeptInterfaceFirstKeepsIt(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})
	s.keepInterface("kept")
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
