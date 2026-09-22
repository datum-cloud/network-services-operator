// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// diagnose walks the load balancer every test in this file builds.
func diagnose(t *testing.T, r *fakeReader) *Diagnosis {
	t.Helper()
	d, err := DiagnoseAt(context.Background(), r, "default", "my-app", testNow)
	require.NoError(t, err)
	return d
}

// TestGreenStatusIsUnverifiedNotWorking is the #457 guard. A load balancer can
// report every condition true and still not be reachable, because Datum
// publishes no signal for whether its edge can serve one. Reporting that as
// health is a wrong answer that reads like a right one.
func TestGreenStatusIsUnverifiedNotWorking(t *testing.T) {
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{healthyProxy()}}
	d := diagnose(t, r)

	assert.Nil(t, d.RootCause)
	assert.True(t, d.Serving)
	assert.Equal(t, ConfidenceUnverified, d.Confidence,
		"nothing confirms the edge can serve this, so the answer is unverified")

	lower := strings.ToLower(d.Summary)
	assert.NotContains(t, lower, "is working", "an unverified result must not claim it works")
	assert.Contains(t, lower, "does not mean",
		"the summary has to say what it is not claiming")
	assert.Contains(t, strings.Join(d.NextSteps, " "), "abc123.datumproxy.net",
		"the one thing that would settle it is a request against the generated hostname")
}

// TestAggregateIsNeverTheAnswer pins the fan-out. PartialFailure says "one or
// more hostnames" and names none; the answer has to name the hostname or it
// sends the customer back to the console to find out which.
func TestAggregateIsNeverTheAnswer(t *testing.T) {
	p := healthyProxy()
	p.Status.Conditions = append(p.Status.Conditions, cond(
		networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		metav1.ConditionFalse, ago(20*time.Minute)))
	p = withHostname(p, networkingv1alpha.HostnameStatus{
		Hostname: "app.example.com",
		Conditions: []metav1.Condition{
			cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonClaimed, metav1.ConditionTrue, ago(25*time.Minute)),
			cond(networkingv1alpha.HostnameConditionDNSRecordProgrammed, networkingv1alpha.DNSRecordReasonConflict, metav1.ConditionFalse, ago(20*time.Minute)),
		},
	})

	r := &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{p},
		domains: []networkingv1alpha.Domain{verifiedDomain()},
	}
	d := diagnose(t, r)

	require.NotNil(t, d.RootCause)
	assert.NotEqual(t, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, d.RootCause.Reason,
		"an aggregate names no hostname and is not an answer")
	assert.Equal(t, networkingv1alpha.DNSRecordReasonConflict, d.RootCause.Reason)
	assert.Equal(t, "app.example.com", d.RootCause.Hostname)
	assert.Contains(t, d.Summary, "app.example.com", "the answer must name the hostname")
}

// TestHostnameCollisionIsFoundNotFilteredOut is the negative-polarity guard at
// the walk level. HostnamesInUse is True when something is wrong, so a naive
// "not True is failing" filter drops a collision entirely and reports health.
func TestHostnameCollisionIsFoundNotFilteredOut(t *testing.T) {
	p := healthyProxy()
	p.Status.Conditions = append(p.Status.Conditions, cond(
		networkingv1alpha.HTTPProxyConditionHostnamesInUse,
		networkingv1alpha.HostnameInUseReason,
		metav1.ConditionTrue, ago(10*time.Minute)))
	p = withHostname(p, networkingv1alpha.HostnameStatus{
		Hostname: "taken.example.com",
		Conditions: []metav1.Condition{
			cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonInUse, metav1.ConditionFalse, ago(10*time.Minute)),
		},
	})

	r := &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{p},
		domains: []networkingv1alpha.Domain{verifiedDomain()},
	}
	d := diagnose(t, r)

	require.NotNil(t, d.RootCause, "a hostname collision must not be filtered out as healthy")
	assert.Equal(t, networkingv1alpha.HostnameAvailableReasonInUse, d.RootCause.Reason)
	assert.Equal(t, ActionabilityUser, d.RootCause.Actionability)
	assert.Contains(t, strings.ToLower(d.RootCause.Remediation), "not visible from here",
		"the holder may be another tenant's, so the copy must not send them hunting for it")
}

