// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	otherProject    = "customer-other-example"
	providerProject = "provider-example"
)

var ipv4Only = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}

func (s *scenario) attachmentPoint(port string) *networkingv1alpha.NetworkInterfaceAttachmentPoint {
	return &networkingv1alpha.NetworkInterfaceAttachmentPoint{
		MACAddress: "00:00:5e:00:53:01",
		Switch:     s.switchName,
		Port:       port,
	}
}

func (s *scenario) physicalSpec(port string, purpose networkingv1alpha.NetworkInterfacePurpose) networkingv1alpha.NetworkInterfaceClaimSpec {
	return networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName:   "data",
		AttachmentMode:  networkingv1alpha.NetworkInterfaceAttachmentModePhysical,
		AttachmentPoint: s.attachmentPoint(port),
		Purpose:         purpose,
		IPFamilies:      ipv4Only,
		ReclaimPolicy:   networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	}
}

func (s *scenario) createPhysicalClaim(
	name, port string,
	purpose networkingv1alpha.NetworkInterfacePurpose,
) *networkingv1alpha.NetworkInterfaceClaim {
	s.t.Helper()
	claim := &networkingv1alpha.NetworkInterfaceClaim{}
	claim.Namespace = s.namespace
	claim.Name = name
	claim.Finalizers = []string{networkInterfaceClaimFinalizer, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer}
	claim.Spec = s.physicalSpec(port, purpose)
	claim.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	require.NoError(s.t, s.client.Create(s.ctx, claim))
	return claim
}

func (s *scenario) sibling(project string) *scenario {
	s.t.Helper()
	namespace := &corev1.Namespace{}
	namespace.Name = "ns-" + scenarioSuffix(s.t)
	namespace.Labels = map[string]string{
		downstreamclient.UpstreamOwnerNamespaceLabel:   testProjectNS,
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + project,
	}
	require.NoError(s.t, s.client.Create(s.ctx, namespace))

	network := &networkingv1alpha.Network{}
	network.Namespace = namespace.Name
	network.Name = "default"
	network.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: ipv4Only,
		MTU:        1440,
	}
	require.NoError(s.t, s.client.Create(s.ctx, network))

	other := *s
	other.namespace = namespace.Name
	other.createNetworkContext("default", ipv4Only, 1440)
	return &other
}

func (s *scenario) getHold(port string) (*networkingv1alpha.SwitchPortHold, error) {
	hold := &networkingv1alpha.SwitchPortHold{}
	err := s.client.Get(s.ctx, client.ObjectKey{Name: networkingv1alpha.SwitchPortHoldName(s.switchName, port)}, hold)
	return hold, err
}

func (s *scenario) requireHold(port string) *networkingv1alpha.SwitchPortHold {
	s.t.Helper()
	hold, err := s.getHold(port)
	require.NoError(s.t, err)
	return hold
}

// detach stands in for the realizer.
func (s *scenario) detach(name string) {
	s.t.Helper()
	claim := s.getClaim(name)
	require.True(s.t, controllerutil.RemoveFinalizer(claim, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer),
		"the claim must still carry the detach finalizer")
	require.NoError(s.t, s.client.Update(s.ctx, claim))
}

func (s *scenario) requireAwaitingDetach(name string) {
	s.t.Helper()
	claim := s.getClaim(name)
	require.Contains(s.t, claim.Finalizers, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer)
	ready := conditionOf(claim, networkingv1alpha.NetworkInterfaceClaimReady)
	require.NotNil(s.t, ready)
	require.Equal(s.t, metav1.ConditionFalse, ready.Status)
	require.Equal(s.t, networkingv1alpha.NetworkInterfaceClaimReasonAwaitingDetach, ready.Reason)
	prepared := conditionOf(claim, networkingv1alpha.NetworkInterfaceClaimPrepared)
	require.NotNil(s.t, prepared)
	require.Equal(s.t, networkingv1alpha.NetworkInterfaceClaimReasonReleasing, prepared.Reason)
}

