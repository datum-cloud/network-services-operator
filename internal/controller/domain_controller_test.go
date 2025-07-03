// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	datumapisv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestParseWHOISData(t *testing.T) {
	controller := &DomainReconciler{}

	// Sample WHOIS data for example.com
	sampleWHOIS := `
Domain Name: EXAMPLE.COM
Registry Domain ID: 2336799_DOMAIN_COM-VRSN
Registrar WHOIS Server: whois.iana.org
Registrar URL: http://res-dom.iana.org
Updated Date: 2022-08-14T07:01:38Z
Creation Date: 1995-08-14T04:00:00Z
Registry Expiry Date: 2023-08-13T04:00:00Z
Registrar: Internet Assigned Numbers Authority
Registrar IANA ID: 376
Registrar Abuse Contact Email: abuse@iana.org
Registrar Abuse Contact Phone: +1-208-555-0123
Name Server: A.IANA-SERVERS.NET
Name Server: B.IANA-SERVERS.NET
DNSSEC: signedDelegation
Status: clientTransferProhibited https://icann.org/epp#clientTransferProhibited
Status: serverDeleteProhibited https://icann.org/epp#serverDeleteProhibited
Status: serverTransferProhibited https://icann.org/epp#serverTransferProhibited
Status: serverUpdateProhibited https://icann.org/epp#serverUpdateProhibited
`

	result := controller.parseWHOISData(sampleWHOIS)

	// Verify the parsed data
	assert.Equal(t, "Internet Assigned Numbers Authority", result.IANAName)
	assert.Equal(t, "376", result.IANAID)
	assert.Equal(t, "1995-08-14T04:00:00Z", result.CreatedDate)
	assert.Equal(t, "2022-08-14T07:01:38Z", result.ModifiedDate)
	assert.Equal(t, "2023-08-13T04:00:00Z", result.ExpirationDate)
	assert.Equal(t, 2, len(result.Nameservers))

	// Compare nameservers case-insensitively
	lowerNameservers := make([]string, 0, len(result.Nameservers))
	for _, ns := range result.Nameservers {
		lowerNameservers = append(lowerNameservers, strings.ToLower(ns))
	}
	assert.Contains(t, lowerNameservers, strings.ToLower("A.IANA-SERVERS.NET"))
	assert.Contains(t, lowerNameservers, strings.ToLower("B.IANA-SERVERS.NET"))

	assert.True(t, result.DNSSEC.Signed)

	// Compare status codes (parser strips URLs, so just check for the code)
	assert.Contains(t, result.ClientStatusCodes, "clientTransferProhibited")
	assert.Equal(t, 3, len(result.ServerStatusCodes))
}

func TestParseWHOISDataWithMissingFields(t *testing.T) {
	controller := &DomainReconciler{}

	// WHOIS data with missing fields
	sampleWHOIS := `
Domain Name: EXAMPLE.COM
Registrar: Some Registrar
`

	result := controller.parseWHOISData(sampleWHOIS)

	// Verify defaults are set
	assert.Equal(t, "Some Registrar", result.IANAName)
	assert.Equal(t, "0", result.IANAID) // Default IANA ID
	assert.Equal(t, "", result.CreatedDate)
	assert.Equal(t, "", result.ModifiedDate)
	assert.Equal(t, "", result.ExpirationDate)
	assert.Equal(t, 0, len(result.Nameservers))
	assert.False(t, result.DNSSEC.Signed)
	assert.Equal(t, 0, len(result.ClientStatusCodes))
	assert.Equal(t, 0, len(result.ServerStatusCodes))
}

func TestIsDomainVerified(t *testing.T) {
	controller := &DomainReconciler{}

	tests := []struct {
		name     string
		domain   *datumapisv1alpha.Domain
		expected bool
	}{
		{
			name: "no verification status",
			domain: &datumapisv1alpha.Domain{
				Spec: datumapisv1alpha.DomainSpec{
					DomainName: "example.com",
				},
			},
			expected: false,
		},
		{
			name: "has registrar data",
			domain: &datumapisv1alpha.Domain{
				Spec: datumapisv1alpha.DomainSpec{
					DomainName: "example.com",
				},
				Status: datumapisv1alpha.DomainStatus{
					Registrar: &datumapisv1alpha.DomainRegistrarStatus{
						IANAName: "Test Registrar",
					},
				},
			},
			expected: true,
		},
		{
			name: "has verification records but no registrar",
			domain: &datumapisv1alpha.Domain{
				Spec: datumapisv1alpha.DomainSpec{
					DomainName: "example.com",
				},
				Status: datumapisv1alpha.DomainStatus{
					Verification: &datumapisv1alpha.DomainVerificationStatus{
						RequiredDNSRecords: []datumapisv1alpha.DNSVerificationExpectedRecord{
							{
								Name:    "_datum-verification.example.com",
								Type:    "TXT",
								Content: "datum-verification=test",
							},
						},
					},
				},
			},
			expected: false, // Will be false because DNS check will fail in test environment
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := controller.isDomainVerified(tt.domain)
			t.Logf("Test case: %s, Domain: %+v, Result: %v, Expected: %v",
				tt.name, tt.domain.Status, result, tt.expected)
			assert.Equal(t, tt.expected, result)
		})
	}
}
