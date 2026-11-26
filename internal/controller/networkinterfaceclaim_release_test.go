// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const physicalClaimName = "server-data"

// boundPhysicalClaim stands up what a bound physical claim leaves behind: the
// claim, its interface, and an IPClaim under each of the given names. NSO
// refuses to bind physical claims until switch ports can be held, so the state
// is written directly, the way the bind path writes it.
func boundPhysicalClaim(s *scenario, ipClaimNames ...string) (*networkingv1alpha.NetworkInterfaceClaim, string) {
	s.t.Helper()
	name := physicalClaimName

	claim := s.createClaim(name, networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName:   "data",
		AttachmentMode:  networkingv1alpha.NetworkInterfaceAttachmentModePhysical,
		AttachmentPoint: physicalAttachmentPoint("Ethernet1/12"),
		IPFamilies:      []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		ReclaimPolicy:   networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(s.t, err)
	var address string
	for _, ipClaimName := range ipClaimNames {
		ipClaim := physicalIPClaim(ipClaimName, testLocationName)
		require.NoError(s.t, ipamClient.Create(s.ctx, ipClaim))
		require.NoError(s.t, ipamClient.Get(s.ctx, client.ObjectKeyFromObject(ipClaim), ipClaim))
		if address == "" {
			address = ipClaim.Status.AllocatedCIDR
		}
	}
	require.NotEmpty(s.t, address)

	claim = s.getClaim(name)
	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Spec.Network.Name = "default"
	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   s.namespace,
			Name:        name,
			Annotations: map[string]string{allocationClaimAnnotation: name},
			Finalizers:  []string{networkInterfaceFinalizer},
		},
		Spec: interfaceSpecForClaim(claim, networkContext),
	}
	iface.Spec.Addresses = []networkingv1alpha.NetworkInterfaceAddress{{
		Family: networkingv1alpha.IPv4Protocol, Address: address, Primary: true,
	}}
	require.NoError(s.t, s.client.Create(s.ctx, iface))

	claim.Status.NetworkInterfaceRef = &networkingv1alpha.LocalNetworkInterfaceRef{Name: name}
	require.NoError(s.t, s.client.Status().Update(s.ctx, claim))
	return s.getClaim(name), address
}

func physicalIPClaim(name, location string) *ipamv1alpha1.IPClaim {
	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = testProjectNS
	ipClaim.Name = name
	ipClaim.Spec = ipamv1alpha1.IPClaimSpec{
		IPFamily:      ipamv1alpha1.IPFamily(networkingv1alpha.IPv4Protocol),
		ReclaimPolicy: ipamv1alpha1.ReclaimDelete,
		Scope: map[string]ipamv1alpha1.ScopeRef{
			ipamScopeRoleNetwork:  {APIGroup: datumNetworkingAPIGroup, Kind: "Network", Name: "default"},
			ipamScopeRoleLocation: {APIGroup: datumNetworkingAPIGroup, Kind: "Location", Name: location},
		},
	}
	return ipClaim
}

func physicalIPClaimNames(claimName string) []string {
	return ipClaimNames(claimName, familyDiscriminator(networkingv1alpha.IPv4Protocol),
		locationsv1alpha1.LocationReference{Name: testLocationName})
}

func (s *scenario) claimGone(name string) bool {
	s.t.Helper()
	var claim networkingv1alpha.NetworkInterfaceClaim
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &claim)
	if apierrors.IsNotFound(err) {
		return true
	}
	require.NoError(s.t, err)
	return false
}

func (s *scenario) requireReleaseUnverified(name, address string) {
	s.t.Helper()
	require.False(s.t, s.claimGone(name), "the claim must stay while IPAM holds its address")
	ready := conditionOf(s.getClaim(name), networkingv1alpha.NetworkInterfaceClaimReady)
	require.NotNil(s.t, ready)
	require.Equal(s.t, metav1.ConditionFalse, ready.Status)
	require.Equal(s.t, networkingv1alpha.NetworkInterfaceClaimReasonReleaseUnverified, ready.Reason)
	require.Contains(s.t, ready.Message, address)
	for _, name := range physicalIPClaimNames(name) {
		require.NotContains(s.t, ready.Message, name, "the reason must not name NSO's IPAM claims")
	}
	_, err := s.getInterface(name)
	require.NoError(s.t, err, "the interface records the IPAM names and must outlive an unverified release")
}

func TestPhysicalReleaseCompletesOnceIPAMLetsGo(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, _ := boundPhysicalClaim(s, names[0])

	s.deleteClaim(claim)

	require.True(t, s.claimGone(physicalClaimName))
	_, err := s.getInterface(physicalClaimName)
	require.True(t, apierrors.IsNotFound(err))
	require.Zero(t, s.ipam.storedAnywhere(t))
}

func TestPhysicalReleaseWaitsWhileIPAMRefusesTheDelete(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, _ := boundPhysicalClaim(s, names[0])
	s.ipam.refuseRelease(names[0], errors.New("ipam is unavailable"))

	require.NoError(t, s.client.Delete(s.ctx, claim))
	_, err := s.reconciler.reconcileClaim(s.ctx, s.client, s.events, client.ObjectKeyFromObject(claim))
	require.Error(t, err)
	require.False(t, s.claimGone(physicalClaimName))

	s.ipam.refuseRelease(names[0], nil)
	s.reconcile(claim)
	require.True(t, s.claimGone(physicalClaimName), "a release that fails is retried, and completes once IPAM answers")
}

