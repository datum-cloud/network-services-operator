// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// hostnameRefusal says why a hostname was not admitted, for the listener's
// Accepted condition and the hostname's status.
type hostnameRefusal struct {
	reason  string
	message string
}

// provenByDNS reports whether a Domain was verified by a DNS TXT record. A
// Datum DNS zone does not count yet: any project can create a zone for any
// domain, and a zone proves ownership only once Datum serves it and the
// registry delegates to it. An HTTP token does not count either, since anyone
// who can serve one name beneath a wildcard can serve the token.
func provenByDNS(domain *networkingv1alpha.Domain) bool {
	return apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified) &&
		apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
}

// wildcardOwnership decides whether a wildcard's base, or a parent of it, is
// proven by DNS. When it is not, it explains why and names a Domain to create
// so the user has a TXT record to publish.
type wildcardOwnership struct {
	proven       bool
	refusal      hostnameRefusal
	createDomain string
}

func checkWildcardOwnership(hostname string, domains []networkingv1alpha.Domain) wildcardOwnership {
	base := strings.TrimPrefix(hostname, "*.")

	var verified, pending *networkingv1alpha.Domain
	baseDomainExists := false
	for i := range domains {
		d := &domains[i]
		if !domainCoversHostname(d.Spec.DomainName, base) {
			continue
		}
		if d.Spec.DomainName == base {
			baseDomainExists = true
		}
		if provenByDNS(d) {
			return wildcardOwnership{proven: true}
		}
		mostSpecific := func(current *networkingv1alpha.Domain) bool {
			return current == nil || len(d.Spec.DomainName) > len(current.Spec.DomainName)
		}
		if apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
			if mostSpecific(verified) {
				verified = d
			}
		} else if mostSpecific(pending) {
			pending = d
		}
	}

	need := fmt.Sprintf("The wildcard %q needs DNS proof that you control %s or a parent domain. ", hostname, base)
	result := wildcardOwnership{refusal: hostnameRefusal{reason: networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired}}

	if verified != nil {
		need += fmt.Sprintf("Domain %q was verified %s. ", verified.Spec.DomainName, verificationMethodPhrase(verified))
	}

	switch {
	case pending != nil:
		result.refusal.message = need + fmt.Sprintf("Publish the DNS TXT record shown on Domain %q; HTTP verification does not count for wildcards.", pending.Spec.DomainName)
	case !baseDomainExists:
		result.createDomain = base
		if verified == nil {
			result.createDomain = registrableDomain(base)
		}
		result.refusal.message = need + fmt.Sprintf("Publish the DNS TXT record shown on Domain %q to prove it.", result.createDomain)
	default:
		result.refusal.message = need + fmt.Sprintf("Add a Domain for a parent of %s and publish the DNS TXT record shown on it.", base)
	}

	return result
}

func verificationMethodPhrase(domain *networkingv1alpha.Domain) string {
	switch {
	case apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP):
		return "over HTTP, which does not prove control of every name beneath it"
	case apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNSZone):
		return "through a Datum DNS zone, which does not yet count as proof for wildcards"
	default:
		return "before the platform recorded how, so it does not count as DNS proof"
	}
}

func registrableDomain(hostname string) string {
	apex, err := registeredApex(hostname)
	if err != nil {
		return hostname
	}
	return apex
}
