package validation

import (
	"k8s.io/apimachinery/pkg/util/validation/field"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func ValidateHTTPProxy(route *networkingv1alpha.HTTPProxy) field.ErrorList {

	allErrs := field.ErrorList{}

	// TODO(jreese)
	// - [ ] implement validation, specifically for endpoints.
	// - [ ] leverage existing validation function for rules / etc.
	// - [ ] Prohibit paths in backend endpoints

	return allErrs
}
