package validation

import (
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

func ValidateBackend(backend *envoygatewayv1alpha1.Backend) field.ErrorList {
	allErrs := field.ErrorList{}

	specPath := field.NewPath("spec")

	if ptr.Deref(backend.Spec.Type, envoygatewayv1alpha1.BackendTypeEndpoints) != envoygatewayv1alpha1.BackendTypeEndpoints {
		supportedTypes := []envoygatewayv1alpha1.BackendType{envoygatewayv1alpha1.BackendTypeEndpoints}
		allErrs = append(allErrs, field.NotSupported(specPath.Child("type"), backend.Spec.Type, supportedTypes))
	}

	allErrs = append(allErrs, validateBackendEndpoints(backend.Spec.Endpoints, specPath.Child("endpoints"))...)

	// appProtocols, fallback, and TLS settings are considered safe to leverage as is.

	return allErrs
}

func validateBackendEndpoints(endpoints []envoygatewayv1alpha1.BackendEndpoint, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, endpoint := range endpoints {
		endpointPath := fldPath.Index(i)

		if endpoint.Unix != nil {
			allErrs = append(allErrs, field.Forbidden(endpointPath.Child("unix"), "unix endpoints are not permitted"))
		}
	}

	return allErrs
}