func TestPhysicalReleaseWaitsForALingeringAllocation(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, address := boundPhysicalClaim(s, names[0])

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	lingering := &ipamv1alpha1.IPAllocation{}
	lingering.Namespace = testProjectNS
	lingering.Name = "lingering"
	lingering.Spec.ClaimRef = &ipamv1alpha1.LocalRef{Name: names[0]}
	lingering.Spec.Scope = map[string]ipamv1alpha1.ScopeRef{ipamScopeRoleLocation: {Name: testLocationName}}
	require.NoError(t, ipamClient.Create(s.ctx, lingering))

	s.deleteClaim(claim)
	s.requireReleaseUnverified(physicalClaimName, address)
	s.reconcile(claim)
	s.requireReleaseUnverified(physicalClaimName, address)

	require.NoError(t, ipamClient.Get(s.ctx, client.ObjectKeyFromObject(lingering), lingering))
	lingering.Spec.ClaimRef = nil
	require.NoError(t, ipamClient.Update(s.ctx, lingering))
	s.reconcile(claim)
	require.True(t, s.claimGone(physicalClaimName))
}

func TestPhysicalReleaseFreesEveryNameAnAddressIsHeldUnder(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, _ := boundPhysicalClaim(s, names...)

	s.deleteClaim(claim)
	require.False(t, s.claimGone(physicalClaimName), "one pass frees one name, so the claim waits for the other")

	s.reconcile(claim)
	require.True(t, s.claimGone(physicalClaimName))
	require.Zero(t, s.ipam.storedAnywhere(t), "both names must be released")
}

func TestPhysicalReleaseIgnoresAnotherLocationsAllocation(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, _ := boundPhysicalClaim(s, names[1])

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	foreign := physicalIPClaim(names[0], "location-elsewhere")
	require.NoError(t, ipamClient.Create(s.ctx, foreign))

	s.deleteClaim(claim)
	require.True(t, s.claimGone(physicalClaimName), "another location's same-named allocation is not this claim's")
	require.NoError(t, ipamClient.Get(s.ctx, client.ObjectKeyFromObject(foreign), foreign),
		"another location's allocation must be left alone")
}

func TestPhysicalReleaseKeepsItsReasonAcrossPasses(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, address := boundPhysicalClaim(s, names[0])

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	lingering := &ipamv1alpha1.IPAllocation{}
	lingering.Namespace = testProjectNS
	lingering.Name = "lingering"
	lingering.Spec.ClaimRef = &ipamv1alpha1.LocalRef{Name: names[0]}
	require.NoError(t, ipamClient.Create(s.ctx, lingering))

	s.deleteClaim(claim)
	first := s.getClaim(physicalClaimName).ResourceVersion
	s.reconcile(claim)
	s.requireReleaseUnverified(physicalClaimName, address)
	require.Equal(t, first, s.getClaim(physicalClaimName).ResourceVersion,
		"a pass that finds nothing new must not rewrite the claim")
}

// Verified release is for physical claims. Compute's claims release as they
// always have, so a lingering allocation does not hold them.
func TestVirtualReleaseIsUnchanged(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	claim := s.createClaim("web-0-eth0", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)
	require.NotNil(t, s.getClaim("web-0-eth0").Status.NetworkInterfaceRef)

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	lingering := &ipamv1alpha1.IPAllocation{}
	lingering.Namespace = testProjectNS
	lingering.Name = "lingering"
	lingering.Spec.ClaimRef = &ipamv1alpha1.LocalRef{Name: ipClaimName("web-0-eth0", familyDiscriminator(networkingv1alpha.IPv4Protocol))}
	require.NoError(t, ipamClient.Create(s.ctx, lingering))

	s.deleteClaim(s.getClaim("web-0-eth0"))
	require.True(t, s.claimGone("web-0-eth0"))
}

// An interface removed out of band takes the record of its IPAM names with it,
// so the claim falls back to the names derived from itself and still waits.
func TestPhysicalReleaseWaitsWhenTheInterfaceIsAlreadyGone(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})
	names := physicalIPClaimNames(physicalClaimName)
	claim, _ := boundPhysicalClaim(s, names[0])

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	lingering := &ipamv1alpha1.IPAllocation{}
	lingering.Namespace = testProjectNS
	lingering.Name = "lingering"
	lingering.Spec.ClaimRef = &ipamv1alpha1.LocalRef{Name: names[0]}
	require.NoError(t, ipamClient.Create(s.ctx, lingering))

	iface, err := s.getInterface(physicalClaimName)
	require.NoError(t, err)
	iface.Finalizers = nil
	require.NoError(t, s.client.Update(s.ctx, iface))
	require.NoError(t, s.client.Delete(s.ctx, iface))

	s.deleteClaim(claim)
	require.False(t, s.claimGone(physicalClaimName))
	ready := conditionOf(s.getClaim(physicalClaimName), networkingv1alpha.NetworkInterfaceClaimReady)
	require.Equal(t, networkingv1alpha.NetworkInterfaceClaimReasonReleaseUnverified, ready.Reason)
}
