// SPDX-License-Identifier: AGPL-3.0-only

// Package dns provides utilities for DNS-related operations.
package dns

import (
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// NormalizeNameserver normalizes a nameserver hostname by lowercasing,
// trimming whitespace, and removing trailing dots.
func NormalizeNameserver(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// HasNameserverOverlap returns true if there is at least one overlapping
// nameserver between the two slices. Comparison is case-insensitive and
// ignores trailing dots.
func HasNameserverOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		n := NormalizeNameserver(v)
		if n == "" {
			continue
		}
		set[n] = struct{}{}
	}
	for _, v := range b {
		n := NormalizeNameserver(v)
		if n == "" {
			continue
		}
		if _, ok := set[n]; ok {
			return true
		}
	}
	return false
}

// DomainNameserversOverlapZone checks if a Domain's nameservers overlap with
// the given zone nameservers. This is a convenience wrapper that extracts
// hostnames from the Domain's Nameserver structs.
func DomainNameserversOverlapZone(domainNS []networkingv1alpha.Nameserver, zoneNS []string) bool {
	if len(domainNS) == 0 || len(zoneNS) == 0 {
		return false
	}

	// Extract hostnames from domain nameservers
	hostnames := make([]string, 0, len(domainNS))
	for _, ns := range domainNS {
		if ns.Hostname != "" {
			hostnames = append(hostnames, ns.Hostname)
		}
	}

	return HasNameserverOverlap(hostnames, zoneNS)
}

// HasDNSAuthority checks whether Datum DNS currently has authority over the
// domain by verifying:
// 1. The DNSZone is ready (Accepted=True and Programmed=True)
// 2. The Domain's nameservers include at least one of the DNSZone's nameservers
//
// This is used to determine if we can safely create DNS records for a domain,
// regardless of how the domain was originally verified (DNS TXT, HTTP, or DNSZone).
func HasDNSAuthority(domain *networkingv1alpha.Domain, dnsZone *dnsv1alpha1.DNSZone) bool {
	// Check if DNSZone is ready
	if !apimeta.IsStatusConditionTrue(dnsZone.Status.Conditions, "Accepted") ||
		!apimeta.IsStatusConditionTrue(dnsZone.Status.Conditions, "Programmed") {
		return false
	}

	// Check if DNSZone has nameservers
	if len(dnsZone.Status.Nameservers) == 0 {
		return false
	}

	// Check if domain has nameservers
	if len(domain.Status.Nameservers) == 0 {
		return false
	}

	// Check for nameserver overlap
	return DomainNameserversOverlapZone(domain.Status.Nameservers, dnsZone.Status.Nameservers)
}