// TestCustomerRunsTheirOwnDNSIsNotAFault pins the two reasons the API sets True
// to say a step does not apply. Reporting them as faults invents a problem in
// the one arrangement that needs no fix.
func TestCustomerRunsTheirOwnDNSIsNotAFault(t *testing.T) {
	for _, reason := range []string{
		networkingv1alpha.DNSRecordReasonZoneNotFound,
		networkingv1alpha.DNSRecordReasonNotApplicable,
	} {
		p := withHostname(healthyProxy(), networkingv1alpha.HostnameStatus{
			Hostname: "app.example.com",
			Conditions: []metav1.Condition{
				cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonClaimed, metav1.ConditionTrue, ago(25*time.Minute)),
				cond(networkingv1alpha.HostnameConditionDNSRecordProgrammed, reason, metav1.ConditionTrue, ago(25*time.Minute)),
				cond(networkingv1alpha.HostnameConditionCertificateReady, networkingv1alpha.CertificateReadyReasonCertificateIssued, metav1.ConditionTrue, ago(20*time.Minute)),
			},
		})
		r := &fakeReader{
			proxies: []networkingv1alpha.HTTPProxy{p},
			domains: []networkingv1alpha.Domain{verifiedDomain()},
		}
		d := diagnose(t, r)

		assert.Nil(t, d.RootCause, "%s is not a fault", reason)
		step := stepNamed(t, d, "app.example.com", StepDNSRecord)
		assert.Equal(t, StepNotApplicable, step.State, "%s means the step does not apply", reason)
	}
}

// TestGeneratedHostnameDNSStepIsNotReported is the #458 guard.
//
// This test is written to FAIL once the platform publishes a DNS-record
// condition for generated hostnames. That is deliberate: a fixed platform must
// not keep a silent workaround, and this failing is how anyone finds out.
func TestGeneratedHostnameDNSStepIsNotReported(t *testing.T) {
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{healthyProxy()}}
	d := diagnose(t, r)

	step := stepNamed(t, d, "abc123.datumproxy.net", StepDNSRecord)
	assert.Equal(t, StepNotReported, step.State)
	assert.Contains(t, step.Note, "means nothing either way",
		"absence must read as absence, not as success and not as failure")
	assert.Nil(t, d.RootCause, "an unreported step is not a fault")
}

// TestOwnershipIsReadFromTheDomain pins where domain verification actually
// lives. The API declares a per-hostname Verified condition that no controller
// writes, so waiting on it would wait forever.
func TestOwnershipIsReadFromTheDomain(t *testing.T) {
	p := withHostname(healthyProxy(), networkingv1alpha.HostnameStatus{
		Hostname: "app.example.com",
		Conditions: []metav1.Condition{
			cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonClaimed, metav1.ConditionTrue, ago(25*time.Minute)),
		},
	})

	unverified := verifiedDomain()
	unverified.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.DomainConditionVerified, networkingv1alpha.DomainReasonVerificationRecordNotFound, metav1.ConditionFalse, ago(2*time.Hour)),
	}
	unverified.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
		DNSRecord: networkingv1alpha.DNSVerificationRecord{
			Name: "_datum-challenge.example.com", Type: "TXT", Content: "token-abc",
		},
	}

	r := &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{p},
		domains: []networkingv1alpha.Domain{unverified},
	}
	d := diagnose(t, r)

	step := stepNamed(t, d, "app.example.com", StepOwnership)
	assert.Equal(t, StepFailed, step.State)
	assert.Equal(t, networkingv1alpha.DomainReasonVerificationRecordNotFound, step.Reason)

	require.NotNil(t, d.RootCause)
	assert.Equal(t, networkingv1alpha.DomainReasonVerificationRecordNotFound, d.RootCause.Reason)

	next := strings.Join(d.NextSteps, " ")
	assert.Contains(t, next, "_datum-challenge.example.com", "the exact record is the whole answer")
	assert.Contains(t, next, "token-abc")
}

