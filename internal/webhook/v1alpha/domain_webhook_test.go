// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"example.com", "example.com"},
		{"example.com.", "example.com"},
		{"  example.com.  ", "example.com"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := normalizeHostname(tt.in); got != tt.want {
			t.Fatalf("normalizeHostname(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsConditionTrue(t *testing.T) {
	conds := []metav1.Condition{
		{Type: networkingv1alpha.HTTPProxyConditionAccepted, Status: metav1.ConditionTrue},
		{Type: networkingv1alpha.HTTPProxyConditionProgrammed, Status: metav1.ConditionFalse},
	}

	if !isConditionTrue(conds, networkingv1alpha.HTTPProxyConditionAccepted) {
		t.Fatalf("expected Accepted to be true")
	}
	if isConditionTrue(conds, networkingv1alpha.HTTPProxyConditionProgrammed) {
		t.Fatalf("expected Programmed to be false")
	}
	if isConditionTrue(conds, "DoesNotExist") {
		t.Fatalf("expected unknown condition type to be false")
	}
}