func (s *scenario) deleteAndDetach(claim *networkingv1alpha.NetworkInterfaceClaim) {
	s.t.Helper()
	require.NoError(s.t, s.client.Delete(s.ctx, claim))
	s.reconcile(claim)
	s.requireAwaitingDetach(claim.Name)
	s.detach(claim.Name)
}

func (s *scenario) deletePhysicalClaim(claim *networkingv1alpha.NetworkInterfaceClaim) {
	s.t.Helper()
	s.deleteAndDetach(claim)
	s.reconcile(claim)
}

func (s *scenario) requireWaiting(name string, unnamed ...string) {
	s.t.Helper()
	claim := s.getClaim(name)
	require.Nil(s.t, claim.Status.NetworkInterfaceRef)
	for _, conditionType := range []string{
		networkingv1alpha.NetworkInterfaceClaimBound,
		networkingv1alpha.NetworkInterfaceClaimAllocated,
		networkingv1alpha.NetworkInterfaceClaimReady,
	} {
		condition := conditionOf(claim, conditionType)
		require.NotNil(s.t, condition, conditionType)
		require.Equal(s.t, metav1.ConditionFalse, condition.Status, conditionType)
		require.Equal(s.t, networkingv1alpha.NetworkInterfaceClaimReasonAttachmentPointHeld, condition.Reason, conditionType)
		for _, word := range unnamed {
			require.NotContains(s.t, condition.Message, word, "a waiting claim must not learn what holds its port")
		}
	}
	_, err := s.getInterface(name)
	require.True(s.t, apierrors.IsNotFound(err), "a waiting claim must have no interface")
}

func (s *scenario) requireBound(name string) *networkingv1alpha.NetworkInterfaceClaim {
	s.t.Helper()
	claim := s.getClaim(name)
	require.NotNil(s.t, claim.Status.NetworkInterfaceRef, "%s must bind", name)
	bound := conditionOf(claim, networkingv1alpha.NetworkInterfaceClaimBound)
	require.NotNil(s.t, bound)
	require.Equal(s.t, metav1.ConditionTrue, bound.Status)
	return claim
}

func entryOf(s *scenario, name, network string) networkingv1alpha.SwitchPortHoldClaimRef {
	return networkingv1alpha.SwitchPortHoldClaimRef{Namespace: s.namespace, Name: name, Network: network}
}

func TestSwitchPortHoldNamesAreUnambiguous(t *testing.T) {
	name := networkingv1alpha.SwitchPortHoldName("rack-a1-tor-1", "Ethernet1/12")
	require.Regexp(t, `^port-[0-9a-f]{40}$`, name)
	require.Equal(t, name, networkingv1alpha.SwitchPortHoldName("rack-a1-tor-1", "Ethernet1/12"))
	require.NotEqual(t, name, networkingv1alpha.SwitchPortHoldName("rack-a1-tor-1E", "thernet1/12"),
		"moving a character from the port to the switch must name another port")
	require.NotEqual(t, networkingv1alpha.SwitchPortHoldName("a1:", "b"), networkingv1alpha.SwitchPortHoldName("a", "1:b"))
}

func TestPhysicalClaimBindsAfterRecordingItsPort(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(claim)

	bound := s.requireBound("server-data")
	require.ElementsMatch(t, []string{networkInterfaceClaimFinalizer, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer},
		bound.Finalizers)
	allocated := conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimAllocated)
	require.Equal(t, metav1.ConditionTrue, allocated.Status)

	hold := s.requireHold("Ethernet1/12")
	require.Equal(t, s.switchName, hold.Spec.Switch)
	require.Equal(t, "Ethernet1/12", hold.Spec.Port)
	require.Nil(t, hold.Spec.Boot)
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(s, "server-data", "default")}, hold.Spec.NetworkClaims)
	require.Equal(t, &networkingv1alpha.SwitchPortHoldConsumerRef{
		APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: testProject,
	}, hold.Spec.ConsumerRef)

	iface, err := s.getInterface("server-data")
	require.NoError(t, err)
	require.Equal(t, claim.Spec.AttachmentPoint, iface.Spec.AttachmentPoint)
	require.Equal(t, networkingv1alpha.NetworkInterfaceAttachmentModePhysical, iface.Spec.AttachmentMode)

	before := hold.ResourceVersion
	s.reconcile(claim)
	require.Equal(t, before, s.requireHold("Ethernet1/12").ResourceVersion, "recording again must not rewrite the hold")
}

