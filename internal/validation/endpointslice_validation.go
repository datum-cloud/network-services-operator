package validation

import (
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateEndpointSliceAddresses(
	slice *discoveryv1.EndpointSlice,
	vouched sets.Set[string],
	permittedSuffixes []string,
) field.ErrorList {
	allErrs := field.ErrorList{}
	endpointsPath := field.NewPath("endpoints")
	for i, endpoint := range slice.Endpoints {
		addressesPath := endpointsPath.Index(i).Child("addresses")
		for j, address := range endpoint.Addresses {
			if vouched.Has(address) {
				continue
			}
			allErrs = append(allErrs, validateExternalHost(addressesPath.Index(j), address, permittedSuffixes...)...)
		}
	}
	return allErrs
}
