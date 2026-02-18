// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const testMutatedHostname = "mutated.example.com"

// TestHostnameStatus_DeepCopy verifies that DeepCopy produces an independent
// copy of a HostnameStatus value with all nested slices properly duplicated.
func TestHostnameStatus_DeepCopy(t *testing.T) {
	t.Parallel()

	original := networkingv1alpha.HostnameStatus{
		Hostname: "api.example.com",
		Conditions: []metav1.Condition{
			{
				Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
				Status:             metav1.ConditionTrue,
				Reason:             networkingv1alpha.DNSRecordReasonCreated,
				Message:            "cname record created in DNSZone \"example-com\"",
				ObservedGeneration: 1,
			},
			{
				Type:               networkingv1alpha.HostnameConditionVerified,
				Status:             metav1.ConditionTrue,
				Reason:             "DomainVerified",
				ObservedGeneration: 1,
			},
		},
	}

	copied := original.DeepCopy()
	require.NotNil(t, copied)

	// Values must be equal.
	assert.Equal(t, original.Hostname, copied.Hostname)
	assert.Equal(t, original.Conditions, copied.Conditions)

	// Mutating the copy must not affect the original.
	copied.Hostname = testMutatedHostname
	copied.Conditions[0].Message = "mutated message"
	copied.Conditions = append(copied.Conditions, metav1.Condition{Type: "Extra"})

	assert.Equal(t, "api.example.com", original.Hostname)
	assert.Equal(t, "cname record created in DNSZone \"example-com\"", original.Conditions[0].Message)
	assert.Len(t, original.Conditions, 2, "original conditions should not gain extra element")
}

// TestHostnameStatus_DeepCopy_Nil verifies that DeepCopy on a nil pointer
// returns nil rather than panicking.
func TestHostnameStatus_DeepCopy_Nil(t *testing.T) {
	t.Parallel()

	var hs *networkingv1alpha.HostnameStatus
	assert.Nil(t, hs.DeepCopy())
}

// TestHostnameStatus_DeepCopy_EmptyConditions verifies that a HostnameStatus
// with an empty (but non-nil) Conditions slice is deep-copied correctly.
func TestHostnameStatus_DeepCopy_EmptyConditions(t *testing.T) {
	t.Parallel()

	original := networkingv1alpha.HostnameStatus{
		Hostname:   "example.com",
		Conditions: []metav1.Condition{},
	}
	copied := original.DeepCopy()
	require.NotNil(t, copied)
	assert.Equal(t, original.Hostname, copied.Hostname)
	assert.NotNil(t, copied.Conditions, "empty slice should be preserved as non-nil")
}

// TestHTTPProxyStatus_DeepCopy verifies that HTTPProxyStatus deep-copies all
// nested slices correctly.
func TestHTTPProxyStatus_DeepCopy(t *testing.T) {
	t.Parallel()

	original := networkingv1alpha.HTTPProxyStatus{
		CanonicalHostname: "abc123.gateways.test.local",
		Addresses: []gatewayv1.GatewayStatusAddress{
			{
				Type:  ptr.To(gatewayv1.HostnameAddressType),
				Value: "abc123.gateways.test.local",
			},
			{
				Type:  ptr.To(gatewayv1.IPAddressType),
				Value: "192.168.1.10",
			},
		},
		HostnameStatuses: []networkingv1alpha.HostnameStatus{
			{
				Hostname: "api.example.com",
				Conditions: []metav1.Condition{
					{
						Type:   networkingv1alpha.HostnameConditionDNSRecordProgrammed,
						Status: metav1.ConditionTrue,
						Reason: networkingv1alpha.DNSRecordReasonCreated,
					},
				},
			},
		},
		Conditions: []metav1.Condition{
			{
				Type:   networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
				Status: metav1.ConditionTrue,
				Reason: networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
			},
		},
	}

	copied := original.DeepCopy()
	require.NotNil(t, copied)

	// Top-level value equality.
	assert.Equal(t, original.CanonicalHostname, copied.CanonicalHostname)
	assert.Equal(t, original.Addresses, copied.Addresses)
	assert.Equal(t, original.HostnameStatuses, copied.HostnameStatuses)
	assert.Equal(t, original.Conditions, copied.Conditions)

	// Mutation independence – addresses.
	copied.Addresses[0].Value = "mutated"
	assert.Equal(t, "abc123.gateways.test.local", original.Addresses[0].Value)

	// Mutation independence – hostname statuses.
	copied.HostnameStatuses[0].Hostname = testMutatedHostname
	assert.Equal(t, "api.example.com", original.HostnameStatuses[0].Hostname)

	// Mutation independence – conditions.
	copied.Conditions[0].Reason = "Mutated"
	assert.Equal(t, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, original.Conditions[0].Reason)
}