func TestBootAndNetworkClaimsShareAPort(t *testing.T) {
	customer := newScenario(t, true, ipv4Only)
	provider := customer.sibling(providerProject)

	boot := provider.createClaim("install-1-boot", provider.physicalSpec("Ethernet1/12", networkingv1alpha.NetworkInterfacePurposeBoot))
	provider.reconcile(boot)
	data := customer.createClaim("server-data", customer.physicalSpec("Ethernet1/12", ""))
	customer.reconcile(data)

	provider.requireBound("install-1-boot")
	customer.requireBound("server-data")

	hold := customer.requireHold("Ethernet1/12")
	require.Equal(t, entryOf(provider, "install-1-boot", "default"), *hold.Spec.Boot)
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(customer, "server-data", "default")}, hold.Spec.NetworkClaims)
	require.Equal(t, testProject, hold.Spec.ConsumerRef.Name, "the boot claim's project does not hold the port")

	provider.deletePhysicalClaim(provider.getClaim("install-1-boot"))
	require.True(t, provider.claimGone("install-1-boot"))
	hold = customer.requireHold("Ethernet1/12")
	require.Nil(t, hold.Spec.Boot)
	require.Len(t, hold.Spec.NetworkClaims, 1, "the network claim keeps the port once the boot claim is gone")
}

func TestAnotherProjectWaitsForAHeldPort(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	other := s.sibling(otherProject)

	holder := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(holder)
	s.requireBound("server-data")

	allocations := s.ipam.createdAnywhere()
	waiting := other.createClaim("their-data", other.physicalSpec("Ethernet1/12", ""))
	other.reconcile(waiting)
	other.reconcile(waiting)

	other.requireWaiting("their-data", testProject, "server-data", s.namespace)
	require.Equal(t, allocations, s.ipam.createdAnywhere(), "a waiting claim allocates nothing")
	require.Empty(t, s.ipam.created()[otherProject])
	hold := s.requireHold("Ethernet1/12")
	require.Equal(t, testProject, hold.Spec.ConsumerRef.Name)
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(s, "server-data", "default")}, hold.Spec.NetworkClaims)

	// Deleting the waiting claim needs no realizer: the hold never recorded it.
	require.NoError(t, s.client.Delete(s.ctx, waiting))
	other.reconcile(waiting)
	require.True(t, other.claimGone("their-data"))
	require.Equal(t, hold.ResourceVersion, s.requireHold("Ethernet1/12").ResourceVersion)

	s.deletePhysicalClaim(s.getClaim("server-data"))
	_, err := s.getHold("Ethernet1/12")
	require.True(t, apierrors.IsNotFound(err), "a hold that records nothing is deleted")
	next := other.createClaim("their-data-2", other.physicalSpec("Ethernet1/12", ""))
	other.reconcile(next)
	other.requireBound("their-data-2")
	require.Equal(t, otherProject, s.requireHold("Ethernet1/12").Spec.ConsumerRef.Name)
}

func TestSameProjectSharesAPortOnDifferentNetworks(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	second := s.sibling(testProject)

	first := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(first)
	other := second.createClaim("server-storage", second.physicalSpec("Ethernet1/12", ""))
	second.reconcile(other)

	s.requireBound("server-data")
	second.requireBound("server-storage")
	require.Len(t, s.requireHold("Ethernet1/12").Spec.NetworkClaims, 2)
}

