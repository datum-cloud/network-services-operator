package validation

import (
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func ValidateGateway(gateway *gatewayv1.Gateway, opts GatewayValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	if gateway.Spec.GatewayClassName == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "gatewayClassName"), "gatewayClassName is required"))
	}

	allErrs = append(allErrs, validateListeners(gateway.Spec.Listeners, field.NewPath("spec", "listeners"), opts)...)

	if len(gateway.Spec.Addresses) > 0 {
		allErrs = append(allErrs, field.TooMany(field.NewPath("spec", "addresses"), len(gateway.Spec.Addresses), 0))
	}

	if gateway.Spec.Infrastructure != nil {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "infrastructure"), "infrastructure is not permitted"))
	}

	if gateway.Spec.BackendTLS != nil {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "backendTLS"), "backendTLS is not permitted"))
	}

	return allErrs
}

func validateListeners(listeners []gatewayv1.Listener, fldPath *field.Path, opts GatewayValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, l := range listeners {
		listenerPath := fldPath.Index(i)

		if !opts.PermitListenerHostnames && l.Hostname != nil {
			allErrs = append(allErrs, field.Invalid(listenerPath.Child("hostname"), l.Hostname, "hostnames are not permitted"))
		}

		if !slices.Contains(opts.ValidPortNumbers, int(l.Port)) {
			allErrs = append(allErrs, field.NotSupported(listenerPath.Child("port"), l.Port, opts.ValidPortNumbers.StringSlice()))
		}

		if !slices.Contains(opts.ValidProtocolTypes, l.Protocol) {
			allErrs = append(allErrs, field.NotSupported(listenerPath.Child("protocol"), l.Protocol, opts.ValidProtocolTypes))
		}

		allErrs = append(allErrs, validateGatewayTLSConfig(l.TLS, listenerPath.Child("tls"), opts)...)

		allErrs = append(allErrs, validateAllowedRoutes(l.AllowedRoutes, listenerPath.Child("allowedRoutes"), opts)...)
	}

	return allErrs
}

func validateAllowedRoutes(allowedRoutes *gatewayv1.AllowedRoutes, fldPath *field.Path, opts GatewayValidationOptions) field.ErrorList {
	if allowedRoutes == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if opts.RoutesFromSameNamespaceOnly {
		if allowedRoutes.Namespaces != nil && allowedRoutes.Namespaces.From != nil {
			if *allowedRoutes.Namespaces.From == gatewayv1.NamespacesFromAll {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("namespaces", "from"), allowedRoutes.Namespaces.From, "allowedRoutes.namespaces.from must be set to NamespacesFromAll"))
			}
		}
	}

	if len(allowedRoutes.Kinds) > 0 {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("kinds"), len(allowedRoutes.Kinds), 0))
	}

	return allErrs
}

func validateGatewayTLSConfig(tls *gatewayv1.GatewayTLSConfig, fldPath *field.Path, opts GatewayValidationOptions) field.ErrorList {
	if tls == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if tls.Mode != nil && *tls.Mode != gatewayv1.TLSModeTerminate {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("mode"), tls.Mode, "mode must be set to Terminate"))
	}

	if len(tls.CertificateRefs) > 0 && !opts.PermitCertificateRefs {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("certificateRefs"), len(tls.CertificateRefs), 0))
	}

	if tls.FrontendValidation != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("frontendValidation"), "frontendValidation is not permitted"))
	}

	if len(tls.Options) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("options"), "options are not permitted"))
	}

	return allErrs
}

type GatewayValidationOptions struct {
	RoutesFromSameNamespaceOnly bool
	PermitListenerHostnames     bool
	PermitCertificateRefs       bool
	ValidPortNumbers            validPortNumbers
	ValidProtocolTypes          []gatewayv1.ProtocolType
}

type validPortNumbers []int

func (v validPortNumbers) StringSlice() []string {
	s := make([]string, len(v))
	for i, p := range v {
		s[i] = fmt.Sprint(p)
	}
	return s
}
