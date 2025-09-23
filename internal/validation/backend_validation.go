package validation

import (
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateBackend(backend *envoygatewayv1alpha1.Backend) field.ErrorList {
	return nil
}
