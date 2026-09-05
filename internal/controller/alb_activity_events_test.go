// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestHTTPProxyActivityEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		previous []metav1.Condition
		current  []metav1.Condition
		want     []string
	}{
		{
			name:     "first programmed",
			previous: []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionFalse, networkingv1alpha.HTTPProxyReasonPending, 1)},
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionTrue, networkingv1alpha.HTTPProxyReasonProgrammed, 1)},
			want:     []string{EventReasonProgrammed},
		},
		{
			name:     "still programmed is silent",
			previous: []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionTrue, networkingv1alpha.HTTPProxyReasonProgrammed, 1)},
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionTrue, networkingv1alpha.HTTPProxyReasonProgrammed, 1)},
			want:     nil,
		},
		{
			name:     "programmed then failed",
			previous: []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionTrue, networkingv1alpha.HTTPProxyReasonProgrammed, 1)},
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionFalse, networkingv1alpha.HTTPProxyReasonConflict, 2)},
			want:     []string{EventReasonProgrammingFailed},
		},
		{
			name:     "invalid spec",
			previous: []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionAccepted, metav1.ConditionFalse, networkingv1alpha.HTTPProxyReasonPending, 1)},
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionAccepted, metav1.ConditionFalse, networkingv1alpha.HTTPProxyReasonInvalid, 1)},
			want:     []string{EventReasonProgrammingFailed},
		},
		{
			name:     "hostname in use",
			previous: nil,
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionHostnamesInUse, metav1.ConditionTrue, networkingv1alpha.HostnameInUseReason, 1)},
			want:     []string{EventReasonHostnameInUse},
		},
		{
			name:     "unverified once per generation",
			previous: []metav1.Condition{condGen(networkingv1alpha.HTTPProxyConditionHostnamesVerified, metav1.ConditionFalse, networkingv1alpha.UnverifiedHostnamesPresent, 1)},
			current:  []metav1.Condition{condGen(networkingv1alpha.HTTPProxyConditionHostnamesVerified, metav1.ConditionFalse, networkingv1alpha.UnverifiedHostnamesPresent, 1)},
			want:     nil,
		},
		{
			name:     "unverified new generation",
			previous: []metav1.Condition{condGen(networkingv1alpha.HTTPProxyConditionHostnamesVerified, metav1.ConditionFalse, networkingv1alpha.UnverifiedHostnamesPresent, 1)},
			current:  []metav1.Condition{condGen(networkingv1alpha.HTTPProxyConditionHostnamesVerified, metav1.ConditionFalse, networkingv1alpha.UnverifiedHostnamesPresent, 2)},
			want:     []string{EventReasonHostnamesUnverified},
		},
		{
			name:     "certificate issued",
			previous: []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionCertificatesReady, metav1.ConditionFalse, networkingv1alpha.CertificatesReadyReasonCertificatesPending, 1)},
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionCertificatesReady, metav1.ConditionTrue, networkingv1alpha.CertificatesReadyReasonAllCertificatesReady, 1)},
			want:     []string{EventReasonCertificateIssued},
		},
		{
			name:     "certificate failed",
			previous: []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionCertificatesReady, metav1.ConditionFalse, networkingv1alpha.CertificatesReadyReasonCertificatesPending, 1)},
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionCertificatesReady, metav1.ConditionFalse, networkingv1alpha.CertificatesReadyReasonCertificatesFailed, 1)},
			want:     []string{EventReasonCertificateFailed},
		},
		{
			name:     "dns record failed",
			previous: nil,
			current:  []metav1.Condition{cond(networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed, metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, 1)},
			want:     []string{EventReasonDNSRecordFailed},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			events := httpProxyActivityEvents(tt.previous, tt.current, "app.example.com")
			var reasons []string
			for _, ev := range events {
				reasons = append(reasons, ev.Reason)
			}
			assert.Equal(t, tt.want, reasons)
		})
	}
}

func TestTPPWaitingForCertificatesFirstTime(t *testing.T) {
	t.Parallel()

	waiting := &metav1.Condition{
		Type:   string(networkingv1alpha.HTTPProxyConditionAccepted),
		Status: metav1.ConditionFalse,
		Reason: string(PolicyReasonWaitingForCertificates),
	}
	assert.True(t, tppWaitingForCertificatesFirstTime(nil, waiting))
	assert.False(t, tppWaitingForCertificatesFirstTime(waiting, waiting))
}

func TestTPPProgrammingFailed(t *testing.T) {
	t.Parallel()

	live := &metav1.Condition{Status: metav1.ConditionTrue, Reason: string(PolicyReasonProgrammed)}
	partial := &metav1.Condition{Status: metav1.ConditionFalse, Reason: string(PolicyReasonProgrammedPartialFailure)}
	assert.True(t, tppProgrammingFailed(live, partial))
	assert.False(t, tppProgrammingFailed(partial, partial))
	assert.False(t, tppProgrammingFailed(nil, &metav1.Condition{Status: metav1.ConditionFalse, Reason: string(PolicyReasonProgrammedPending)}))
}

func cond(condType string, status metav1.ConditionStatus, reason string, gen int64) metav1.Condition {
	return condGen(condType, status, reason, gen)
}

func condGen(condType string, status metav1.ConditionStatus, reason string, gen int64) metav1.Condition {
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		ObservedGeneration: gen,
	}
}
