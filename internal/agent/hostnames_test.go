// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func hostnameWithRecords(
	dnsCondition *metav1.Condition,
	records ...networkingv1alpha.HostnameDNSRecord,
) *fakeReader {
	conditions := []metav1.Condition{
		cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonClaimed, metav1.ConditionTrue, ago(25*time.Minute)),
		cond(networkingv1alpha.HostnameConditionCertificateReady, networkingv1alpha.CertificateReadyReasonCertificateIssued, metav1.ConditionTrue, ago(20*time.Minute)),
	}
	if dnsCondition != nil {
		conditions = append(conditions, *dnsCondition)
	}
	p := withHostname(healthyProxy(), networkingv1alpha.HostnameStatus{
		Hostname:   "app.example.com",
		Conditions: conditions,
		DNSRecords: records,
	})
	return &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{p},
		domains: []networkingv1alpha.Domain{verifiedDomain()},
	}
}

func routingRecord(by networkingv1alpha.HostnameDNSRecordManager, state networkingv1alpha.HostnameDNSRecordState) networkingv1alpha.HostnameDNSRecord {
	return networkingv1alpha.HostnameDNSRecord{
		Name:      "app.example.com",
		Type:      "CNAME",
		Content:   "abc123.datumproxy.net",
		Purpose:   networkingv1alpha.HostnameDNSRecordPurposeRouting,
		ManagedBy: by,
		State:     state,
	}
}

func certificateRecord(state networkingv1alpha.HostnameDNSRecordState) networkingv1alpha.HostnameDNSRecord {
	return networkingv1alpha.HostnameDNSRecord{
		Name:      "_acme-challenge.app.example.com",
		Type:      "CNAME",
		Content:   "app-example-com.challenges.example.net",
		Purpose:   networkingv1alpha.HostnameDNSRecordPurposeCertificate,
		ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser,
		State:     state,
	}
}

func ownershipRecord() networkingv1alpha.HostnameDNSRecord {
	return networkingv1alpha.HostnameDNSRecord{
		Name:      "_datum-challenge.example.com",
		Type:      "TXT",
		Content:   "token-abc",
		Purpose:   networkingv1alpha.HostnameDNSRecordPurposeOwnership,
		ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser,
		State:     networkingv1alpha.HostnameDNSRecordMissing,
	}
}

func dnsCondition(reason string, status metav1.ConditionStatus) *metav1.Condition {
	c := cond(networkingv1alpha.HostnameConditionDNSRecordProgrammed, reason, status, ago(20*time.Minute))
	return &c
}

func TestPresentRecordsMakeTheDNSStepDone(t *testing.T) {
	for name, c := range map[string]*metav1.Condition{
		"no condition":         nil,
		"customer runs DNS":    dnsCondition(networkingv1alpha.DNSRecordReasonNotApplicable, metav1.ConditionTrue),
		"zone not found here":  dnsCondition(networkingv1alpha.DNSRecordReasonZoneNotFound, metav1.ConditionTrue),
		"datum wrote a record": dnsCondition(networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
	} {
		t.Run(name, func(t *testing.T) {
			d := diagnose(t, hostnameWithRecords(c,
				routingRecord(networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordPresent),
				certificateRecord(networkingv1alpha.HostnameDNSRecordPresent),
			))

			step := stepNamed(t, d, "app.example.com", StepDNSRecord)
			assert.Equal(t, StepOK, step.State)
			assert.Nil(t, d.RootCause, "a hostname whose records are all in place has nothing wrong with it")
		})
	}
}

func TestAMissingRecordOfTheCustomersIsTheirNextStep(t *testing.T) {
	for name, c := range map[string]*metav1.Condition{
		"no condition":      nil,
		"customer runs DNS": dnsCondition(networkingv1alpha.DNSRecordReasonNotApplicable, metav1.ConditionTrue),
	} {
		t.Run(name, func(t *testing.T) {
			d := diagnose(t, hostnameWithRecords(c,
				routingRecord(networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordMissing),
				certificateRecord(networkingv1alpha.HostnameDNSRecordMissing),
			))

			step := stepNamed(t, d, "app.example.com", StepDNSRecord)
			assert.Equal(t, StepFailed, step.State)
			assert.Equal(t, ReasonRecordNotPublished, step.Reason)
			assert.Contains(t, step.Note, "abc123.datumproxy.net", "the step names the value to publish")
			assert.Contains(t, step.Note, "_acme-challenge.app.example.com")

			require.NotNil(t, d.RootCause, "a record the customer has not published is a fault")
			assert.Equal(t, ReasonRecordNotPublished, d.RootCause.Reason)
			assert.Equal(t, networkingv1alpha.HostnameConditionDNSRecordProgrammed, d.RootCause.ConditionType)
			assert.Equal(t, "app.example.com", d.RootCause.Hostname)
			assert.Equal(t, ActionabilityUser, d.RootCause.Actionability)
			assert.Equal(t, ScopeOneHostname, d.RootCause.Scope)
			assert.False(t, d.Serving)

			next := strings.Join(d.NextSteps, " ")
			assert.Contains(t, next, `name "app.example.com", type CNAME, value "abc123.datumproxy.net"`)
			assert.Contains(t, next, `name "_acme-challenge.app.example.com", type CNAME, value "app-example-com.challenges.example.net"`)
		})
	}
}

func TestOnlyMissingRecordsAreNextSteps(t *testing.T) {
	d := diagnose(t, hostnameWithRecords(nil,
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordMissing),
		certificateRecord(networkingv1alpha.HostnameDNSRecordPresent),
	))

	step := stepNamed(t, d, "app.example.com", StepDNSRecord)
	assert.NotContains(t, step.Note, "_acme-challenge")
	assert.NotContains(t, strings.Join(d.NextSteps, " "), "_acme-challenge")
}