// TestHTTPProxyStatus_DeepCopy_Nil verifies that DeepCopy on a nil pointer
// returns nil rather than panicking.
func TestHTTPProxyStatus_DeepCopy_Nil(t *testing.T) {
	t.Parallel()

	var s *networkingv1alpha.HTTPProxyStatus
	assert.Nil(t, s.DeepCopy())
}

// TestHTTPProxy_DeepCopy verifies that the full HTTPProxy object is deep-copied
// correctly (round-trip: copy, mutate, verify original unchanged).
func TestHTTPProxy_DeepCopy(t *testing.T) {
	t.Parallel()

	original := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-proxy",
			Namespace: "default",
		},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: []gatewayv1.Hostname{"api.example.com", "example.com"},
		},
		Status: networkingv1alpha.HTTPProxyStatus{
			CanonicalHostname: "abc123.gateways.test.local",
			HostnameStatuses: []networkingv1alpha.HostnameStatus{
				{
					Hostname: "api.example.com",
					Conditions: []metav1.Condition{
						{
							Type:   networkingv1alpha.HostnameConditionDNSRecordProgrammed,
							Status: metav1.ConditionTrue,
							Reason: networkingv1alpha.DNSRecordReasonCreated,
						},
					},
				},
			},
		},
	}

	copied := original.DeepCopy()
	require.NotNil(t, copied)

	// Value equality.
	assert.Equal(t, original.Name, copied.Name)
	assert.Equal(t, original.Namespace, copied.Namespace)
	assert.Equal(t, original.Spec.Hostnames, copied.Spec.Hostnames)
	assert.Equal(t, original.Status.CanonicalHostname, copied.Status.CanonicalHostname)
	assert.Equal(t, original.Status.HostnameStatuses, copied.Status.HostnameStatuses)

	// Mutation independence.
	copied.Status.CanonicalHostname = "mutated"
	assert.Equal(t, "abc123.gateways.test.local", original.Status.CanonicalHostname)

	copied.Status.HostnameStatuses[0].Hostname = testMutatedHostname
	assert.Equal(t, "api.example.com", original.Status.HostnameStatuses[0].Hostname)

	copied.Spec.Hostnames[0] = testMutatedHostname
	assert.Equal(t, gatewayv1.Hostname("api.example.com"), original.Spec.Hostnames[0])
}

// TestConditionConstants verifies that the expected condition constant strings
// match the documented API values, preventing accidental renames.
func TestConditionConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "DNSRecordsProgrammed", networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
	assert.Equal(t, "Verified", networkingv1alpha.HostnameConditionVerified)
	assert.Equal(t, "DNSRecordProgrammed", networkingv1alpha.HostnameConditionDNSRecordProgrammed)

	// Reason constants
	assert.Equal(t, "RecordCreated", networkingv1alpha.DNSRecordReasonCreated)
	assert.Equal(t, "RecordUpdated", networkingv1alpha.DNSRecordReasonUpdated)
	assert.Equal(t, "DNSZoneNotFound", networkingv1alpha.DNSRecordReasonZoneNotFound)
	assert.Equal(t, "DNSZoneNotReady", networkingv1alpha.DNSRecordReasonZoneNotReady)
	assert.Equal(t, "DomainNotVerified", networkingv1alpha.DNSRecordReasonDomainNotVerified)
	assert.Equal(t, "ConflictWithUserRecord", networkingv1alpha.DNSRecordReasonConflict)
	assert.Equal(t, "RecordCreationFailed", networkingv1alpha.DNSRecordReasonFailed)
	assert.Equal(t, "RetryPending", networkingv1alpha.DNSRecordReasonRetryPending)
	assert.Equal(t, "NotApplicable", networkingv1alpha.DNSRecordReasonNotApplicable)

	assert.Equal(t, "AllRecordsCreated", networkingv1alpha.DNSRecordsProgrammedReasonAllCreated)
	assert.Equal(t, "AllApplicableRecordsCreated", networkingv1alpha.DNSRecordsProgrammedReasonAllApplicableCreated)
	assert.Equal(t, "PartialFailure", networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure)
}