func TestAPortCarriesANetworkOnce(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	first := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(first)
	second := s.createClaim("server-data-again", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(second)

	s.requireBound("server-data")
	s.requireWaiting("server-data-again", testProject, s.namespace)
	require.Len(t, s.requireHold("Ethernet1/12").Spec.NetworkClaims, 1)
}

func TestASecondBootClaimWaits(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	first := s.createClaim("install-1-boot", s.physicalSpec("Ethernet1/12", networkingv1alpha.NetworkInterfacePurposeBoot))
	s.reconcile(first)
	second := s.createClaim("wipe-1-boot", s.physicalSpec("Ethernet1/12", networkingv1alpha.NetworkInterfacePurposeBoot))
	s.reconcile(second)

	s.requireBound("install-1-boot")
	s.requireWaiting("wipe-1-boot", "install-1-boot")
	require.Equal(t, "install-1-boot", s.requireHold("Ethernet1/12").Spec.Boot.Name)

	s.deletePhysicalClaim(s.getClaim("install-1-boot"))
	s.reconcile(second)
	s.requireBound("wipe-1-boot")
}

func TestAHashCollisionIsRefused(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	collided := &networkingv1alpha.SwitchPortHold{}
	collided.Name = networkingv1alpha.SwitchPortHoldName(s.switchName, "Ethernet1/12")
	collided.Spec.Switch = s.switchName
	collided.Spec.Port = "Ethernet1/99"
	require.NoError(t, s.client.Create(s.ctx, collided))

	claim := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(claim)

	s.requireWaiting("server-data")
	require.Zero(t, s.ipam.createdAnywhere())
	hold := s.requireHold("Ethernet1/12")
	require.Equal(t, collided.ResourceVersion, hold.ResourceVersion, "another port's hold must be left alone")

	require.NoError(t, s.client.Delete(s.ctx, claim))
	s.reconcile(claim)
	require.True(t, s.claimGone("server-data"), "a claim no hold recorded is released by NSO alone")
	require.Equal(t, collided.ResourceVersion, s.requireHold("Ethernet1/12").ResourceVersion)
}

func TestReleaseWaitsForTheRealizerToDetach(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(claim)
	s.requireBound("server-data")
	names := physicalIPClaimNames("server-data")

	require.NoError(t, s.client.Delete(s.ctx, claim))
	s.reconcile(claim)
	s.requireAwaitingDetach("server-data")
	waiting := s.getClaim("server-data").ResourceVersion
	s.reconcile(claim)
	s.requireAwaitingDetach("server-data")
	require.Equal(t, waiting, s.getClaim("server-data").ResourceVersion, "a pass that finds nothing new must not rewrite the claim")

	s.ipClaim(names[0])
	_, err := s.getInterface("server-data")
	require.NoError(t, err, "the interface stays until the port is detached")
	require.Empty(t, s.ipam.deleted()[testProject], "no address is released while the detach finalizer is present")
	require.Len(t, s.requireHold("Ethernet1/12").Spec.NetworkClaims, 1)

	s.detach("server-data")
	s.reconcile(claim)
	require.True(t, s.claimGone("server-data"))
	require.Zero(t, s.ipam.storedAnywhere(t))
	_, err = s.getInterface("server-data")
	require.True(t, apierrors.IsNotFound(err))
	_, err = s.getHold("Ethernet1/12")
	require.True(t, apierrors.IsNotFound(err), "the last claim to leave deletes the hold")
}

func TestReleaseStepsThroughWhatItWaitsOn(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim, address := boundPhysicalClaim(s)
	names := physicalIPClaimNames(physicalClaimName)
	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	lingering := &ipamv1alpha1.IPAllocation{}
	lingering.Namespace = testProjectNS
	lingering.Name = "lingering"
	lingering.Spec.ClaimRef = &ipamv1alpha1.LocalRef{Name: names[0]}
	require.NoError(t, ipamClient.Create(s.ctx, lingering))

	s.deletePhysicalClaim(claim)
	s.requireReleaseUnverified(address)
	require.Len(t, s.requireHold("Ethernet1/12").Spec.NetworkClaims, 1,
		"the claim leaves its hold only once its release is verified")
}

func TestAnInterfaceDeletedBeforeDetachKeepsItsAddresses(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim, _ := boundPhysicalClaim(s)
	names := physicalIPClaimNames(physicalClaimName)

	require.NoError(t, s.client.Delete(s.ctx, claim))
	s.reconcile(claim)
	s.requireAwaitingDetach(physicalClaimName)

	iface, err := s.getInterface(physicalClaimName)
	require.NoError(t, err)
	require.NoError(t, s.client.Delete(s.ctx, iface))
	s.reconcileInterface(physicalClaimName)
	s.ipClaim(names[0])
	require.Empty(t, s.ipam.deleted()[testProject], "the port may still carry the address")

	s.detach(physicalClaimName)
	s.reconcile(claim)
	require.True(t, s.claimGone(physicalClaimName))
	require.Zero(t, s.ipam.storedAnywhere(t), "the claim's own release frees what its interface held")
}

// A crash after recording and before the interface exists leaves a record and
// no interface. The realizer may have acted on the record, so it alone may
// detach.
func TestARecordWithoutAnInterfaceStillWaitsForTheRealizer(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim := s.createPhysicalClaim("server-data", "Ethernet1/12", "")
	recorded, err := recordOnHold(s.ctx, s.client, s.client, claim, testProject)
	require.NoError(t, err)
	require.True(t, recorded)

	require.NoError(t, s.client.Delete(s.ctx, claim))
	s.reconcile(claim)
	s.requireAwaitingDetach("server-data")

	s.detach("server-data")
	s.reconcile(claim)
	require.True(t, s.claimGone("server-data"))
	_, err = s.getHold("Ethernet1/12")
	require.True(t, apierrors.IsNotFound(err))
}

// A crash after allocating and before recording the interface leaves IPAM
// claims that only the claim's name can find.
func TestAddressesAllocatedBeforeTheInterfaceAreReleasedAndVerified(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim := s.createPhysicalClaim("server-storage", "Ethernet1/12", "")
	recorded, err := recordOnHold(s.ctx, s.client, s.client, claim, testProject)
	require.NoError(t, err)
	require.True(t, recorded)

	names := physicalIPClaimNames("server-storage")
	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	require.NoError(t, ipamClient.Create(s.ctx, physicalIPClaim(names[0], testLocationName)))
	lingering := &ipamv1alpha1.IPAllocation{}
	lingering.Namespace = testProjectNS
	lingering.Name = "lingering"
	lingering.Spec.ClaimRef = &ipamv1alpha1.LocalRef{Name: names[0]}
	require.NoError(t, ipamClient.Create(s.ctx, lingering))
	require.Nil(t, s.getClaim("server-storage").Status.NetworkInterfaceRef)

	s.deletePhysicalClaim(claim)
	require.False(t, s.claimGone("server-storage"))
	ready := conditionOf(s.getClaim("server-storage"), networkingv1alpha.NetworkInterfaceClaimReady)
	require.Equal(t, networkingv1alpha.NetworkInterfaceClaimReasonReleaseUnverified, ready.Reason)
	require.Equal(t, []string{names[0]}, s.ipam.deleted()[testProject], "the IPAM claim is freed by the claim's name")

	require.NoError(t, ipamClient.Get(s.ctx, client.ObjectKeyFromObject(lingering), lingering))
	lingering.Spec.ClaimRef = nil
	require.NoError(t, ipamClient.Update(s.ctx, lingering))
	s.reconcile(claim)
	require.True(t, s.claimGone("server-storage"))
}

func TestAStaleEntryIsDroppedOnTheNextWrite(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	other := s.sibling(otherProject)

	gone := s.createPhysicalClaim("server-data", "Ethernet1/12", "")
	recorded, err := recordOnHold(s.ctx, s.client, s.client, gone, testProject)
	require.NoError(t, err)
	require.True(t, recorded)
	gone.Finalizers = nil
	require.NoError(t, s.client.Update(s.ctx, gone))
	require.NoError(t, s.client.Delete(s.ctx, gone))
	require.True(t, s.claimGone("server-data"))

	claim := other.createClaim("their-data", other.physicalSpec("Ethernet1/12", ""))
	other.reconcile(claim)
	other.requireBound("their-data")
	hold := s.requireHold("Ethernet1/12")
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(other, "their-data", "default")}, hold.Spec.NetworkClaims)
	require.Equal(t, otherProject, hold.Spec.ConsumerRef.Name)
}