func TestAMissingRecordOfDatumsIsPending(t *testing.T) {
	fresh := cond(networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue, ago(5*time.Minute))
	d := diagnose(t, hostnameWithRecords(
		&fresh,
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByPlatform, networkingv1alpha.HostnameDNSRecordMissing),
	))

	step := stepNamed(t, d, "app.example.com", StepDNSRecord)
	assert.Equal(t, StepPending, step.State)

	require.NotNil(t, d.RootCause)
	assert.Equal(t, ReasonRecordNotInEffect, d.RootCause.Reason)
	assert.Equal(t, ActionabilityTransient, d.RootCause.Actionability)
	assert.NotContains(t, strings.Join(d.NextSteps, " "), "Create this DNS record",
		"a record Datum publishes is not the customer's to create")
}

func TestAFailingConditionExplainsDatumsMissingRecord(t *testing.T) {
	d := diagnose(t, hostnameWithRecords(
		dnsCondition(networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, metav1.ConditionFalse),
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByPlatform, networkingv1alpha.HostnameDNSRecordMissing),
	))

	step := stepNamed(t, d, "app.example.com", StepDNSRecord)
	assert.Equal(t, StepFailed, step.State)
	assert.Equal(t, networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, step.Reason)

	require.NotNil(t, d.RootCause)
	assert.Equal(t, networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, d.RootCause.Reason)
}

func TestOwnershipRecordsBelongToTheOwnershipStep(t *testing.T) {
	d := diagnose(t, hostnameWithRecords(
		dnsCondition(networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
		ownershipRecord(),
	))
	assert.Equal(t, StepOK, stepNamed(t, d, "app.example.com", StepDNSRecord).State,
		"with only an ownership record, the condition decides")

	d = diagnose(t, hostnameWithRecords(nil, ownershipRecord()))
	assert.Equal(t, StepNotReported, stepNamed(t, d, "app.example.com", StepDNSRecord).State)
}

func TestNoRecordsAndNoConditionIsNotReported(t *testing.T) {
	d := diagnose(t, hostnameWithRecords(nil))

	step := stepNamed(t, d, "app.example.com", StepDNSRecord)
	assert.Equal(t, StepNotReported, step.State)
	assert.Nil(t, d.RootCause)
}

func TestAStaleMissingRecordOfDatumsStalls(t *testing.T) {
	old := dnsCondition(networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue)
	d := diagnose(t, hostnameWithRecords(old,
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByPlatform, networkingv1alpha.HostnameDNSRecordMissing),
	))
	require.NotNil(t, d.RootCause)
	assert.Equal(t, ReasonRecordNotInEffect, d.RootCause.Reason)
	assert.Equal(t, ActionabilityStalled, d.RootCause.Actionability)

	d = diagnose(t, hostnameWithRecords(nil,
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByPlatform, networkingv1alpha.HostnameDNSRecordMissing),
	))
	require.NotNil(t, d.RootCause)
	assert.Equal(t, ActionabilityStalled, d.RootCause.Actionability,
		"with no condition the load balancer's age bounds the wait")

	fresh := cond(networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue, ago(5*time.Minute))
	d = diagnose(t, hostnameWithRecords(&fresh,
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByPlatform, networkingv1alpha.HostnameDNSRecordMissing),
	))
	require.NotNil(t, d.RootCause)
	assert.Equal(t, ActionabilityTransient, d.RootCause.Actionability)
}

func TestAnUnknownRecordStateIsNotACustomerAction(t *testing.T) {
	d := diagnose(t, hostnameWithRecords(
		dnsCondition(networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
		routingRecord(networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordState("Unheard")),
	))
	assert.Equal(t, StepOK, stepNamed(t, d, "app.example.com", StepDNSRecord).State)
	if d.RootCause != nil {
		assert.NotEqual(t, ReasonRecordNotPublished, d.RootCause.Reason)
	}
}
