package validation

import (
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateSecurityPolicy(securityPolicy *envoygatewayv1alpha1.SecurityPolicy) field.ErrorList {
	return nil
}
