package validation

import (
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func ValidateGateway(gateway *gatewayv1.Gateway, opts GatewayValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	if gateway.Spec.GatewayClassName == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "gatewayClassName"), "gatewayClassName is required"))
	}

	allErrs = append(allErrs, validateListeners(gateway, field.NewPath("spec", "listeners"), opts)...)

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

func validateListeners(gateway *gatewayv1.Gateway, fldPath *field.Path, opts GatewayValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, l := range gateway.Spec.Listeners {
		listenerPath := fldPath.Index(i)

		if gatewayutil.IsDefaultListener(l) {
			expectedHostname := opts.GatewayDNSAddressFunc(gateway)

			// Require expected hostname on default listeners if set. Note that these
			// listeners will not have the hostname set at time of creation.
			if l.Hostname != nil && *l.Hostname != gatewayv1.Hostname(expectedHostname) {
				allErrs = append(allErrs, field.NotSupported(listenerPath.Child("hostname"), l.Hostname, []string{expectedHostname}))
			}
		} else if l.Hostname == nil {
			allErrs = append(allErrs, field.Required(listenerPath.Child("hostname"), fmt.Sprintf("must be set to %q or a custom hostname", opts.GatewayDNSAddressFunc(gateway))))
		} else if !opts.SkipHostnameFQDNValidation {
			allErrs = append(allErrs, validation.IsFullyQualifiedDomainName(listenerPath.Child("hostname"), string(*l.Hostname))...)
		}

		if !slices.Contains(opts.ValidPortNumbers, int(l.Port)) {
			allErrs = append(allErrs, field.NotSupported(listenerPath.Child("port"), l.Port, opts.ValidPortNumbers.StringSlice()))
		} else if protocols := opts.ValidProtocolTypes[int(l.Port)]; !slices.Contains(protocols, l.Protocol) {
			allErrs = append(allErrs, field.NotSupported(listenerPath.Child("protocol"), l.Protocol, protocols))
		}

		if l.Protocol == gatewayv1.HTTPSProtocolType {
			allErrs = append(allErrs, validateGatewayTLSConfig(l.TLS, listenerPath.Child("tls"), opts)...)
		}

		allErrs = append(allErrs, validateAllowedRoutes(l.AllowedRoutes, listenerPath.Child("allowedRoutes"))...)
	}

	return allErrs
}

func validateAllowedRoutes(allowedRoutes *gatewayv1.AllowedRoutes, fldPath *field.Path) field.ErrorList {
	if allowedRoutes == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if allowedRoutes.Namespaces != nil && allowedRoutes.Namespaces.From != nil {
		if *allowedRoutes.Namespaces.From != gatewayv1.NamespacesFromSame {
			allErrs = append(allErrs, field.NotSupported(fldPath.Child("namespaces", "from"), allowedRoutes.Namespaces.From, []gatewayv1.FromNamespaces{gatewayv1.NamespacesFromSame}))
		}
	}

	if len(allowedRoutes.Kinds) > 0 {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("kinds"), len(allowedRoutes.Kinds), 0))
	}

	return allErrs
}

func validateGatewayTLSConfig(tls *gatewayv1.GatewayTLSConfig, fldPath *field.Path, opts GatewayValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	optionsFieldPath := fldPath.Child("options")

	if tls == nil || len(tls.Options) == 0 {
		// Require the TLS option for cert issuance until there's support for
		// providing certs.
		allErrs = append(allErrs, field.Required(optionsFieldPath, "must provide TLS options"))
		if tls == nil {
			return allErrs
		}
	}

	if ptr.Deref(tls.Mode, gatewayv1.TLSModeTerminate) != gatewayv1.TLSModeTerminate {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("mode"), tls.Mode, "mode must be set to Terminate"))
	}

	if len(tls.CertificateRefs) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("certificateRefs"), "certificateRefs are not permitted"))
	}

	if tls.FrontendValidation != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("frontendValidation"), "frontendValidation is not permitted"))
	}

	for k, v := range tls.Options {
		optionPath := optionsFieldPath.Key(string(k))

		if optValues, ok := opts.PermittedTLSOptions[string(k)]; !ok {
			allErrs = append(allErrs, field.Forbidden(optionPath, "option is not permitted"))
		} else {
			if len(optValues) > 0 && !slices.Contains(optValues, string(v)) {
				allErrs = append(allErrs, field.NotSupported(optionPath, string(v), optValues))
			}
		}
	}

	return allErrs
}

type GatewayValidationOptions struct {
	ControllerName             gatewayv1.GatewayController
	PermittedTLSOptions        map[string][]string
	ValidPortNumbers           validPortNumbers
	ValidProtocolTypes         map[int][]gatewayv1.ProtocolType
	GatewayDNSAddressFunc      func(gateway *gatewayv1.Gateway) string
	ClusterName                string
	SkipHostnameFQDNValidation bool
}

type validPortNumbers []int

func (v validPortNumbers) StringSlice() []string {
	s := make([]string, len(v))
	for i, p := range v {
		s[i] = fmt.Sprint(p)
	}
	return s
}
