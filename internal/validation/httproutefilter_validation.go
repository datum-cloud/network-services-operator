package validation

import (
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type HTTPRouteFilterValidationOptions struct {
	// MaxInlineBodySize is the maximum allowed size for an inline body in a direct response filter.
	MaxInlineBodySize int
}

func ValidateHTTPRouteFilter(httpRouteFilter *envoygatewayv1alpha1.HTTPRouteFilter, opts HTTPRouteFilterValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	specPath := field.NewPath("spec")

	if directResponse := httpRouteFilter.Spec.DirectResponse; directResponse != nil &&
		directResponse.Body != nil &&
		directResponse.Body.Inline != nil {
		if len(*directResponse.Body.Inline) > opts.MaxInlineBodySize {
			allErrs = append(allErrs, field.TooLong(specPath.Child("directResponse").Child("body").Child("inline"), len(*directResponse.Body.Inline), opts.MaxInlineBodySize))
		}
	}

	return allErrs
}
