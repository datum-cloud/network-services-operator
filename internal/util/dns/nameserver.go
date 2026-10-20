// SPDX-License-Identifier: AGPL-3.0-only

// Package dns provides utilities for DNS-related operations.
package dns

import (
	"slices"
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
	zone := ReadZoneInputs(dnsZone)

	// Check if DNSZone is ready
	if !zone.Ready() {
		return false
	}

	// Check if DNSZone has nameservers
	if len(zone.Nameservers) == 0 {
		return false
	}

	// Check if domain has nameservers
	if len(domain.Status.Nameservers) == 0 {
		return false
	}

	// Check for nameserver overlap
	return DomainNameserversOverlapZone(domain.Status.Nameservers, zone.Nameservers)
}

// ZoneInputs is what a Gateway's DNS reads from a DNSZone: its domain name,
// whether it is Accepted and Programmed, and its nameservers. A zone change
// that leaves it equal cannot change a Gateway's DNS, so the Gateway
// controller's zone watch passes only a change to it. A new read of a zone
// field belongs here, and the watch follows it.
type ZoneInputs struct {
	DomainName  string
	Accepted    bool
	Programmed  bool
	Nameservers []string
}

// ReadZoneInputs returns what a Gateway's DNS reads from zone.
func ReadZoneInputs(zone *dnsv1alpha1.DNSZone) ZoneInputs {
	return ZoneInputs{
		DomainName:  zone.Spec.DomainName,
		Accepted:    apimeta.IsStatusConditionTrue(zone.Status.Conditions, "Accepted"),
		Programmed:  apimeta.IsStatusConditionTrue(zone.Status.Conditions, "Programmed"),
		Nameservers: zone.Status.Nameservers,
	}
}

// Ready reports whether the zone is Accepted and Programmed.
func (z ZoneInputs) Ready() bool {
	return z.Accepted && z.Programmed
}

// Equal reports whether two zones give a Gateway's DNS the same inputs.
func (z ZoneInputs) Equal(other ZoneInputs) bool {
	return z.DomainName == other.DomainName &&
		z.Accepted == other.Accepted &&
		z.Programmed == other.Programmed &&
		slices.Equal(z.Nameservers, other.Nameservers)
}
