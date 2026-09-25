// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"slices"

	"k8s.io/apimachinery/pkg/util/validation/field"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	ipv6RequiredDetail = "networks are IPv6-only: the platform addresses workloads over IPv6, so a network without it can never run one; set ipFamilies to [IPv6] or omit it to take the default"

	ipv4UnsupportedDetail = "networks are IPv6-only and IPv4 is not supported; set ipFamilies to [IPv6] or omit it to take the default"

	ipv4AddedDetail = "networks are IPv6-only and IPv4 cannot be added to one; leave IPv4 out of ipFamilies"

	ipv4RangeUnsupportedDetail = "networks are IPv6-only and IPv4 is not supported; remove ipv4Range"
)

// ValidateNetwork admits only IPv6 networks. The edge carries IPv6 and address
// space is drawn from the tenant IPv6 pool, so IPv4 is never delivered.
func ValidateNetwork(network *networkingv1alpha.Network) field.ErrorList {
	allErrs := field.ErrorList{}

	familiesPath := field.NewPath("spec", "ipFamilies")
	switch {
	case networkCarriesIPv4(network):
		allErrs = append(allErrs, field.Invalid(familiesPath, network.Spec.IPFamilies, ipv4UnsupportedDetail))
	case !networkCarriesIPv6(network):
		allErrs = append(allErrs, field.Invalid(familiesPath, network.Spec.IPFamilies, ipv6RequiredDetail))
	}

	if network.Spec.IPAM.IPV4Range != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "ipam", "ipv4Range"), *network.Spec.IPAM.IPV4Range, ipv4RangeUnsupportedDetail))
	}

	return allErrs
}

// ValidateNetworkUpdate refuses a change that introduces IPv4 or takes IPv6
// away. IPv4 a network already carried is left alone, so existing networks
// stay writable: the operator keeps reporting on them, users can repair them,
// and finalizers can still be removed.
func ValidateNetworkUpdate(newNetwork, oldNetwork *networkingv1alpha.Network) field.ErrorList {
	allErrs := field.ErrorList{}

	familiesPath := field.NewPath("spec", "ipFamilies")
	if networkCarriesIPv4(newNetwork) && !networkCarriesIPv4(oldNetwork) {
		allErrs = append(allErrs, field.Invalid(familiesPath, newNetwork.Spec.IPFamilies, ipv4AddedDetail))
	} else if networkCarriesIPv6(oldNetwork) && !networkCarriesIPv6(newNetwork) {
		allErrs = append(allErrs, field.Invalid(familiesPath, newNetwork.Spec.IPFamilies, ipv6RequiredDetail))
	}

	if newNetwork.Spec.IPAM.IPV4Range != nil && oldNetwork.Spec.IPAM.IPV4Range == nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "ipam", "ipv4Range"), *newNetwork.Spec.IPAM.IPV4Range, ipv4RangeUnsupportedDetail))
	}

	return allErrs
}

func networkCarriesIPv4(network *networkingv1alpha.Network) bool {
	return slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv4Protocol)
}

func networkCarriesIPv6(network *networkingv1alpha.Network) bool {
	return slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv6Protocol)
}
