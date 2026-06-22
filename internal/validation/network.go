package validation

import (
	"fmt"
	"net"
	"net/url"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// validateExternalHost validates that a host (IP literal or DNS name) used as an
// egress target by the shared data plane is not an internal or link-local
// address that could be abused for SSRF against cluster-local or cloud-metadata
// services (e.g. 169.254.169.254).
//
// The rules intentionally mirror those already enforced for HTTPProxy backends
// (see validateHTTPProxyRuleBackend): IP literals must not be unspecified,
// loopback, or link-local; DNS names must be fully qualified, which rejects
// single-label internal names such as "localhost", "kubernetes", or "metadata".
// RFC1918 / unique-local ranges are intentionally NOT blocked here, to stay
// consistent with the HTTPProxy rules; egress allowlisting is tracked
// separately.
func validateExternalHost(fldPath *field.Path, host string) field.ErrorList {
	allErrs := field.ErrorList{}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			allErrs = append(allErrs, field.Invalid(fldPath, host, fmt.Sprintf("may not be unspecified (%v)", host)))
		}
		if ip.IsLoopback() {
			allErrs = append(allErrs, field.Invalid(fldPath, host, "may not be in the loopback range (127.0.0.0/8, ::1/128)"))
		}
		if ip.IsLinkLocalUnicast() {
			allErrs = append(allErrs, field.Invalid(fldPath, host, "may not be in the link-local range (169.254.0.0/16, fe80::/10)"))
		}
		if ip.IsLinkLocalMulticast() {
			allErrs = append(allErrs, field.Invalid(fldPath, host, "may not be in the link-local multicast range (224.0.0.0/24, ff02::/10)"))
		}
		return allErrs
	}

	// Not an IP literal: require a fully-qualified domain name so single-label
	// internal service names cannot be used to reach cluster-local services.
	allErrs = append(allErrs, validation.IsFullyQualifiedDomainName(fldPath, host)...)

	return allErrs
}

// validateExternalHTTPSURL validates a free-form URL string that the shared data
// plane will fetch on a tenant's behalf (e.g. OIDC issuer/endpoints, remote
// JWKS). The URL must be a valid absolute https URL with no userinfo component
// and a host that passes validateExternalHost. An empty value is treated as
// valid (callers decide whether the field is required).
func validateExternalHTTPSURL(fldPath *field.Path, raw string) field.ErrorList {
	allErrs := field.ErrorList{}

	if raw == "" {
		return allErrs
	}

	u, err := url.Parse(raw)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, raw, fmt.Sprintf("invalid URL: %s", err)))
		return allErrs
	}

	if u.Scheme != schemeHTTPS {
		allErrs = append(allErrs, field.NotSupported(fldPath.Key("scheme"), u.Scheme, []string{schemeHTTPS}))
	}

	if u.User != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Key("userinfo"), fmt.Sprintf("%s:redacted", u.User.Username()), "must not have a userinfo component"))
	}

	host := u.Hostname()
	if host == "" {
		allErrs = append(allErrs, field.Required(fldPath.Key("host"), "must have a host component"))
		return allErrs
	}

	allErrs = append(allErrs, validateExternalHost(fldPath.Key("host"), host)...)

	return allErrs
}
