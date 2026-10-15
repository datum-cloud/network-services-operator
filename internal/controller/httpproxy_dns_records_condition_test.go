// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const proxyGeneration = 7

func dnsRecordsCondition(status metav1.ConditionStatus, reason string, generation int64) metav1.Condition {
	return metav1.Condition{
		Type: networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed, Status: status, Reason: reason,
		Message: "1/2 hostnames have DNS records programmed", ObservedGeneration: generation,
	}
}

func TestDNSRecordsProgrammedFromGateway(t *testing.T) {
	gatewayWith := func(conditions ...metav1.Condition) *gatewayv1.Gateway {
		gw := &gatewayv1.Gateway{}
		gw.Generation = 3
		gw.Status.Conditions = conditions
		return gw
	}
	pending := dnsRecordsPending(proxyGeneration)
	cases := []struct {
		name    string
		gateway *gatewayv1.Gateway
		want    *metav1.Condition
	}{
		{"no hostname uses Datum DNS: no condition", gatewayWith(), nil},
		{"a refused record: False carried with its reason and message",
			gatewayWith(dnsRecordsCondition(metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, 3)),
			ptrTo(dnsRecordsCondition(metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, proxyGeneration))},
		{"every record programmed: True carried",
			gatewayWith(dnsRecordsCondition(metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, 3)),
			ptrTo(dnsRecordsCondition(metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, proxyGeneration))},
		{"written for an earlier Gateway generation: Unknown, never True",
			gatewayWith(dnsRecordsCondition(metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, 2)),
			pending},
		{"a reason this code does not name, as a held record reports: carried as it is",
			gatewayWith(dnsRecordsCondition(metav1.ConditionFalse, "DNSAuthorityMissing", 3)),
			ptrTo(dnsRecordsCondition(metav1.ConditionFalse, "DNSAuthorityMissing", proxyGeneration))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dnsRecordsProgrammedFromGateway(tc.gateway, proxyGeneration))
		})
	}
}

func TestDNSRecordsUnread(t *testing.T) {
	failed := dnsRecordsCondition(metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, 5)
	cases := []struct {
		name string
		held []metav1.Condition
		want *metav1.Condition
	}{
		{"held none: none", nil, nil},
		{"held True: Unknown, so it never outlives its read",
			[]metav1.Condition{dnsRecordsCondition(metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, 5)},
			dnsRecordsPending(proxyGeneration)},
		{"held False: kept, so the failure is not reported again", []metav1.Condition{failed}, &failed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dnsRecordsUnread(tc.held, proxyGeneration))
		})
	}
}

func TestSetDNSRecordsProgrammedRemovesOnNil(t *testing.T) {
	conditions := []metav1.Condition{dnsRecordsCondition(metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, 1)}
	setDNSRecordsProgrammed(&conditions, nil, nil)
	require.Empty(t, conditions)
}

// A Gateway left with no Datum-managed hostname keeps the aggregate it wrote
// for an earlier generation, so the HTTPProxy decides Pending by its own
// hostnames; anything the Gateway reports for its current generation is carried.
func TestSetDNSRecordsProgrammedDropsPendingWithNoRecord(t *testing.T) {
	withRecord := []networkingv1alpha.HostnameStatus{{Hostname: "app.example.test", Conditions: []metav1.Condition{{
		Type: networkingv1alpha.HostnameConditionDNSRecordProgrammed, Status: metav1.ConditionTrue,
		Reason: networkingv1alpha.DNSRecordReasonCreated,
	}}}}
	withoutRecord := []networkingv1alpha.HostnameStatus{{Hostname: "app.example.test"}}
	allCreated := dnsRecordsCondition(metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, proxyGeneration)
	cases := []struct {
		name      string
		cond      *metav1.Condition
		hostnames []networkingv1alpha.HostnameStatus
		want      bool
	}{
		{"Pending, no hostname has a DNS record: removed", dnsRecordsPending(proxyGeneration), withoutRecord, false},
		{"Pending, no hostname at all: removed", dnsRecordsPending(proxyGeneration), nil, false},
		{"Pending, a hostname has a DNS record: kept", dnsRecordsPending(proxyGeneration), withRecord, true},
		{"True, no hostname has a DNS record yet: kept", &allCreated, withoutRecord, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var conditions []metav1.Condition
			setDNSRecordsProgrammed(&conditions, tc.cond, tc.hostnames)
			require.Equal(t, tc.want, len(conditions) == 1)
		})
	}
}

func ptrTo(c metav1.Condition) *metav1.Condition { return &c }
