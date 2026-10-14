// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func testDomain(name string, trueConditions ...string) networkingv1alpha.Domain {
	d := networkingv1alpha.Domain{Spec: networkingv1alpha.DomainSpec{DomainName: name}}
	for _, c := range trueConditions {
		d.Status.Conditions = append(d.Status.Conditions, metav1.Condition{Type: c, Status: metav1.ConditionTrue})
	}
	return d
}

func TestUnverifiedHostnames(t *testing.T) {
	const (
		verified    = networkingv1alpha.DomainConditionVerified
		verifiedDNS = networkingv1alpha.DomainConditionVerifiedDNS
	)

	tests := []struct {
		name     string
		hostname string
		domains  []networkingv1alpha.Domain
		reason   string
	}{
		{
			name:     "apex of a verified domain",
			hostname: "example.com",
			domains:  []networkingv1alpha.Domain{testDomain("example.com", verified)},
		},
		{
			name:     "subdomain of a verified domain",
			hostname: "app.example.com",
			domains:  []networkingv1alpha.Domain{testDomain("example.com", verified)},
		},
		{
			name:     "case and trailing dot are ignored",
			hostname: "App.Example.com.",
			domains:  []networkingv1alpha.Domain{testDomain("example.com", verified)},
		},
		{
			name:     "a verified parent covers it even when a closer domain is unverified",
			hostname: "api.staging.example.com",
			domains: []networkingv1alpha.Domain{
				testDomain("staging.example.com"),
				testDomain("example.com", verified),
			},
		},
		{
			name:     "no domain covers it",
			hostname: "app.other.dev",
			domains:  []networkingv1alpha.Domain{testDomain("example.com", verified)},
			reason:   "no domain in this project covers it",
		},
		{
			name:     "a suffix that is not a label boundary does not count",
			hostname: "notexample.com",
			domains:  []networkingv1alpha.Domain{testDomain("example.com", verified)},
			reason:   "no domain in this project covers it",
		},
		{
			name:     "domain still pending",
			hostname: "app.example.com",
			domains:  []networkingv1alpha.Domain{testDomain("example.com")},
			reason:   "domain example.com is not verified yet",
		},
		{
			name:     "wildcard under a domain verified by DNS",
			hostname: "*.s3.example.com",
			domains:  []networkingv1alpha.Domain{testDomain("example.com", verified, verifiedDNS)},
		},
		{
			name:     "wildcard under a domain verified another way",
			hostname: "*.s3.example.com",
			domains: []networkingv1alpha.Domain{
				testDomain("example.com", verified, networkingv1alpha.DomainConditionVerifiedHTTP),
			},
			reason: "a wildcard needs s3.example.com, or a parent domain, verified by its DNS TXT record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnverifiedHostnames([]string{tt.hostname}, tt.domains)
			if tt.reason == "" {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, []UnverifiedHostname{{Hostname: tt.hostname, Reason: tt.reason}}, got)
		})
	}
}
