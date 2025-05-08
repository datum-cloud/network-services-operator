package validation

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
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

		if l.Hostname != nil {
			var allowedSuffixes []string
			for _, allowListEntry := range opts.CustomHostnameAllowList {
				if allowListEntry.ClusterName == opts.ClusterName {
					allowedSuffixes = allowListEntry.Suffixes
				}
			}

			if len(allowedSuffixes) == 0 {
				allErrs = append(allErrs, field.Invalid(listenerPath.Child("hostname"), l.Hostname, "hostnames are not permitted"))
			} else {
				hostnameStr := string(*l.Hostname)
				validHostname := false
				for _, suffix := range allowedSuffixes {
					if strings.HasSuffix(hostnameStr, "."+suffix) {
						validHostname = true
						break
					}
				}
				if !validHostname {
					allErrs = append(allErrs, field.Invalid(listenerPath.Child("hostname"), hostnameStr, fmt.Sprintf("hostname does not match any allowed suffixes: %v", allowedSuffixes)))
				}
			}
		}

		if !slices.Contains(opts.ValidPortNumbers, int(l.Port)) {
			allErrs = append(allErrs, field.NotSupported(listenerPath.Child("port"), l.Port, opts.ValidPortNumbers.StringSlice()))
		}

		if !slices.Contains(opts.ValidProtocolTypes, l.Protocol) {
			allErrs = append(allErrs, field.NotSupported(listenerPath.Child("protocol"), l.Protocol, opts.ValidProtocolTypes))
		}

		allErrs = append(allErrs, validateGatewayTLSConfig(l.TLS, listenerPath.Child("tls"), opts)...)

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
	if tls == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if tls.Mode != nil && *tls.Mode != gatewayv1.TLSModeTerminate {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("mode"), tls.Mode, "mode must be set to Terminate"))
	}

	if len(tls.CertificateRefs) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("certificateRefs"), "certificateRefs are not permitted"))
	}

	if tls.FrontendValidation != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("frontendValidation"), "frontendValidation is not permitted"))
	}

	if len(tls.Options) > 0 {
		for k, v := range tls.Options {
			optionPath := fldPath.Child("options").Key(string(k))

			if optValues, ok := opts.PermittedTLSOptions[string(k)]; !ok {
				allErrs = append(allErrs, field.Forbidden(optionPath, "option is not permitted"))
			} else {
				if len(optValues) > 0 && !slices.Contains(optValues, string(v)) {
					allErrs = append(allErrs, field.NotSupported(optionPath, string(v), optValues))
				}
			}
		}
	}

	return allErrs
}

type GatewayValidationOptions struct {
	ControllerName          gatewayv1.GatewayController
	PermittedTLSOptions     map[string][]string
	ValidPortNumbers        validPortNumbers
	ValidProtocolTypes      []gatewayv1.ProtocolType
	ClusterName             string
	CustomHostnameAllowList []config.CustomHostnameAllowListEntry
}

type validPortNumbers []int

func (v validPortNumbers) StringSlice() []string {
	s := make([]string, len(v))
	for i, p := range v {
		s[i] = fmt.Sprint(p)
	}
	return s
}