func TestAnEntryLeftByAnEarlierClaimOfTheNameIsReplaced(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim := s.createPhysicalClaim("server-data", "Ethernet1/12", "")

	hold := &networkingv1alpha.SwitchPortHold{}
	hold.Name = networkingv1alpha.SwitchPortHoldName(s.switchName, "Ethernet1/12")
	hold.Spec.Switch = s.switchName
	hold.Spec.Port = "Ethernet1/12"
	hold.Spec.ConsumerRef = &networkingv1alpha.SwitchPortHoldConsumerRef{APIGroup: miloProjectAPIGroup, Kind: miloProjectKind, Name: testProject}
	hold.Spec.NetworkClaims = []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(s, "server-data", "retired")}
	require.NoError(t, s.client.Create(s.ctx, hold))

	recorded, err := recordOnHold(s.ctx, s.client, s.client, claim, testProject)
	require.NoError(t, err)
	require.True(t, recorded)
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(s, "server-data", "default")},
		s.requireHold("Ethernet1/12").Spec.NetworkClaims)
}

// recordUntilDecided retries the way the reconciler's requeue does, so a
// writer that lost every attempt still reaches an answer.
func recordUntilDecided(s *scenario, claim *networkingv1alpha.NetworkInterfaceClaim, project string) (bool, error) {
	for {
		recorded, err := recordOnHold(s.ctx, s.client, s.client, claim, project)
		if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) || apierrors.IsNotFound(err) {
			continue
		}
		return recorded, err
	}
}

