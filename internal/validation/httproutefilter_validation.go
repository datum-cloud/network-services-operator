package validation

import (
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateHTTPRouteFilter(httpRouteFilter *envoygatewayv1alpha1.HTTPRouteFilter) field.ErrorList {
	return nil
}
