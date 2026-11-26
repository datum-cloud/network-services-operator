// SPDX-License-Identifier: AGPL-3.0-only

// Package physicalrealizer is a test realizer that programs a simulated switch.
package physicalrealizer

import (
	"context"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/types"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

type Port struct {
	Switch string
	Name   string
}

// Membership is one network a port carries. A VLAN of zero is untagged.
type Membership struct {
	Network   types.NamespacedName
	VLAN      int32
	Isolation networkingv1alpha.NetworkInterfaceIsolation
}

// Driver changes one switch port at a time. Apply replaces everything a port
// carries in one change, and only Read proves it took effect. Ports lists every
// port that carries anything.
type Driver interface {
	Apply(ctx context.Context, port Port, desired []Membership) error
	Read(ctx context.Context, port Port) ([]Membership, error)
	Ports(ctx context.Context) ([]Port, error)
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
