// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func network(families ...networkingv1alpha.IPFamily) *networkingv1alpha.Network {
	n := &networkingv1alpha.Network{}
	n.Name = "net"
	n.Spec.IPAM.Mode = networkingv1alpha.NetworkIPAMModeAuto
	n.Spec.IPFamilies = families
	return n
}

func withIPv4Range(n *networkingv1alpha.Network) *networkingv1alpha.Network {
	n.Spec.IPAM.IPV4Range = ptr.To("10.128.0.0/9")
	return n
}

var (
	v6   = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	v4   = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	dual = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol}
)

func TestValidateNetwork(t *testing.T) {
	tests := []struct {
		name      string
		network   *networkingv1alpha.Network
		wantField string
		wantText  string
	}{
		{name: "IPv6 alone is the supported network", network: network(v6...)},
		{
			name:      "dual-stack carries IPv4",
			network:   network(dual...),
			wantField: "spec.ipFamilies",
			wantText:  "IPv4 is not supported",
		},
		{
			name:      "the order families are listed in does not decide it",
			network:   network(networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol),
			wantField: "spec.ipFamilies",
			wantText:  "IPv4 is not supported",
		},
		{
			name:      "IPv4 alone",
			network:   network(v4...),
			wantField: "spec.ipFamilies",
			wantText:  "IPv4 is not supported",
		},
		{
			name:      "no families at all carries no IPv6",
			network:   network(),
			wantField: "spec.ipFamilies",
			wantText:  "set ipFamilies to [IPv6]",
		},
		{
			name:      "an IPv4 range on an IPv6 network",
			network:   withIPv4Range(network(v6...)),
			wantField: "spec.ipam.ipv4Range",
			wantText:  "remove ipv4Range",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := ValidateNetwork(test.network)
			if test.wantField == "" {
				require.Empty(t, errs)
				return
			}

			require.Len(t, errs, 1)
			require.Equal(t, test.wantField, errs[0].Field)
			require.Contains(t, errs[0].Detail, "IPv6-only")
			require.Contains(t, errs[0].Detail, test.wantText)
		})
	}
}

// Existing networks must stay writable, or finalizer removal and status
// reporting on them are refused and deletion wedges. Only a change that
// introduces IPv4, or takes IPv6 away, is refused.
func TestValidateNetworkUpdate(t *testing.T) {
	tests := []struct {
		name      string
		old       *networkingv1alpha.Network
		updated   *networkingv1alpha.Network
		wantField string
	}{
		{name: "an existing IPv4 network stays writable", old: network(v4...), updated: network(v4...)},
		{name: "an existing dual-stack network stays writable", old: network(dual...), updated: network(dual...)},
		{name: "an existing IPv4 network can be repaired to IPv6", old: network(v4...), updated: network(v6...)},
		{name: "an existing IPv4 network can gain IPv6", old: network(v4...), updated: network(dual...)},
		{name: "a network with no families stays writable", old: network(), updated: network()},
		{name: "a dual-stack network may drop to IPv6", old: network(dual...), updated: network(v6...)},
		{
			name:    "an existing IPv4 range stays writable",
			old:     withIPv4Range(network(v6...)),
			updated: withIPv4Range(network(v6...)),
		},
		{
			name:      "an IPv6 network may not add IPv4",
			old:       network(v6...),
			updated:   network(dual...),
			wantField: "spec.ipFamilies",
		},
		{
			name:      "an IPv6 network may not switch to IPv4",
			old:       network(v6...),
			updated:   network(v4...),
			wantField: "spec.ipFamilies",
		},
		{
			name:      "a network with no families may not add IPv4",
			old:       network(),
			updated:   network(v4...),
			wantField: "spec.ipFamilies",
		},
		{
			name:      "a dual-stack network may not lose IPv6",
			old:       network(dual...),
			updated:   network(v4...),
			wantField: "spec.ipFamilies",
		},
		{
			name:      "an IPv6 network may not add an IPv4 range",
			old:       network(v6...),
			updated:   withIPv4Range(network(v6...)),
			wantField: "spec.ipam.ipv4Range",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := ValidateNetworkUpdate(test.updated, test.old)
			if test.wantField == "" {
				require.Empty(t, errs)
				return
			}

			require.Len(t, errs, 1)
			require.Equal(t, test.wantField, errs[0].Field)
			require.Contains(t, errs[0].Detail, "IPv6-only")
		})
	}
}
