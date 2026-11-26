// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	testSwitch = "rack-a1-tor-1"
	testPort   = "Ethernet1/12"
)

func testHold() *networkingv1alpha.SwitchPortHold {
	hold := &networkingv1alpha.SwitchPortHold{}
	hold.Name = networkingv1alpha.SwitchPortHoldName(testSwitch, testPort)
	hold.Spec.Switch = testSwitch
	hold.Spec.Port = testPort
	return hold
}

func physicalClaim(namespace, name, network string, purpose networkingv1alpha.NetworkInterfacePurpose) *networkingv1alpha.NetworkInterfaceClaim {
	claim := &networkingv1alpha.NetworkInterfaceClaim{}
	claim.Namespace = namespace
	claim.Name = name
	claim.Finalizers = []string{networkingv1alpha.NetworkInterfaceClaimDetachFinalizer}
	claim.Spec.Network.Name = network
	claim.Spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModePhysical
	claim.Spec.AttachmentPoint = &networkingv1alpha.NetworkInterfaceAttachmentPoint{
		MACAddress: "00:00:5e:00:53:01", Switch: testSwitch, Port: testPort,
	}
	claim.Spec.Purpose = purpose
	return claim
}

func entryFor(claim *networkingv1alpha.NetworkInterfaceClaim, boot bool) recorded {
	return recorded{
		entry: networkingv1alpha.SwitchPortHoldClaimRef{
			Namespace: claim.Namespace, Name: claim.Name, Network: claim.Spec.Network.Name,
		},
		boot:  boot,
		claim: claim,
	}
}

func network(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}

func TestABootClaimCarriesItsNetworkAloneAndUntagged(t *testing.T) {
	boot := entryFor(physicalClaim("ns-provider-example", "install-1-boot", "boot", networkingv1alpha.NetworkInterfacePurposeBoot), true)
	data := entryFor(physicalClaim("ns-customer-example", "server-data", "app", ""), false)
	storage := entryFor(physicalClaim("ns-customer-example", "server-storage", "storage", ""), false)

	p := planFor(testHold(), []recorded{boot, data, storage})
	require.Equal(t, []Membership{{
		Network: network("ns-provider-example", "boot"), Isolation: networkingv1alpha.NetworkInterfaceIsolationPrivate,
	}}, p.carried())
	require.Len(t, p.networks, 2, "network claims keep their VLANs while the boot claim holds the port")
	require.Zero(t, p.networks[0].membership.VLAN)
	require.NotZero(t, p.networks[1].membership.VLAN)
}

func TestNetworkClaimsAreOneUntaggedAndTheRestTagged(t *testing.T) {
	names := []string{"app", "storage", "backup", "metrics"}
	entries := make([]recorded, 0, len(names))
	for _, name := range names {
		entries = append(entries, entryFor(physicalClaim("ns-customer-example", "server-"+name, name, ""), false))
	}

	carried := planFor(testHold(), entries).carried()
	require.Len(t, carried, 4)
	require.Equal(t, network("ns-customer-example", "app"), carried[0].Network)
	require.Zero(t, carried[0].VLAN, "the first network claim recorded arrives untagged")
	seen := map[int32]bool{}
	for _, m := range carried[1:] {
		require.GreaterOrEqual(t, m.VLAN, int32(firstSimulatedVLAN))
		require.LessOrEqual(t, m.VLAN, int32(maxVLAN))
		require.False(t, seen[m.VLAN], "VLAN %d is used twice", m.VLAN)
		seen[m.VLAN] = true
		require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationShared, m.Isolation)
	}
	require.NoError(t, validMemberships(carried))
	require.Equal(t, carried, planFor(testHold(), entries).carried(), "the VLANs are deterministic")
}

func TestSimulatedVLANsProbePastACollision(t *testing.T) {
	used := map[int32]bool{}
	first := simulatedVLAN(network("ns-customer-example", "app"), used)
	second := simulatedVLAN(network("ns-customer-example", "app"), used)
	require.NotEqual(t, first, second)
	require.Equal(t, first, simulatedVLAN(network("ns-customer-example", "app"), map[int32]bool{}))

	full := map[int32]bool{}
	for vlan := int32(firstSimulatedVLAN); vlan < maxVLAN; vlan++ {
		full[vlan] = true
	}
	require.Equal(t, int32(maxVLAN), simulatedVLAN(network("ns-customer-example", "app"), full))
}

func TestIsolationFollowsPurposeUnlessSet(t *testing.T) {
	boot := entryFor(physicalClaim("ns-provider-example", "install-1-boot", "boot", networkingv1alpha.NetworkInterfacePurposeBoot), true)
	require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationPrivate, boot.isolation())
	boot.claim.Spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationShared
	require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationShared, boot.isolation())

	data := entryFor(physicalClaim("ns-customer-example", "server-data", "app", ""), false)
	require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationShared, data.isolation())
	data.claim.Spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
	require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationPrivate, data.isolation())
}

