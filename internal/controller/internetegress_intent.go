// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"slices"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// projectInternetEgress is the egress intent one location is instructed with,
// copied from what the network declares.
//
// Translation runs on the node an instance attaches to, so a location has
// nothing to select and nothing to resolve. The declaration is carried as
// written, and what the location realized is the location's answer to report.
//
// A network declaring nothing projects to nothing, which is not the same as a
// network that reaches nothing: the first carries no instruction and the second
// carries Disabled.
func projectInternetEgress(network *networkingv1alpha.Network) *networkingv1alpha.NetworkContextInternetEgress {
	if network.Spec.Egress == nil || network.Spec.Egress.Internet == nil {
		return nil
	}
	declared := network.Spec.Egress.Internet

	switch declared.Mode {
	case networkingv1alpha.NetworkInternetEgressDisabled:
		return &networkingv1alpha.NetworkContextInternetEgress{
			Mode: networkingv1alpha.NetworkInternetEgressDisabled,
		}
	case networkingv1alpha.NetworkInternetEgressEnabled:
		return &networkingv1alpha.NetworkContextInternetEgress{
			Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
			Reach: slices.Clone(declared.Reach),
		}
	default:
		return nil
	}
}
