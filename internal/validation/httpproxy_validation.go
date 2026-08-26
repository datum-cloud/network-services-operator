package validation

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

const (
	minPortNumber = 1
	maxPortNumber = 65535
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

// validateProgrammableHostname enforces the Gateway API PreciseHostname
// constraints that apply once this value is synthesized into the generated
// HTTPRoute. Case is not enforced: the controller lowercases the value, and
// hostnames compare case-insensitively wherever it lands.
func validateProgrammableHostname(hostname string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if gatewayutil.IsPreciseHostname(hostname) {
		return allErrs
	}

	if host, port, err := net.SplitHostPort(hostname); err == nil && port != "" {
		detail := fmt.Sprintf("must not include a port; use %q and set the port on the backend endpoint", host)
		return append(allErrs, field.Invalid(fldPath, hostname, detail))
	}

	if len(hostname) > gatewayutil.MaxPreciseHostnameLength {
		detail := fmt.Sprintf("must be no more than %d characters", gatewayutil.MaxPreciseHostnameLength)
		return append(allErrs, field.Invalid(fldPath, hostname, detail))
	}

	detail := "must be a hostname consisting of lower case alphanumeric characters, '-' or '.', starting and ending with an alphanumeric character (e.g. 'example.com'); wildcards are not permitted"
	return append(allErrs, field.Invalid(fldPath, hostname, detail))
}

// validateHostHeaderOverride checks a Host header set by a RequestHeaderModifier
// filter. The controller carries this value into the generated route's
// URLRewrite.Hostname, so it must satisfy the hostname constraints even though
// the schema only sees it as a header value.
func validateHostHeaderOverride(filters []gatewayv1.HTTPRouteFilter, fldPath *field.Path) field.ErrorList {
	override, found := gatewayutil.FindHostHeaderOverride(filters)
	if !found {
		return field.ErrorList{}
	}

	hostHeaderPath := fldPath.Index(override.FilterIndex).
		Child("requestHeaderModifier", "set").Index(override.SetIndex).Child("value")

	return validateProgrammableHostname(override.Value, hostHeaderPath)
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
	allErrs = append(allErrs, validateHostHeaderOverride(rule.Filters, fldPath.Child("filters"))...)
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

	// instance and networkService backends don't use the endpoint field at all
	// — see their own validation blocks below instead.
	if backend.Instance == nil && backend.NetworkService == nil {
		allErrs = append(allErrs, validateHTTPProxyRuleBackendEndpoint(backend, fldPath)...)
	}

	// tls.hostname becomes the generated route's URLRewrite.Hostname and the
	// downstream BackendTLSPolicy hostname, so it carries the same constraints
	// as any other hostname the user writes.
	if backend.TLS != nil && backend.TLS.Hostname != nil && *backend.TLS.Hostname != "" {
		allErrs = append(allErrs, validateProgrammableHostname(*backend.TLS.Hostname, fldPath.Child("tls", "hostname"))...)
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

	if backend.Instance != nil {
		instanceFieldPath := fldPath.Child("instance", "name")
		if backend.Instance.Name == "" {
			allErrs = append(allErrs, field.Required(instanceFieldPath, "instance name is required"))
		} else {
			for _, msg := range validation.IsDNS1123Subdomain(backend.Instance.Name) {
				allErrs = append(allErrs, field.Invalid(instanceFieldPath, backend.Instance.Name, msg))
			}
		}
	}

	if backend.NetworkService != nil {
		nameFieldPath := fldPath.Child("networkService", "name")
		if backend.NetworkService.Name == "" {
			allErrs = append(allErrs, field.Required(nameFieldPath, "network service name is required"))
		} else {
			for _, msg := range validation.IsDNS1123Subdomain(backend.NetworkService.Name) {
				allErrs = append(allErrs, field.Invalid(nameFieldPath, backend.NetworkService.Name, msg))
			}
		}

		portFieldPath := fldPath.Child("networkService", "port")
		if backend.NetworkService.Port == "" {
			allErrs = append(allErrs, field.Required(portFieldPath, "network service port name is required"))
		} else {
			for _, msg := range validation.IsDNS1123Label(backend.NetworkService.Port) {
				allErrs = append(allErrs, field.Invalid(portFieldPath, backend.NetworkService.Port, msg))
			}
		}
	}

	allErrs = append(allErrs, validateFilters(backend.Filters, supportedHTTPBackendRefFilters, fldPath.Child("filters"))...)
	allErrs = append(allErrs, validateHostHeaderOverride(backend.Filters, fldPath.Child("filters"))...)
	return allErrs
}

// validateHTTPProxyRuleBackendEndpoint validates the endpoint field. Only
// called for endpoint/connector backends — instance backends don't carry an
// endpoint URL at all.
func validateHTTPProxyRuleBackendEndpoint(backend networkingv1alpha.HTTPProxyRuleBackend, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	endpointFieldPath := fldPath.Child("endpoint")
	u, err := url.Parse(backend.Endpoint)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(endpointFieldPath, backend.Endpoint, fmt.Sprintf("invalid endpoint: %s", err)))
	} else {
		if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
			allErrs = append(allErrs, field.NotSupported(endpointFieldPath.Key("scheme"), u.Scheme, []string{schemeHTTP, schemeHTTPS}))
		}

		if u.User != nil {
			allErrs = append(allErrs, field.Invalid(endpointFieldPath.Key("userinfo"), fmt.Sprintf("%s:redacted", u.User.Username()), "endpoint must not have a userinfo component"))
		}

		// Align with EndpointSlice validation of addresses.
		// See: https://github.com/kubernetes/kubernetes/blob/d21da29c9ec486956b204050cdfaa46c686e29cc/pkg/apis/discovery/validation/validation.go#L115
		hostFieldPath := endpointFieldPath.Key("host")
		host := u.Hostname()
		hasConnector := backend.Connector != nil
		isIPAddress := false
		if ip := net.ParseIP(host); ip != nil {
			isIPAddress = true
			// Adapted from https://github.com/kubernetes/kubernetes/blob/d21da29c9ec486956b204050cdfaa46c686e29cc/pkg/apis/core/validation/validation.go#L7797
			if ip.IsUnspecified() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, fmt.Sprintf("may not be unspecified (%v)", host)))
			}
			if ip.IsLoopback() && !hasConnector {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, "may not be in the loopback range (127.0.0.0/8, ::1/128)"))
			}
			if ip.IsLinkLocalUnicast() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, "may not be in the link-local range (169.254.0.0/16, fe80::/10)"))
			}
			if ip.IsLinkLocalMulticast() {
				allErrs = append(allErrs, field.Invalid(hostFieldPath, host, "may not be in the link-local multicast range (224.0.0.0/24, ff02::/10)"))
			}
		} else {
			if !hasConnector || host != "localhost" {
				allErrs = append(allErrs, validation.IsFullyQualifiedDomainName(hostFieldPath, host)...)
			}
		}

		// HTTPS endpoints with IP addresses require tls.hostname for certificate validation
		if u.Scheme == schemeHTTPS && isIPAddress {
			if backend.TLS == nil || backend.TLS.Hostname == nil || *backend.TLS.Hostname == "" {
				allErrs = append(allErrs, field.Required(fldPath.Child("tls", "hostname"), "tls.hostname is required for HTTPS endpoints with IP addresses"))
			}
		}

		// The endpoint port is carried into a backendRef and an EndpointSlice,
		// both of which enforce this range.
		if port := u.Port(); port != "" {
			portFieldPath := endpointFieldPath.Key("port")
			portNumber, err := strconv.Atoi(port)
			if err != nil {
				allErrs = append(allErrs, field.Invalid(portFieldPath, port, "must be a number"))
			} else if portNumber < minPortNumber || portNumber > maxPortNumber {
				detail := fmt.Sprintf("must be between %d and %d, inclusive", minPortNumber, maxPortNumber)
				allErrs = append(allErrs, field.Invalid(portFieldPath, port, detail))
			}
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

	return allErrs
}
