// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