func TestTwoProjectsRacingForOnePortRecordOne(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	other := s.sibling(otherProject)
	provider := s.sibling(providerProject)

	const iterations = 30
	for i := range iterations {
		port := fmt.Sprintf("Ethernet1/%d", i+1)
		// Half the ports start free, so both writers create; half start held by
		// a boot claim, so both update.
		if i%2 == 1 {
			boot := provider.createPhysicalClaim(fmt.Sprintf("boot-%d", i), port,
				networkingv1alpha.NetworkInterfacePurposeBoot)
			recorded, err := recordOnHold(s.ctx, s.client, s.client, boot, providerProject)
			require.NoError(t, err)
			require.True(t, recorded)
		}

		claims := []*networkingv1alpha.NetworkInterfaceClaim{
			s.createPhysicalClaim(fmt.Sprintf("ours-%d", i), port, ""),
			other.createPhysicalClaim(fmt.Sprintf("theirs-%d", i), port, ""),
		}
		projects := []string{testProject, otherProject}
		recorded := make([]bool, len(claims))
		errs := make([]error, len(claims))

		start := make(chan struct{})
		var wg sync.WaitGroup
		for j := range claims {
			wg.Go(func() {
				<-start
				recorded[j], errs[j] = recordUntilDecided(s, claims[j], projects[j])
			})
		}
		close(start)
		wg.Wait()
		require.NoError(t, errors.Join(errs...))

		require.NotEqual(t, recorded[0], recorded[1], "exactly one project may hold port %s", port)
		winner := 0
		if recorded[1] {
			winner = 1
		}
		hold := s.requireHold(port)
		require.Equal(t, projects[winner], hold.Spec.ConsumerRef.Name)
		require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{{
			Namespace: claims[winner].Namespace, Name: claims[winner].Name, Network: "default",
		}}, hold.Spec.NetworkClaims)
	}
}