// TestWidestBlastRadiusWins pins the sort. A traffic protection problem must
// not outrank a load balancer that is serving nothing at all.
func TestWidestBlastRadiusWins(t *testing.T) {
	p := healthyProxy()
	p.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.HTTPProxyConditionProgrammed, networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound, metav1.ConditionFalse, ago(15*time.Minute)),
	}
	p = withHostname(p, networkingv1alpha.HostnameStatus{
		Hostname: "app.example.com",
		Conditions: []metav1.Condition{
			cond(networkingv1alpha.HostnameConditionCertificateReady, networkingv1alpha.CertificateReadyReasonPending, metav1.ConditionFalse, ago(5*time.Minute)),
		},
	})

	r := &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{p},
		domains: []networkingv1alpha.Domain{verifiedDomain()},
	}
	d := diagnose(t, r)

	require.NotNil(t, d.RootCause)
	assert.Equal(t, ScopeAllTraffic, d.RootCause.Scope)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound, d.RootCause.Reason,
		"a dead origin stops everything; a pending certificate stops one hostname")
	assert.NotEmpty(t, d.OtherCauses, "the narrower cause is still reported, just not first")
}

// TestUnreadEvidenceDegradesConfidence pins that a thin answer looks thin. A
// diagnosis that could not read the domains must not present as complete.
func TestUnreadEvidenceDegradesConfidence(t *testing.T) {
	r := &fakeReader{
		proxies:    []networkingv1alpha.HTTPProxy{healthyProxy()},
		domainsErr: assertAnError{},
	}
	d := diagnose(t, r)

	assert.Equal(t, ConfidencePartial, d.Confidence)
	assert.NotEmpty(t, d.Unread)
	assert.Contains(t, strings.Join(d.NextSteps, " "), "Not everything could be checked")
}

// TestNoProtectionIsSurfaced pins the state nothing reports. Creating a load
// balancer in the portal attaches protection best-effort, so a load balancer
// with none is common and no condition anywhere says so.
func TestNoProtectionIsSurfaced(t *testing.T) {
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{healthyProxy()}}
	d := diagnose(t, r)

	assert.False(t, d.Protection.Attached)
	assert.Contains(t, strings.Join(d.NextSteps, " "), "Nothing is inspecting traffic")

	withTPP := &fakeReader{
		proxies:  []networkingv1alpha.HTTPProxy{healthyProxy()},
		policies: []networkingv1alpha.TrafficProtectionPolicy{tppFor("my-app", "Enforce")},
	}
	d2 := diagnose(t, withTPP)
	assert.True(t, d2.Protection.Attached)
	assert.Equal(t, "Enforce", d2.Protection.Mode)
	assert.NotContains(t, strings.Join(d2.NextSteps, " "), "Nothing is inspecting traffic")
}

// TestEpochTimestampDoesNotStallAFreshLoadBalancer pins the sentinel guard
// through the whole walk, not just the catalog.
func TestEpochTimestampDoesNotStallAFreshLoadBalancer(t *testing.T) {
	p := healthyProxy()
	p.CreationTimestamp = ago(10 * time.Second)
	epoch := metav1.NewTime(time.Unix(0, 0).UTC())
	p.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.HTTPProxyConditionProgrammed, networkingv1alpha.HTTPProxyReasonPending, metav1.ConditionUnknown, epoch),
	}

	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{p}}
	d := diagnose(t, r)

	require.NotNil(t, d.RootCause)
	assert.Equal(t, ActionabilityTransient, d.RootCause.Actionability,
		"a ten-second-old load balancer has not stalled")
	assert.Empty(t, d.RootCause.InStateFor, "the epoch is a placeholder, not an age")
	assert.NotEmpty(t, d.RootCause.TimeDiscarded, "say that a timestamp was thrown away")
}

type assertAnError struct{}

func (assertAnError) Error() string { return "reading domains failed" }

// verifiedDomain is the domain behind every custom hostname these tests use.
func verifiedDomain() networkingv1alpha.Domain {
	const name = "example.com"
	return networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Name: strings.ReplaceAll(name, ".", "-"), Namespace: "default"},
		Spec:       networkingv1alpha.DomainSpec{DomainName: name},
		Status: networkingv1alpha.DomainStatus{
			Conditions: []metav1.Condition{
				cond(networkingv1alpha.DomainConditionVerified, networkingv1alpha.DomainReasonVerified, metav1.ConditionTrue, ago(3*time.Hour)),
			},
		},
	}
}

