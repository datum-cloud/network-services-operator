// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// UnverifiedHostname says why the platform would not admit a hostname yet.
type UnverifiedHostname struct {
	Hostname string
	Reason   string
}

// UnverifiedHostnames returns the hostnames no verified Domain covers yet. An
// ALB given one of these sits in Programming until the Domain verifies, and
// errors if it never does.
//
// The rules match the gateway controller, which is the authority on them: a
// hostname needs any Domain that equals it or is a parent of it with Verified
// true (ensureHostnameVerification), and a wildcard needs that Domain proven
// by its DNS TXT record (checkWildcardOwnership).
func UnverifiedHostnames(hostnames []string, domains []networkingv1alpha.Domain) []UnverifiedHostname {
	var out []UnverifiedHostname
	for _, hostname := range hostnames {
		if reason := unverifiedReason(normalizeHostname(hostname), domains); reason != "" {
			out = append(out, UnverifiedHostname{Hostname: hostname, Reason: reason})
		}
	}
	return out
}

func unverifiedReason(hostname string, domains []networkingv1alpha.Domain) string {
	wildcard := strings.HasPrefix(hostname, "*.")
	base := strings.TrimPrefix(hostname, "*.")

	var covering []string
	for i := range domains {
		d := &domains[i]
		name := normalizeHostname(d.Spec.DomainName)
		if name == "" || (base != name && !strings.HasSuffix(base, "."+name)) {
			continue
		}
		verified := apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerified)
		if wildcard {
			verified = verified &&
				apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
		}
		if verified {
			return ""
		}
		covering = append(covering, name)
	}

	switch {
	case wildcard:
		return fmt.Sprintf("a wildcard needs %s, or a parent domain, verified by its DNS TXT record", base)
	case len(covering) == 0:
		return "no domain in this project covers it"
	default:
		return fmt.Sprintf("domain %s is not verified yet", strings.Join(covering, ", "))
	}
}

func normalizeHostname(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}