func TestARecordRacingTheLastLeaveNeverLosesTheHold(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	second := s.sibling(testProject)

	const iterations = 40
	for i := range iterations {
		port := fmt.Sprintf("Ethernet2/%d", i+1)
		leaving := s.createPhysicalClaim(fmt.Sprintf("leaving-%d", i), port, "")
		recorded, err := recordOnHold(s.ctx, s.client, s.client, leaving, testProject)
		require.NoError(t, err)
		require.True(t, recorded)
		joining := second.createPhysicalClaim(fmt.Sprintf("joining-%d", i), port, "")

		var joined bool
		var joinErr, leaveErr error
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			joined, joinErr = recordUntilDecided(s, joining, testProject)
		})
		wg.Go(func() {
			<-start
			for {
				leaveErr = leaveHold(s.ctx, s.client, s.client, leaving)
				if !apierrors.IsConflict(leaveErr) {
					return
				}
			}
		})
		close(start)
		wg.Wait()
		require.NoError(t, joinErr)
		require.NoError(t, leaveErr)
		require.True(t, joined)

		hold, err := s.getHold(port)
		require.NoError(t, err, "a hold must never be deleted under a recorded claim (port %s)", port)
		require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(second, joining.Name, "default")},
			hold.Spec.NetworkClaims)
	}
}

// interleavingClient runs another writer once, between a hold's read and the
// write that follows it, so an interleaving a race only sometimes produces
// happens every time.
type interleavingClient struct {
	client.Client
	beforeUpdate func()
	beforeDelete func()
}

func (c *interleavingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if _, ok := obj.(*networkingv1alpha.SwitchPortHold); ok && c.beforeUpdate != nil {
		hook := c.beforeUpdate
		c.beforeUpdate = nil
		hook()
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *interleavingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, ok := obj.(*networkingv1alpha.SwitchPortHold); ok && c.beforeDelete != nil {
		hook := c.beforeDelete
		c.beforeDelete = nil
		hook()
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestARecordLandingBeforeTheLastLeaveDeletesKeepsTheHold(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	second := s.sibling(testProject)
	leaving := s.createPhysicalClaim("leaving", "Ethernet1/12", "")
	recorded, err := recordOnHold(s.ctx, s.client, s.client, leaving, testProject)
	require.NoError(t, err)
	require.True(t, recorded)
	joining := second.createPhysicalClaim("joining", "Ethernet1/12", "")

	leaver := &interleavingClient{Client: s.client, beforeDelete: func() {
		joined, err := recordOnHold(s.ctx, s.client, s.client, joining, testProject)
		require.NoError(t, err)
		require.True(t, joined)
	}}
	require.NoError(t, leaveHold(s.ctx, leaver, s.client, leaving))
	require.Nil(t, leaver.beforeDelete, "the record must land between the leave's read and its delete")

	hold, err := s.getHold("Ethernet1/12")
	require.NoError(t, err, "a hold must never be deleted under a recorded claim")
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(second, "joining", "default")}, hold.Spec.NetworkClaims)
}

func TestALeaveLandingBeforeARecordUpdatesLeavesANewHold(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	second := s.sibling(testProject)
	leaving := s.createPhysicalClaim("leaving", "Ethernet1/12", "")
	recorded, err := recordOnHold(s.ctx, s.client, s.client, leaving, testProject)
	require.NoError(t, err)
	require.True(t, recorded)
	joining := second.createPhysicalClaim("joining", "Ethernet1/12", "")

	joiner := &interleavingClient{Client: s.client, beforeUpdate: func() {
		require.NoError(t, leaveHold(s.ctx, s.client, s.client, leaving))
	}}
	joined, err := recordOnHold(s.ctx, joiner, s.client, joining, testProject)
	require.NoError(t, err)
	require.True(t, joined)
	require.Nil(t, joiner.beforeUpdate, "the leave must land between the record's read and its update")

	hold := s.requireHold("Ethernet1/12")
	require.Equal(t, []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(second, "joining", "default")}, hold.Spec.NetworkClaims)
	require.Equal(t, testProject, hold.Spec.ConsumerRef.Name)
}