func stepNamed(t *testing.T, d *Diagnosis, hostname, step string) HostnameStep {
	t.Helper()
	for _, h := range d.Hostnames {
		if h.Hostname != hostname {
			continue
		}
		for _, s := range h.Steps {
			if s.Step == step {
				return s
			}
		}
		t.Fatalf("hostname %s has no %s step", hostname, step)
	}
	t.Fatalf("no hostname %s in the diagnosis", hostname)
	return HostnameStep{}
}

// TestNotStartedIsAnAnswerButTheWeakestOne pins both halves. A load balancer
// nothing has looked at must not report as healthy, and "nothing has run yet"
// must not outrank a cause that names something.
func TestNotStartedIsAnAnswerButTheWeakestOne(t *testing.T) {
	p := healthyProxy()
	p.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.HTTPProxyConditionAccepted, networkingv1alpha.HTTPProxyReasonPending, metav1.ConditionUnknown, ago(1*time.Minute)),
	}
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{p}}
	d := diagnose(t, r)

	require.NotNil(t, d.RootCause, "a load balancer nothing has evaluated is not healthy")
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, d.RootCause.Reason)

	// Now add a cause that names something, at the same scope.
	p.Status.Conditions = append(p.Status.Conditions, cond(
		networkingv1alpha.HTTPProxyConditionProgrammed,
		networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound,
		metav1.ConditionFalse, ago(1*time.Minute)))
	r2 := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{p}}
	d2 := diagnose(t, r2)

	require.NotNil(t, d2.RootCause)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound, d2.RootCause.Reason,
		"a named cause outranks 'nothing has run yet'")
}

// TestACannotProgramPendingIsNotSomethingToWaitFor is the case a naive reading
// gets exactly backwards.
//
// The controller reports a configuration it cannot assemble with the same
// Pending reason it uses for work it has not started, and the load balancer
// goes on serving what it published last. So the change looks accepted, is not
// applied, and the only thing that says so is a message. Telling the customer
// to wait is wrong advice that sounds reassuring; nothing is coming.
func TestACannotProgramPendingIsNotSomethingToWaitFor(t *testing.T) {
	conflict := "The HTTPProxy cannot be programmed: backend 1 in rule 0 needs Host header " +
		"rewritten to \"b.example.com\", which conflicts with another backend in the same rule " +
		"that needs \"a.example.com\"; backends sharing a rule must resolve to the same Host " +
		"rewrite target. Set a Host header override on the rule so every backend agrees, or give " +
		"each backend its own rule"

	p := healthyProxy()
	p.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.HTTPProxyConditionAccepted, networkingv1alpha.HTTPProxyReasonAccepted, metav1.ConditionTrue, ago(time.Hour)),
		{
			Type:               networkingv1alpha.HTTPProxyConditionProgrammed,
			Status:             metav1.ConditionFalse,
			Reason:             networkingv1alpha.HTTPProxyReasonPending,
			Message:            conflict,
			LastTransitionTime: ago(3 * time.Hour),
		},
	}

	d := diagnose(t, &fakeReader{proxies: []networkingv1alpha.HTTPProxy{p}})

	require.NotNil(t, d.RootCause)
	assert.Equal(t, ReasonCannotProgram, d.RootCause.Reason,
		"the message says Datum gave up, so this is not the same Pending as work not yet started")
	assert.Equal(t, ActionabilityUser, d.RootCause.Actionability,
		"a conflict in the settings is the customer's to resolve")
	assert.NotEqual(t, ActionabilityTransient, d.RootCause.Actionability)

	assert.Contains(t, strings.Join(d.NextSteps, " "), "Waiting will not clear it")
	assert.Contains(t, d.RootCause.Message, "conflicts with another backend",
		"the real cause is in the message and has to survive into the answer")

	// The ordinary Pending must still read as something to wait for.
	p2 := healthyProxy()
	p2.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.HTTPProxyConditionProgrammed, networkingv1alpha.HTTPProxyReasonPending, metav1.ConditionFalse, ago(time.Minute)),
	}
	d2 := diagnose(t, &fakeReader{proxies: []networkingv1alpha.HTTPProxy{p2}})
	require.NotNil(t, d2.RootCause)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, d2.RootCause.Reason)
	assert.Equal(t, ActionabilityTransient, d2.RootCause.Actionability)
}
