// SPDX-License-Identifier: AGPL-3.0-only

// Package physicalrealizer is a test realizer for physical network interfaces.
// It programs switch ports from SwitchPortHolds through a Driver, and follows
// the same contract a production realizer must.
package physicalrealizer

import (
	"context"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/types"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// Port is one switch port, in the names network operations uses for it.
type Port struct {
	Switch string
	Name   string
}

// Membership is one network a port carries. A VLAN of zero means the network
// arrives untagged.
type Membership struct {
	Network   types.NamespacedName
	VLAN      int32
	Isolation networkingv1alpha.NetworkInterfaceIsolation
}

// Driver changes and reads one switch port at a time. Apply replaces
// everything the port carries in one change. Read reports what the port
// carries now, which is the only proof a change took effect.
type Driver interface {
	Apply(ctx context.Context, port Port, desired []Membership) error
	Read(ctx context.Context, port Port) ([]Membership, error)
}

func sortedMemberships(memberships []Membership) []Membership {
	sorted := slices.Clone(memberships)
	slices.SortFunc(sorted, func(a, b Membership) int {
		return strings.Compare(a.Network.String(), b.Network.String())
	})
	return sorted
}

func sameMemberships(a, b []Membership) bool {
	return slices.Equal(sortedMemberships(a), sortedMemberships(b))
}

func membershipOf(memberships []Membership, network types.NamespacedName) (Membership, bool) {
	index := slices.IndexFunc(memberships, func(m Membership) bool { return m.Network == network })
	if index < 0 {
		return Membership{}, false
	}
	return memberships[index], true
}