func TestOnlyCountedClaimsAreCarried(t *testing.T) {
	deleting := physicalClaim("ns-customer-example", "server-data", "app", "")
	deleting.DeletionTimestamp = ptrTo(metav1.Now())
	noFinalizer := physicalClaim("ns-customer-example", "server-data", "app", "")
	noFinalizer.Finalizers = nil
	otherPort := physicalClaim("ns-customer-example", "server-data", "app", "")
	otherPort.Spec.AttachmentPoint.Port = "Ethernet1/13"
	otherSwitch := physicalClaim("ns-customer-example", "server-data", "app", "")
	otherSwitch.Spec.AttachmentPoint.Switch = "rack-a1-tor-2"
	renamed := entryFor(physicalClaim("ns-customer-example", "server-data", "app", ""), false)
	renamed.entry.Network = "retired"
	notPhysical := physicalClaim("ns-customer-example", "server-data", "app", "")
	notPhysical.Spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeNetns

	for name, entry := range map[string]recorded{
		"being deleted":                    entryFor(deleting, false),
		"without the detach finalizer":     entryFor(noFinalizer, false),
		"on another port":                  entryFor(otherPort, false),
		"on another switch":                entryFor(otherSwitch, false),
		"naming another network":           renamed,
		"in another mode":                  entryFor(notPhysical, false),
		"gone":                             {entry: renamed.entry},
		"a boot claim in a network slot":   entryFor(physicalClaim("ns-customer-example", "server-data", "app", networkingv1alpha.NetworkInterfacePurposeBoot), false),
		"a network claim in the boot slot": entryFor(physicalClaim("ns-customer-example", "server-data", "app", ""), true),
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, entry.counts(testHold()))
			require.Empty(t, planFor(testHold(), []recorded{entry}).carried())
		})
	}
}

func TestADeletedClaimIsReleasingUntilItsNetworksAreOff(t *testing.T) {
	claim := physicalClaim("ns-customer-example", "server-data", "app", "")
	claim.DeletionTimestamp = ptrTo(metav1.Now())
	entry := entryFor(claim, false)
	entry.entry.Network = "retired"
	require.True(t, entry.releasing(testHold()))

	require.True(t, entry.carriedOn([]Membership{{Network: network("ns-customer-example", "retired")}}),
		"the network the hold recorded may still be on the port")
	require.True(t, entry.carriedOn([]Membership{{Network: network("ns-customer-example", "app")}}),
		"so may the network the claim names")
	require.False(t, entry.carriedOn([]Membership{{Network: network("ns-other-example", "app")}}))

	claim.Finalizers = nil
	require.False(t, entry.releasing(testHold()), "a claim without the detach finalizer waits on nothing here")
}

func TestAChangeTouchesClaimsWhoseNetworkMoves(t *testing.T) {
	data := entryFor(physicalClaim("ns-customer-example", "server-data", "app", ""), false)
	untagged := []Membership{{Network: network("ns-customer-example", "app")}}
	tagged := []Membership{{Network: network("ns-customer-example", "app"), VLAN: 100}}
	other := []Membership{{Network: network("ns-customer-example", "storage")}}

	require.True(t, data.changes(nil, untagged))
	require.True(t, data.changes(untagged, nil))
	require.True(t, data.changes(untagged, tagged))
	require.False(t, data.changes(untagged, append(slices.Clone(untagged), other[0])))
	require.False(t, data.changes(nil, other))
}

func TestTheSimulatedSwitchRecordsAndFails(t *testing.T) {
	ctx := context.Background()
	sw := NewSimulatedSwitch()
	port := Port{Switch: testSwitch, Name: testPort}
	app := Membership{Network: network("ns-customer-example", "app")}
	storage := Membership{Network: network("ns-customer-example", "storage"), VLAN: 100}

	require.NoError(t, sw.Apply(ctx, port, []Membership{app}))
	sw.FailApplies(1)
	require.ErrorIs(t, sw.Apply(ctx, port, nil), ErrSwitchUnreachable)
	require.Equal(t, []Membership{app}, sw.Carried(port), "a failed change leaves the port alone")

	sw.PartlyApply(1)
	require.NoError(t, sw.Apply(ctx, port, []Membership{storage}))
	carried, err := sw.Read(ctx, port)
	require.NoError(t, err)
	require.Empty(t, carried, "a partial change takes networks off and adds none")

	sw.FailReads(true)
	_, err = sw.Read(ctx, port)
	require.ErrorIs(t, err, ErrSwitchUnreachable)
	sw.FailReads(false)

	require.Error(t, sw.Apply(ctx, port, []Membership{app, {Network: network("ns-customer-example", "storage")}}),
		"two untagged networks are refused")
	require.Error(t, sw.Apply(ctx, port, []Membership{app, app}))
	require.Error(t, sw.Apply(ctx, port, []Membership{{Network: app.Network, VLAN: maxVLAN + 1}}))

	require.Equal(t, []Change{{Port: port, Carried: []Membership{app}}, {Port: port, Carried: []Membership{}}}, sw.History())
}

func TestClaimsAndInterfacesWakeTheirPortsHold(t *testing.T) {
	claim := physicalClaim("ns-customer-example", "server-data", "app", "")
	want := []string{networkingv1alpha.SwitchPortHoldName(testSwitch, testPort)}
	names := func(requests []ctrl.Request) []string {
		out := make([]string, 0, len(requests))
		for _, request := range requests {
			out = append(out, request.Name)
		}
		return out
	}
	require.Equal(t, want, names(holdForClaim(context.Background(), claim)))

	iface := &networkingv1alpha.NetworkInterface{}
	iface.Spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModePhysical
	iface.Spec.AttachmentPoint = claim.Spec.AttachmentPoint
	require.Equal(t, want, names(holdForInterface(context.Background(), iface)))

	claim.Spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeNetns
	require.Empty(t, holdForClaim(context.Background(), claim))
	require.Empty(t, holdForInterface(context.Background(), &networkingv1alpha.NetworkInterface{}))
}

func TestSetupNeedsADriver(t *testing.T) {
	require.Error(t, (&Reconciler{}).SetupWithManager(nil))
}

func ptrTo[T any](v T) *T { return &v }
