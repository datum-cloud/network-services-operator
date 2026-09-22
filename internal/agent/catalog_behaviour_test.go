// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// TestHostnamesInUseIsReadAsNegativePolarity guards the easiest mistake in this
// package. HostnamesInUse is True when a hostname collides, so the usual
// "not True means failing" reading marks a collision healthy and drops it.
func TestHostnamesInUseIsReadAsNegativePolarity(t *testing.T) {
	assert.True(t, Failing(networkingv1alpha.HTTPProxyConditionHostnamesInUse, metav1.ConditionTrue),
		"a hostname collision is reported by this condition being True")
	assert.False(t, Failing(networkingv1alpha.HTTPProxyConditionHostnamesInUse, metav1.ConditionFalse),
		"no collision is this condition being False")

	// Every other condition reads the ordinary way round.
	assert.False(t, Failing(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionTrue))
	assert.True(t, Failing(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionFalse))
	assert.True(t, Failing(networkingv1alpha.HTTPProxyConditionProgrammed, metav1.ConditionUnknown))
}

// TestConditionsThatAreTrueOnPurposeAreNotFailures pins the two reasons the API
// sets True to say a step does not apply. Reporting either as a fault sends a
// customer hunting for a problem in the one arrangement that needs no fix, and
// telling them to wait would be worse: nothing is coming.
func TestConditionsThatAreTrueOnPurposeAreNotFailures(t *testing.T) {
	for _, reason := range []string{
		networkingv1alpha.DNSRecordReasonZoneNotFound,
		networkingv1alpha.DNSRecordReasonNotApplicable,
	} {
		info, ok := ExplainReason(networkingv1alpha.HostnameConditionDNSRecordProgrammed, reason)
		require.True(t, ok, "%s must be catalogued", reason)
		assert.Equal(t, ActionabilityInformational, info.Actionability,
			"%s is set True on purpose; it is not a fault and it is not something to wait for", reason)
		assert.Zero(t, info.ExpectedDuration, "%s must not carry a window: nothing is in flight", reason)
		assert.Contains(t, info.Remediation, "CNAME",
			"%s should tell the customer the one thing that does apply to them", reason)
	}
}

// TestTransientReasonsThatSayWaitDeclareAWindow keeps "wait" falsifiable. A
// transient claim nobody can disprove is how a wedged load balancer reads as
// healthy.
func TestTransientReasonsThatSayWaitDeclareAWindow(t *testing.T) {
	for _, info := range AllReasons() {
		if info.Actionability != ActionabilityTransient {
			continue
		}
		assert.NotZero(t, info.ExpectedDuration,
			"%s on %s says to wait but not how long is reasonable",
			info.Reason, info.ConditionType)
		assert.NotEmpty(t, info.ExpectedWithin,
			"%s on %s has a window that was never rendered for a reader",
			info.Reason, info.ConditionType)
	}
}

// TestDomainVerificationIsNotTransient pins a deliberate classification. Domain
// verification waits on a person creating a DNS record, so a window on it would
// be a claim about the customer's speed dressed up as a claim about Datum's.
func TestDomainVerificationIsNotTransient(t *testing.T) {
	info, ok := ExplainReason(networkingv1alpha.DomainConditionVerified, networkingv1alpha.DomainReasonPendingVerification)
	require.True(t, ok)
	assert.Equal(t, ActionabilityUser, info.Actionability)
	assert.Zero(t, info.ExpectedDuration)
}

// TestActionabilityAtIgnoresTheEpochSentinel pins the guard behind every age in
// this package. Both HTTPProxy and Domain default lastTransitionTime to the
// Unix epoch, so without it a load balancer created seconds ago reads as having
// been stuck for twenty thousand days.
func TestActionabilityAtIgnoresTheEpochSentinel(t *testing.T) {
	info, ok := ExplainReason(networkingv1alpha.HostnameConditionDNSRecordProgrammed, networkingv1alpha.DNSRecordReasonPending)
	require.True(t, ok)
	require.Equal(t, ActionabilityTransient, info.Actionability)

	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, ActionabilityTransient, ActionabilityAt(info, "1970-01-01T00:00:00Z", now),
		"the epoch is a placeholder, not an observation twenty thousand days old")
	assert.Equal(t, ActionabilityTransient, ActionabilityAt(info, "", now),
		"no timestamp is not evidence of a stall")
	assert.Equal(t, ActionabilityTransient, ActionabilityAt(info, "not a time", now))
	assert.Equal(t, ActionabilityTransient, ActionabilityAt(info, now.Add(time.Hour).Format(time.RFC3339), now),
		"a future timestamp is not evidence of a stall either")

	assert.Equal(t, ActionabilityTransient,
		ActionabilityAt(info, now.Add(-time.Minute).Format(time.RFC3339), now),
		"inside the window this is still normal")
	assert.Equal(t, ActionabilityStalled,
		ActionabilityAt(info, now.Add(-24*time.Hour).Format(time.RFC3339), now),
		"past the window it has outlived the claim that it clears on its own")
}

// TestAggregatesAreNeverAnswers pins that the conditions reporting over a set
// without naming a member are marked so the walk descends past them.
func TestAggregatesAreNeverAnswers(t *testing.T) {
	for _, tc := range []struct{ conditionType, reason string }{
		{networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure},
		{networkingv1alpha.HTTPProxyConditionCertificatesReady, networkingv1alpha.CertificatesReadyReasonCertificatesPending},
		{networkingv1alpha.HTTPProxyConditionCertificatesReady, networkingv1alpha.CertificatesReadyReasonCertificatesFailed},
		{networkingv1alpha.HTTPProxyConditionHostnamesVerified, networkingv1alpha.UnverifiedHostnamesPresent},
		{networkingv1alpha.HostnameConditionDNSRecordProgrammed, networkingv1alpha.DNSRecordReasonDomainNotVerified},
	} {
		assert.True(t, IsAggregate(tc.conditionType, tc.reason),
			"%s on %s reports over a set and names no member", tc.reason, tc.conditionType)
	}

	// A cause that names something specific must NOT be marked an aggregate, or
	// the walk descends past a real answer and reports nothing.
	assert.False(t, IsAggregate(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonInUse),
		"a hostname collision names the hostname; it is terminal, not a pointer")
	assert.False(t, IsAggregate(networkingv1alpha.HTTPProxyConditionAccepted, networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound))
}

// TestEveryFailingReasonNamesSomebodyAndSomething keeps the catalog usable: a
// reason that says something is wrong but neither what to do nor which
// procedure covers it is a dead end for whoever reads it.
func TestEveryFailingReasonNamesSomebodyAndSomething(t *testing.T) {
	for _, info := range AllReasons() {
		if info.Actionability == ActionabilityInformational && info.Scope == "" {
			continue
		}
		assert.NotEmpty(t, info.Explanation, "%s on %s explains nothing", info.Reason, info.ConditionType)
		assert.NotEmpty(t, info.Remediation, "%s on %s says nothing to do", info.Reason, info.ConditionType)
		if info.Actionability != ActionabilityInformational {
			assert.NotEmpty(t, info.Scope, "%s on %s does not say how much stops working", info.Reason, info.ConditionType)
		}
	}
}
