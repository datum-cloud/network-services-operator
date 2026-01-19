package validation

import (
	"fmt"
	"net"
	"net/url"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func ValidateHTTPProxy(httpProxy *networkingv1alpha.HTTPProxy) field.ErrorList {

	allErrs := field.ErrorList{}

	hostnamesPath := field.NewPath("spec", "hostnames")
	hostnames := sets.New[gatewayv1.Hostname]()
	for i, hostname := range httpProxy.Spec.Hostnames {
		hostnamePath := hostnamesPath.Index(i).Child("hostname")
		allErrs = append(allErrs, validation.IsFullyQualifiedDomainName(hostnamePath, string(hostname))...)
		if hostnames.Has(hostname) {
			allErrs = append(allErrs, field.Duplicate(hostnamePath, hostname))
		} else {
			hostnames.Insert(hostname)
		}
	}

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

		// Align with EndpointSlice validation of addresses.
		// See: https://github.com/kubernetes/kubernetes/blob/d21da29c9ec486956b204050cdfaa46c686e29cc/pkg/apis/discovery/validation/validation.go#L115
		hostFieldPath := endpointFieldPath.Key("host")
		host := u.Hostname()
		if ip := net.ParseIP(host); ip != nil {
			// Adapted from https://github.com/kubernetes/kubernetes/blob/d21da29c9ec486956b204050cdfaa46c686e29cc/pkg/apis/core/validation/validation.go#L7797
			if ip.IsUnspecified() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, fmt.Sprintf("may not be unspecified (%v)", host)))
			}
			if ip.IsLoopback() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, "may not be in the loopback range (127.0.0.0/8, ::1/128)"))
			}
			if ip.IsLinkLocalUnicast() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, "may not be in the link-local range (169.254.0.0/16, fe80::/10)"))
			}
			if ip.IsLinkLocalMulticast() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, "may not be in the link-local multicast range (224.0.0.0/24, ff02::/10)"))
			}
		} else {
			allErrs = append(allErrs, validation.IsFullyQualifiedDomainName(hostFieldPath, host)...)
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

	if backend.Connector != nil {
		connectorFieldPath := fldPath.Child("connector", "name")
		if backend.Connector.Name == "" {
			allErrs = append(allErrs, field.Required(connectorFieldPath, "connector name is required"))
		} else {
			for _, msg := range validation.IsDNS1123Label(backend.Connector.Name) {
				allErrs = append(allErrs, field.Invalid(connectorFieldPath, backend.Connector.Name, msg))
			}
		}
	}

	allErrs = append(allErrs, validateFilters(backend.Filters, supportedHTTPBackendRefFilters, fldPath.Child("filters"))...)
	return allErrs
}
