package validation

import (
	"fmt"
	"net/url"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func ValidateHTTPProxy(httpProxy *networkingv1alpha.HTTPProxy) field.ErrorList {

	allErrs := field.ErrorList{}

	for _, msg := range validation.IsDNS1123Label(httpProxy.Name) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("metadata", "name"), httpProxy.Name, msg))
	}

	allErrs = append(allErrs, validateHTTPProxyRules(httpProxy, field.NewPath("spec", "rules"))...)

	return allErrs
}

func validateHTTPProxyRules(httpProxy *networkingv1alpha.HTTPProxy, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, rule := range httpProxy.Spec.Rules {
		allErrs = append(allErrs, validateHTTPProxyRule(rule, fldPath.Index(i))...)
	}

	return allErrs
}

func validateHTTPProxyRule(rule networkingv1alpha.HTTPProxyRule, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateFilters(rule.Filters, supportedHTTPRouteRuleFilters, fldPath.Child("filters"))...)
	allErrs = append(allErrs, validateHTTPProxyRuleBackends(rule, fldPath.Child("backends"))...)

	return allErrs
}

func validateHTTPProxyRuleBackends(rule networkingv1alpha.HTTPProxyRule, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(rule.Backends) == 0 {
		// If no backends are provided, require that a RequestRedirect filter exists
		redirectFilterFound := false
		for _, filter := range rule.Filters {
			if filter.Type == gatewayv1.HTTPRouteFilterRequestRedirect {
				redirectFilterFound = true
				break
			}
		}

		if !redirectFilterFound {
			allErrs = append(allErrs, field.Required(fldPath, "a backend is required unless a RequestRedirect filter is present on the rule"))
		}
	}

	for i, backend := range rule.Backends {
		allErrs = append(allErrs, validateHTTPProxyRuleBackend(backend, fldPath.Index(i))...)
	}

	return allErrs
}

func validateHTTPProxyRuleBackend(backend networkingv1alpha.HTTPProxyRuleBackend, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	endpointFieldPath := fldPath.Child("endpoint")
	u, err := url.Parse(backend.Endpoint)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(endpointFieldPath, backend.Endpoint, fmt.Sprintf("invalid endpoint: %s", err)))
	} else {
		if u.Scheme != "http" && u.Scheme != "https" {
			allErrs = append(allErrs, field.NotSupported(endpointFieldPath.Key("scheme"), u.Scheme, []string{"http", "https"}))
		}

		if u.User != nil {
			allErrs = append(allErrs, field.Invalid(endpointFieldPath.Key("userinfo"), fmt.Sprintf("%s:redacted", u.User.Username()), "endpoint must not have a userinfo component"))
		}

		for _, msg := range validation.IsDNS1123SubdomainWithUnderscore(u.Host) {
			allErrs = append(allErrs, field.Invalid(endpointFieldPath.Key("host"), u.Host, msg))
		}

		if u.Path != "" {
			allErrs = append(allErrs, field.Invalid(endpointFieldPath.Key("path"), u.Path, "endpoint must not have a path component"))
		}

		if u.RawQuery != "" {
			allErrs = append(allErrs, field.Invalid(endpointFieldPath.Key("query"), u.RawQuery, "endpoint must not have a query component"))
		}

		if u.Fragment != "" {
			allErrs = append(allErrs, field.Invalid(endpointFieldPath.Key("fragment"), u.Fragment, "endpoint must not have a fragment component"))
		}
	}

	allErrs = append(allErrs, validateFilters(backend.Filters, supportedHTTPBackendRefFilters, fldPath.Child("filters"))...)
	return allErrs
}
