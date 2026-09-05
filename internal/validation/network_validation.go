// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"slices"

	"k8s.io/apimachinery/pkg/util/validation/field"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const ipv6RequiredDetail = "the platform addresses workloads over IPv6, so a network that does not carry it can never run one; include IPv6, on its own or alongside IPv4"

// ValidateNetwork refuses a network nothing can ever run on. The edge carries
// IPv6 and address space is drawn from the tenant IPv6 pool, so a network
// without it reaches placement and stops there.
func ValidateNetwork(network *networkingv1alpha.Network) field.ErrorList {
	allErrs := field.ErrorList{}

	fldPath := field.NewPath("spec", "ipFamilies")
	if !networkCarriesIPv6(network) {
		allErrs = append(allErrs, field.Invalid(fldPath, network.Spec.IPFamilies, ipv6RequiredDetail))
	}

	return allErrs
}

// ValidateNetworkUpdate refuses only the change that breaks a network which
// works. A network that already lacked IPv6 is left alone: it is the object
// this validation exists to make visible, and rejecting writes to it would
// stop the operator reporting on it and stop anyone repairing it.
func ValidateNetworkUpdate(newNetwork, oldNetwork *networkingv1alpha.Network) field.ErrorList {
	if !networkCarriesIPv6(oldNetwork) {
		return field.ErrorList{}
	}

	return ValidateNetwork(newNetwork)
}

func networkCarriesIPv6(network *networkingv1alpha.Network) bool {
	return slices.Contains(network.Spec.IPFamilies, networkingv1alpha.IPv6Protocol)
}
