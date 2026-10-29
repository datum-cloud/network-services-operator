// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// effectiveInternetEgressMode is the mode a network runs with: its own, or the
// platform default while it has not been stamped with one.
func effectiveInternetEgressMode(
	network *networkingv1alpha.Network,
	platformDefault networkingv1alpha.NetworkInternetEgressMode,
) networkingv1alpha.NetworkInternetEgressMode {
	if network.Spec.Egress != nil && network.Spec.Egress.Internet != nil && network.Spec.Egress.Internet.Mode != "" {
		return network.Spec.Egress.Internet.Mode
	}
	if platformDefault != "" {
		return platformDefault
	}
	return networkingv1alpha.NetworkInternetEgressEnabled
}

// projectInternetEgress is the egress instruction one location carries for a
// network. It always names a mode, so a cell never has to guess what an
// unstamped network should get.
func projectInternetEgress(
	network *networkingv1alpha.Network,
	platformDefault networkingv1alpha.NetworkInternetEgressMode,
) *networkingv1alpha.NetworkContextEgress {
	return &networkingv1alpha.NetworkContextEgress{
		Internet: &networkingv1alpha.NetworkContextInternetEgress{
			Mode: effectiveInternetEgressMode(network, platformDefault),
		},
	}
}
