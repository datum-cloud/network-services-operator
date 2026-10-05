// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"go.datum.net/network-services-operator/internal/config"
)

// HostnameOptions says which custom hostnames are acceptable.
type HostnameOptions struct {
	// AllowWildcards admits a single leading wildcard label, as in
	// "*.s3.example.com".
	AllowWildcards bool

	// PlatformDomains are the domains the platform names its own hostnames
	// under. No wildcard may sit beneath them.
	PlatformDomains []string
}

// CustomHostnameOptions derives the hostname rules from operator settings:
// wildcards ride on the certificate service, which issues their certificates.
func CustomHostnameOptions(gatewayConfig config.GatewayConfig) HostnameOptions {
	return HostnameOptions{
		AllowWildcards:  gatewayConfig.CertificateService.Enabled,
		PlatformDomains: gatewayConfig.ManagedTargetDomains(),
	}
}

// ValidateCustomHostname checks a hostname a user attaches to a load balancer.
func ValidateCustomHostname(fldPath *field.Path, hostname string, opts HostnameOptions) field.ErrorList {
	base, wildcard := strings.CutPrefix(hostname, "*.")
	if !wildcard || !opts.AllowWildcards {
		return validation.IsFullyQualifiedDomainName(fldPath, hostname)
	}

	if strings.Contains(base, "*") || len(validation.IsFullyQualifiedDomainName(fldPath, base)) > 0 {
		return field.ErrorList{field.Invalid(fldPath, hostname,
			`a wildcard must be a single leading "*." label followed by a domain with at least two labels, e.g. "*.example.com"`)}
	}

	for _, platform := range opts.PlatformDomains {
		if base == platform || strings.HasSuffix(base, "."+platform) {
			return field.ErrorList{field.Invalid(fldPath, hostname,
				fmt.Sprintf("wildcards under %s are managed by the platform and cannot be attached", platform))}
		}
	}

	return nil
}
