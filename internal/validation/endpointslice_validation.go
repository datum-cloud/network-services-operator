package validation

import (
	"net/netip"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// CanonicalAddress returns the canonical spelling of an IP address so two
// spellings of one address compare equal, and anything that is not an IP
// address unchanged.
func CanonicalAddress(address string) string {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	return parsed.Unmap().String()
}

func ValidateEndpointSliceAddresses(
	slice *discoveryv1.EndpointSlice,
	vouched sets.Set[string],
	permittedSuffixes []string,
) field.ErrorList {
	canonicalVouched := sets.New[string]()
	for address := range vouched {
		canonicalVouched.Insert(CanonicalAddress(address))
	}

	allErrs := field.ErrorList{}
	endpointsPath := field.NewPath("endpoints")
	for i, endpoint := range slice.Endpoints {
		addressesPath := endpointsPath.Index(i).Child("addresses")
		for j, address := range endpoint.Addresses {
			if canonicalVouched.Has(CanonicalAddress(address)) {
				continue
			}
			allErrs = append(allErrs, validateExternalHost(addressesPath.Index(j), address, permittedSuffixes...)...)
		}
	}
	return allErrs
}
