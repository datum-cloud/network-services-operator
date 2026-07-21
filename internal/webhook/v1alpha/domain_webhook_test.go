// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	testDomainExampleCom     = "example.com"
	testDomainAutouserRun    = "autouser.run"
	testDomainAPIAutouserRun = "api.autouser.run"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{testDomainExampleCom, testDomainExampleCom},
		{"example.com.", testDomainExampleCom},
		{"  example.com.  ", testDomainExampleCom},
		{"ExAmPlE.CoM", testDomainExampleCom},
		{"", ""},
	}

	for _, tt := range tests {
		if got := normalizeHostname(tt.in); got != tt.want {
			t.Fatalf("normalizeHostname(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHostnameCoveredByDomain(t *testing.T) {
	tests := []struct {
		domain string
		host   string
		want   bool
	}{
		{testDomainAutouserRun, testDomainAutouserRun, true},
		{testDomainAutouserRun, testDomainAPIAutouserRun, true},
		{testDomainAPIAutouserRun, testDomainAPIAutouserRun, true},
		{testDomainAPIAutouserRun, "v1.api.autouser.run", true},
		{testDomainAPIAutouserRun, testDomainAutouserRun, false},
		{testDomainAutouserRun, "notautouser.run", false},
		{testDomainAutouserRun, "foo.notautouser.run", false},
		{testDomainAutouserRun, "API.AutoUser.Run.", true},
	}

	for _, tt := range tests {
		if got := hostnameCoveredByDomain(tt.domain, tt.host); got != tt.want {
			t.Fatalf("hostnameCoveredByDomain(%q, %q) = %v, want %v", tt.domain, tt.host, got, tt.want)
		}
	}
}

func TestDuplicateDomainName(t *testing.T) {
	existing := []networkingv1alpha.Domain{
		{Spec: networkingv1alpha.DomainSpec{DomainName: testDomainExampleCom}},
		{Spec: networkingv1alpha.DomainSpec{DomainName: testDomainAPIAutouserRun}},
	}

	tests := []struct {
		name      string
		candidate string
		want      bool
	}{
		{"unique domain accepted", testDomainAutouserRun, false},
		{"exact duplicate rejected", testDomainExampleCom, true},
		{"case-insensitive duplicate rejected", "ExAmPlE.CoM", true},
		{"trailing-dot duplicate rejected", "example.com.", true},
		{"whitespace duplicate rejected", "  api.autouser.run  ", true},
		{"subdomain is not a duplicate", "www.example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := duplicateDomainName(existing, tt.candidate); got != tt.want {
				t.Fatalf("duplicateDomainName(_, %q) = %v, want %v", tt.candidate, got, tt.want)
			}
		})
	}
}

func TestDNSZoneDomainRefNameMatch(t *testing.T) {
	z := unstructured.Unstructured{
		Object: map[string]any{
			"status": map[string]any{
				"domainRef": map[string]any{
					"name": "my-domain",
				},
			},
		},
	}

	refName, found, err := unstructured.NestedString(z.Object, "status", "domainRef", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatalf("expected domainRef.name to be found")
	}
	if refName != "my-domain" {
		t.Fatalf("expected refName to be %q, got %q", "my-domain", refName)
	}
}
