// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// The class name a deployment configures for the identifier space. It is
// configuration rather than a constant for the same reason the prefix class is.
const testFabricIdentityClass = "datum-fabric-identity"

func (s *networkScenario) allocatesFabricIdentity() *networkScenario {
	s.t.Helper()
	s.reconciler.FabricIdentityClass = testFabricIdentityClass
	s.reconciler.FabricIdentityNamespace = testPlatformNamespace
	return s
}

func (s *networkScenario) identityCondition() *metav1.Condition {
	s.t.Helper()
	for _, condition := range s.get().Status.Conditions {
		if condition.Type == networkingv1alpha.NetworkFabricIdentityAllocated {
			return &condition
		}
	}
	return nil
}

func TestNetworkIsGivenAFabricIdentityWhenCreated(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t)).allocatesFabricIdentity()

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	allocated := s.get()
	require.NotZero(t, allocated.Status.FabricIdentity,
		"the network must carry the identity the fabric knows it by")

	condition := s.identityCondition()
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkFabricIdentityReasonAllocated, condition.Reason)

	require.Contains(t, s.ipam.created()[testPlatformProject], fabricIdentityClaimName(network),
		"the identity must be allocated in the platform's own tenancy, not the consumer's project")
	require.NotContains(t, s.ipam.created()[testProject], fabricIdentityClaimName(network))
}

// Reconciling again must find the identity it already has rather than take a
// second one. An identity taken twice is address space burned and, worse, a
// network whose Route Target moved out from under every location carrying it.
func TestFabricIdentityIsAllocatedOnlyOnce(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t)).allocatesFabricIdentity()

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()
	first := s.get().Status.FabricIdentity
	require.NotZero(t, first)

	for range 3 {
		s.reconcile()
	}

	require.Equal(t, first, s.get().Status.FabricIdentity, "the identity must not change")
	require.Equal(t, []string{fabricIdentityClaimName(network)},
		s.ipam.created()[testPlatformProject],
		"a network already holding an identity must not ask for another")
}

// Two networks are two identities. One shared between them would make them one
// network on the fabric: each would import the other's routes.
func TestEachNetworkGetsItsOwnFabricIdentity(t *testing.T) {
	ipam := newFakeIPAM(t)

	first := newNetworkScenario(t, ipam).allocatesFabricIdentity()
	first.createNetwork(networkingv1alpha.IPv6Protocol)
	first.reconcile()

	second := newNetworkScenario(t, ipam).allocatesFabricIdentity()
	second.createNetwork(networkingv1alpha.IPv6Protocol)
	second.reconcile()

	require.NotEqual(t, first.get().Status.FabricIdentity, second.get().Status.FabricIdentity)
}

// The API refuses to move an identity that is already allocated, so no writer
// can change it whatever it believes.
func TestFabricIdentityIsImmutableOnceAllocated(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t)).allocatesFabricIdentity()

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	allocated := s.get()
	require.NotZero(t, allocated.Status.FabricIdentity)

	allocated.Status.FabricIdentity += 1
	err := s.client.Status().Update(s.ctx, allocated)
	require.Error(t, err, "the identity must not be movable")
	require.Contains(t, err.Error(), "immutable")
}

// A deployment that names no identifier space reconciles a network exactly as
// it did before one existed: no identity, and no condition claiming one is
// missing.
func TestNetworkWithoutAnIdentitySpaceIsUnchanged(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.Zero(t, s.get().Status.FabricIdentity)
	require.Nil(t, s.identityCondition())
	require.Equal(t, metav1.ConditionTrue, s.readyCondition().Status,
		"a network the platform allocates no identity for is still ready")
}

// A network that claims no address space still spans locations, so it still
// needs one identity there rather than a different one per location.
func TestFabricIdentityIsAllocatedForANetworkWithNoAddressSpace(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t)).allocatesFabricIdentity()

	s.createNetwork(networkingv1alpha.IPv4Protocol)
	s.reconcile()

	require.NotZero(t, s.get().Status.FabricIdentity)
}

// Zero is what an unallocated network reads as, so a network handed the pool's
// own first block must be refused rather than published as if it held nothing.
func TestFabricIdentityRefusesTheZeroBlock(t *testing.T) {
	ipam := newFakeIPAM(t)
	ipam.identityBase = 0

	s := newNetworkScenario(t, ipam).allocatesFabricIdentity()
	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.Zero(t, s.get().Status.FabricIdentity)

	condition := s.identityCondition()
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkFabricIdentityReasonIdentityUnusable, condition.Reason)

	require.NotEqual(t, metav1.ConditionTrue, s.readyCondition().Status,
		"a network the fabric cannot tell apart from another is not ready")
}

func TestFabricIdentityIsTheBlocksIndexInThePool(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cidr  string
		want  int64
		wants string
	}{
		{name: "the first usable block", cidr: "fc00:0:0:1::/64", want: 1},
		{name: "an index spanning both halves", cidr: "fc00:0:1234:5678::/64", want: 0x12345678},
		{name: "the last block in the pool", cidr: "fc00:0:ffff:ffff::/64", want: 0xffffffff},
		{name: "a pool rooted longer than a /32", cidr: "fc00:0:0:beef::/64", want: 0xbeef},

		{name: "the pool's own zero block", cidr: "fc00::/64", wants: "index is zero"},
		{name: "a block wider than a /64", cidr: "fc00:0:0:1::/48", wants: "read out of a /64"},
		{name: "a block narrower than a /64", cidr: "fc00:0:0:1::/96", wants: "read out of a /64"},
		{name: "an IPv4 block", cidr: "10.0.0.0/24", wants: "read out of an IPv6 space"},
		{name: "not a prefix at all", cidr: "fc00::", wants: "not a prefix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := fabricIdentityFromBlock(tc.cidr)
			if tc.wants != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wants)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, identity)

			// The width the fabric carries is the whole of it. A value that
			// needed more than 32 bits would be uniqueness the platform
			// believes it has and the Route Target does not.
			require.LessOrEqual(t, identity, int64(0xffffffff))
		})
	}
}
