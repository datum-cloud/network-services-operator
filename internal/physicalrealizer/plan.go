// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"hash/fnv"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	firstSimulatedVLAN = 2
	simulatedVLANs     = maxVLAN - firstSimulatedVLAN + 1
)

// recorded is one entry of a hold, and the claim it names if one exists.
type recorded struct {
	entry networkingv1alpha.SwitchPortHoldClaimRef
	boot  bool
	claim *networkingv1alpha.NetworkInterfaceClaim
}

func (r recorded) network() types.NamespacedName {
	return types.NamespacedName{Namespace: r.entry.Namespace, Name: r.entry.Network}
}

// networks are every network the port may carry for this entry: the one the
// hold recorded, and the one a claim of the entry's name names now.
func (r recorded) networks() []types.NamespacedName {
	networks := []types.NamespacedName{r.network()}
	if r.claim != nil && r.claim.Spec.Network.Name != r.entry.Network {
		networks = append(networks, types.NamespacedName{Namespace: r.entry.Namespace, Name: r.claim.Spec.Network.Name})
	}
	return networks
}

func (r recorded) onPort(hold *networkingv1alpha.SwitchPortHold) bool {
	claim := r.claim
	return claim != nil &&
		claim.Spec.AttachmentMode == networkingv1alpha.NetworkInterfaceAttachmentModePhysical &&
		claim.Spec.AttachmentPoint != nil &&
		claim.Spec.AttachmentPoint.Switch == hold.Spec.Switch &&
		claim.Spec.AttachmentPoint.Port == hold.Spec.Port &&
		controllerutil.ContainsFinalizer(claim, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer)
}

// counts reports whether the port may carry the entry's network: the claim is
// recorded as it is, still carries the detach finalizer, and is not being
// deleted.
func (r recorded) counts(hold *networkingv1alpha.SwitchPortHold) bool {
	return r.onPort(hold) &&
		r.claim.DeletionTimestamp.IsZero() &&
		r.claim.Spec.Network.Name == r.entry.Network &&
		(r.claim.Spec.Purpose == networkingv1alpha.NetworkInterfacePurposeBoot) == r.boot
}

// releasing reports whether the claim waits on the realizer to take its
// network off the port.
func (r recorded) releasing(hold *networkingv1alpha.SwitchPortHold) bool {
	return r.onPort(hold) && !r.claim.DeletionTimestamp.IsZero()
}

func (r recorded) isolation() networkingv1alpha.NetworkInterfaceIsolation {
	if r.claim.Spec.Isolation != "" {
		return r.claim.Spec.Isolation
	}
	if r.boot {
		return networkingv1alpha.NetworkInterfaceIsolationPrivate
	}
	return networkingv1alpha.NetworkInterfaceIsolationShared
}

// plan is what a port carries, worked out from its hold.
type plan struct {
	boot *recorded

	// networks are the counted network claims and what each would arrive on.
	// They keep their VLANs while a boot claim holds the port, so a server
	// being installed can be configured for them.
	networks []assigned
}

type assigned struct {
	recorded
	membership Membership
}

func planFor(hold *networkingv1alpha.SwitchPortHold, entries []recorded) plan {
	var p plan
	used := map[int32]bool{}
	for i := range entries {
		entry := entries[i]
		if !entry.counts(hold) {
			continue
		}
		if entry.boot {
			p.boot = &entry
			continue
		}
		membership := Membership{Network: entry.network(), Isolation: entry.isolation()}
		if len(p.networks) > 0 {
			membership.VLAN = simulatedVLAN(entry.network(), used)
		}
		p.networks = append(p.networks, assigned{recorded: entry, membership: membership})
	}
	return p
}

func (p plan) carried() []Membership {
	if p.boot != nil {
		return []Membership{{Network: p.boot.network(), Isolation: p.boot.isolation()}}
	}
	if len(p.networks) == 0 {
		return nil
	}
	carried := make([]Membership, 0, len(p.networks))
	for _, network := range p.networks {
		carried = append(carried, network.membership)
	}
	return carried
}

func simulatedVLAN(network types.NamespacedName, used map[int32]bool) int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(network.String()))
	offset := hash.Sum32() % simulatedVLANs
	for range simulatedVLANs {
		vlan := firstSimulatedVLAN + int32(offset)
		if !used[vlan] {
			used[vlan] = true
			return vlan
		}
		offset = (offset + 1) % simulatedVLANs
	}
	return 0
}

// changes reports whether moving the port from current to desired adds,
// removes or alters any network the entry may be carried on.
func (r recorded) changes(current, desired []Membership) bool {
	for _, network := range r.networks() {
		before, wasCarried := membershipOf(current, network)
		after, isCarried := membershipOf(desired, network)
		if wasCarried != isCarried || before != after {
			return true
		}
	}
	return false
}

func (r recorded) carriedOn(carried []Membership) bool {
	for _, network := range r.networks() {
		if _, ok := membershipOf(carried, network); ok {
			return true
		}
	}
	return false
}
