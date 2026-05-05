// SPDX-License-Identifier: AGPL-3.0-only

package dns

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

func TestNormalizeNameserver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple hostname",
			input:    "ns1.example.com",
			expected: "ns1.example.com",
		},
		{
			name:     "hostname with trailing dot",
			input:    "ns1.example.com.",
			expected: "ns1.example.com",
		},
		{
			name:     "uppercase hostname",
			input:    "NS1.EXAMPLE.COM",
			expected: "ns1.example.com",
		},
		{
			name:     "hostname with whitespace",
			input:    "  ns1.example.com  ",
			expected: "ns1.example.com",
		},
		{
			name:     "mixed case with trailing dot and whitespace",
			input:    "  NS1.Example.COM.  ",
			expected: "ns1.example.com",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only whitespace",
			input:    "   ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeNameserver(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasNameserverOverlap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        []string
		b        []string
		expected bool
	}{
		{
			name:     "matching nameservers",
			a:        []string{"ns1.example.com", "ns2.example.com"},
			b:        []string{"ns1.example.com", "ns3.example.com"},
			expected: true,
		},
		{
			name:     "no overlap",
			a:        []string{"ns1.example.com", "ns2.example.com"},
			b:        []string{"ns3.example.com", "ns4.example.com"},
			expected: false,
		},
		{
			name:     "case insensitive match",
			a:        []string{"NS1.EXAMPLE.COM"},
			b:        []string{"ns1.example.com"},
			expected: true,
		},
		{
			name:     "trailing dot ignored",
			a:        []string{"ns1.example.com."},
			b:        []string{"ns1.example.com"},
			expected: true,
		},
		{
			name:     "empty first slice",
			a:        []string{},
			b:        []string{"ns1.example.com"},
			expected: false,
		},
		{
			name:     "empty second slice",
			a:        []string{"ns1.example.com"},
			b:        []string{},
			expected: false,
		},
		{
			name:     "both empty",
			a:        []string{},
			b:        []string{},
			expected: false,
		},
		{
			name:     "nil slices",
			a:        nil,
			b:        nil,
			expected: false,
		},
		{
			name:     "empty strings ignored",
			a:        []string{"", "ns1.example.com", ""},
			b:        []string{"ns1.example.com"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasNameserverOverlap(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDomainNameserversOverlapZone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		domainNS []networkingv1alpha.Nameserver
		zoneNS   []string
		expected bool
	}{
		{
			name: "matching nameservers",
			domainNS: []networkingv1alpha.Nameserver{
				{Hostname: "ns1.datumcloud.net"},
				{Hostname: "ns2.datumcloud.net"},
			},
			zoneNS:   []string{"ns1.datumcloud.net", "ns2.datumcloud.net"},
			expected: true,
		},
		{
			name: "partial overlap",
			domainNS: []networkingv1alpha.Nameserver{
				{Hostname: "ns1.datumcloud.net"},
				{Hostname: "ns2.otherprovider.com"},
			},
			zoneNS:   []string{"ns1.datumcloud.net", "ns3.datumcloud.net"},
			expected: true,
		},
		{
			name: "no overlap",
			domainNS: []networkingv1alpha.Nameserver{
				{Hostname: "ns1.otherprovider.com"},
				{Hostname: "ns2.otherprovider.com"},
			},
			zoneNS:   []string{"ns1.datumcloud.net", "ns2.datumcloud.net"},
			expected: false,
		},
		{
			name:     "empty domain nameservers",
			domainNS: []networkingv1alpha.Nameserver{},
			zoneNS:   []string{"ns1.datumcloud.net"},
			expected: false,
		},
		{
			name: "empty zone nameservers",
			domainNS: []networkingv1alpha.Nameserver{
				{Hostname: "ns1.datumcloud.net"},
			},
			zoneNS:   []string{},
			expected: false,
		},
		{
			name: "case insensitive and trailing dot",
			domainNS: []networkingv1alpha.Nameserver{
				{Hostname: "NS1.DATUMCLOUD.NET."},
			},
			zoneNS:   []string{"ns1.datumcloud.net"},
			expected: true,
		},
		{
			name: "empty hostname in domain nameserver",
			domainNS: []networkingv1alpha.Nameserver{
				{Hostname: ""},
				{Hostname: "ns1.datumcloud.net"},
			},
			zoneNS:   []string{"ns1.datumcloud.net"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DomainNameserversOverlapZone(tt.domainNS, tt.zoneNS)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasDNSAuthority(t *testing.T) {
	t.Parallel()

	// Helper to create a DNSZone with conditions and nameservers
	newDNSZone := func(accepted, programmed bool, nameservers []string) *dnsv1alpha1.DNSZone {
		z := &dnsv1alpha1.DNSZone{
			Status: dnsv1alpha1.DNSZoneStatus{
				Nameservers: nameservers,
			},
		}
		if accepted {
			z.Status.Conditions = append(z.Status.Conditions, metav1.Condition{
				Type:   "Accepted",
				Status: metav1.ConditionTrue,
			})
		}
		if programmed {
			z.Status.Conditions = append(z.Status.Conditions, metav1.Condition{
				Type:   "Programmed",
				Status: metav1.ConditionTrue,
			})
		}
		return z
	}

	// Helper to create a Domain with nameservers
	newDomain := func(nameservers []string) *networkingv1alpha.Domain {
		d := &networkingv1alpha.Domain{}
		for _, ns := range nameservers {
			d.Status.Nameservers = append(d.Status.Nameservers, networkingv1alpha.Nameserver{
				Hostname: ns,
			})
		}
		return d
	}

	tests := []struct {
		name     string
		domain   *networkingv1alpha.Domain
		dnsZone  *dnsv1alpha1.DNSZone
		expected bool
	}{
		{
			name:     "all conditions met",
			domain:   newDomain([]string{"ns1.datumcloud.net", "ns2.datumcloud.net"}),
			dnsZone:  newDNSZone(true, true, []string{"ns1.datumcloud.net", "ns2.datumcloud.net"}),
			expected: true,
		},
		{
			name:     "zone not accepted",
			domain:   newDomain([]string{"ns1.datumcloud.net"}),
			dnsZone:  newDNSZone(false, true, []string{"ns1.datumcloud.net"}),
			expected: false,
		},
		{
			name:     "zone not programmed",
			domain:   newDomain([]string{"ns1.datumcloud.net"}),
			dnsZone:  newDNSZone(true, false, []string{"ns1.datumcloud.net"}),
			expected: false,
		},
		{
			name:     "zone has no nameservers",
			domain:   newDomain([]string{"ns1.datumcloud.net"}),
			dnsZone:  newDNSZone(true, true, []string{}),
			expected: false,
		},
		{
			name:     "domain has no nameservers",
			domain:   newDomain([]string{}),
			dnsZone:  newDNSZone(true, true, []string{"ns1.datumcloud.net"}),
			expected: false,
		},
		{
			name:     "nameservers don't overlap",
			domain:   newDomain([]string{"ns1.otherprovider.com", "ns2.otherprovider.com"}),
			dnsZone:  newDNSZone(true, true, []string{"ns1.datumcloud.net", "ns2.datumcloud.net"}),
			expected: false,
		},
		{
			name:     "partial nameserver overlap is sufficient",
			domain:   newDomain([]string{"ns1.datumcloud.net", "ns2.otherprovider.com"}),
			dnsZone:  newDNSZone(true, true, []string{"ns1.datumcloud.net", "ns2.datumcloud.net"}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasDNSAuthority(tt.domain, tt.dnsZone)
			assert.Equal(t, tt.expected, result)
		})
	}
}
