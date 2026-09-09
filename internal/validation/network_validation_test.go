// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	"github.com/stretchr/testify/require"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func network(families ...networkingv1alpha.IPFamily) *networkingv1alpha.Network {
	n := &networkingv1alpha.Network{}
	n.Name = "net"
	n.Spec.IPAM.Mode = networkingv1alpha.NetworkIPAMModeAuto
	n.Spec.IPFamilies = families
	return n
}

func TestValidateNetwork(t *testing.T) {
	tests := []struct {
		name     string
		families []networkingv1alpha.IPFamily
		rejected bool
	}{
		{name: "IPv6 alone is the supported network", families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}},
		{
			name:     "dual-stack carries IPv6 and is allowed",
			families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol},
		},
		{
			name:     "the order families are listed in does not decide it",
			families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol},
		},
		{
			name:     "IPv4 alone can never run a workload",
			families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
			rejected: true,
		},
		{name: "no families at all carries no IPv6 either", rejected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := ValidateNetwork(network(test.families...))
			if !test.rejected {
				require.Empty(t, errs)
				return
			}

			require.Len(t, errs, 1)
			require.Equal(t, "spec.ipFamilies", errs[0].Field)
			require.Contains(t, errs[0].Detail, "IPv6")
		})
	}
}

// The update rule is the whole reason this is a webhook. A network that already
// lacked IPv6 is the object the change exists to surface, so every write to it
// has to keep landing — otherwise the operator cannot report on it and nobody
// can repair it.
func TestValidateNetworkUpdate(t *testing.T) {
	v6 := []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	v4 := []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	dual := []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol}

	tests := []struct {
		name     string
		old      []networkingv1alpha.IPFamily
		updated  []networkingv1alpha.IPFamily
		rejected bool
	}{
		{name: "an existing IPv4 network stays writable", old: v4, updated: v4},
		{name: "an existing IPv4 network can be repaired", old: v4, updated: v6},
		{name: "a network with no families stays writable", updated: nil},
		{name: "a working network may add IPv4", old: v6, updated: dual},
		{name: "a working network may drop back to IPv6", old: dual, updated: v6},
		{name: "a working network may not lose IPv6", old: v6, updated: v4, rejected: true},
		{name: "a dual-stack network may not lose IPv6", old: dual, updated: v4, rejected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := ValidateNetworkUpdate(network(test.updated...), network(test.old...))
			if !test.rejected {
				require.Empty(t, errs)
				return
			}

			require.Len(t, errs, 1)
			require.Equal(t, "spec.ipFamilies", errs[0].Field)
		})
	}
}