func TestTheLastNetworkClaimLeavingClearsTheConsumer(t *testing.T) {
	customer := newScenario(t, true, ipv4Only)
	provider := customer.sibling(providerProject)

	boot := provider.createClaim("install-1-boot", provider.physicalSpec("Ethernet1/12", networkingv1alpha.NetworkInterfacePurposeBoot))
	provider.reconcile(boot)
	data := customer.createClaim("server-data", customer.physicalSpec("Ethernet1/12", ""))
	customer.reconcile(data)
	require.NotNil(t, customer.requireHold("Ethernet1/12").Spec.ConsumerRef)

	customer.deletePhysicalClaim(customer.getClaim("server-data"))
	require.True(t, customer.claimGone("server-data"))
	hold := customer.requireHold("Ethernet1/12")
	require.Equal(t, "install-1-boot", hold.Spec.Boot.Name)
	require.Empty(t, hold.Spec.NetworkClaims)
	require.Nil(t, hold.Spec.ConsumerRef, "a hold kept only by a boot claim names no project")
}

func TestABootClaimReplacesANetworkEntryLeftUnderItsName(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim := s.createPhysicalClaim("install-1-boot", "Ethernet1/12", networkingv1alpha.NetworkInterfacePurposeBoot)

	hold := &networkingv1alpha.SwitchPortHold{}
	hold.Name = networkingv1alpha.SwitchPortHoldName(s.switchName, "Ethernet1/12")
	hold.Spec.Switch = s.switchName
	hold.Spec.Port = "Ethernet1/12"
	hold.Spec.ConsumerRef = &networkingv1alpha.SwitchPortHoldConsumerRef{APIGroup: miloProjectAPIGroup, Kind: miloProjectKind, Name: testProject}
	hold.Spec.NetworkClaims = []networkingv1alpha.SwitchPortHoldClaimRef{entryOf(s, "install-1-boot", "default")}
	require.NoError(t, s.client.Create(s.ctx, hold))

	recorded, err := recordOnHold(s.ctx, s.client, s.client, claim, testProject)
	require.NoError(t, err)
	require.True(t, recorded)
	stored := s.requireHold("Ethernet1/12")
	require.Equal(t, entryOf(s, "install-1-boot", "default"), *stored.Spec.Boot)
	require.Empty(t, stored.Spec.NetworkClaims)
	require.Nil(t, stored.Spec.ConsumerRef)
}

func TestPruningTheLastNetworkEntryClearsTheConsumer(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	provider := s.sibling(providerProject)

	gone := s.createPhysicalClaim("server-data", "Ethernet1/12", "")
	recorded, err := recordOnHold(s.ctx, s.client, s.client, gone, testProject)
	require.NoError(t, err)
	require.True(t, recorded)
	gone.Finalizers = nil
	require.NoError(t, s.client.Update(s.ctx, gone))
	require.NoError(t, s.client.Delete(s.ctx, gone))
	require.True(t, s.claimGone("server-data"))

	boot := provider.createPhysicalClaim("install-1-boot", "Ethernet1/12", networkingv1alpha.NetworkInterfacePurposeBoot)
	recorded, err = recordOnHold(s.ctx, s.client, s.client, boot, providerProject)
	require.NoError(t, err)
	require.True(t, recorded)

	hold := s.requireHold("Ethernet1/12")
	require.Empty(t, hold.Spec.NetworkClaims)
	require.Nil(t, hold.Spec.ConsumerRef, "the project leaves the port with its last network claim")
}
