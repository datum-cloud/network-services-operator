package validation

import (
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1alpha3 "sigs.k8s.io/gateway-api/apis/v1alpha3"
)

func ValidateBackendTLSPolicy(backend *gatewayv1alpha3.BackendTLSPolicy) field.ErrorList {
	allErrs := field.ErrorList{}

	specPath := field.NewPath("spec")

	for i, targetRef := range backend.Spec.TargetRefs {
		targetRefPath := specPath.Child("targetRefs").Index(i)

		if targetRef.Group != envoygatewayv1alpha1.GroupName {
			allErrs = append(allErrs, field.NotSupported(targetRefPath.Child("group"), targetRef.Group, []string{envoygatewayv1alpha1.GroupName}))
		}

		if targetRef.Kind != envoygatewayv1alpha1.KindBackend {
			allErrs = append(allErrs, field.NotSupported(targetRefPath.Child("kind"), targetRef.Kind, []string{envoygatewayv1alpha1.KindBackend}))
		}
	}

	return allErrs
}
