// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"go.miloapis.com/ipam/pkg/ipamerrors"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

var ipclaimsResource = schema.GroupResource{Group: "ipam.miloapis.com", Resource: "ipclaims"}

// fromTheWire is the error the controller actually classifies: the refusal as
// it arrives after the apiserver serialised it and client-go decoded it. A
// classifier proven only against an in-process value proves nothing.
func fromTheWire(t *testing.T, err *apierrors.StatusError) error {
	t.Helper()
	data, marshalErr := json.Marshal(err.ErrStatus)
	require.NoError(t, marshalErr)
	var status metav1.Status
	require.NoError(t, json.Unmarshal(data, &status))
	return &apierrors.StatusError{ErrStatus: status}
}

func v4Request() allocationRequest {
	return allocationRequest{family: networkingv1alpha.IPv4Protocol}
}

func classRequest() allocationRequest {
	return allocationRequest{className: "public-v4", family: networkingv1alpha.IPv4Protocol}
}

// The condition reasons are our API surface. IPAM's taxonomy is finer than
// ours, and this is where the two are pinned together.
func TestClassifyAllocationFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want allocationFailureReason
	}{
		{"pool exhausted", ipamerrors.NewPoolExhausted("datum-v4", `IPPool "datum-v4" is exhausted`), allocationFailureExhausted},
		{"ancestor exhausted", ipamerrors.New(ipamerrors.ReasonExhausted, "ipam: pool exhausted"), allocationFailureExhausted},
		{"retained allocation", ipamerrors.NewRetainedAllocation(ipclaimsResource, "eth0-f-ipv4", "alloc-9f2c", "retained"), allocationFailureConflict},
		{"claim exists", ipamerrors.NewClaimExists(ipclaimsResource, "eth0-f-ipv4", "already holds an allocation"), allocationFailureConflict},
		{"class not found", ipamerrors.New(ipamerrors.ReasonClassNotFound, "ipam: class not found"), allocationFailureRejected},
		{"no default class", ipamerrors.New(ipamerrors.ReasonNoDefaultClass, "ipam: no default class"), allocationFailureRejected},
		{"no offering pool", ipamerrors.New(ipamerrors.ReasonNoOfferingPool, "ipam: no pool offers this class"), allocationFailureRejected},
		{"prefix length", ipamerrors.New(ipamerrors.ReasonPrefixLengthRejected, "prefixLength 20 is outside the class range"), allocationFailureRejected},
		{"scope roles", ipamerrors.NewScopeRolesMissing([]string{"location"}, "scope is missing role"), allocationFailureRejected},
		{"no project scope", ipamerrors.New(ipamerrors.ReasonNoProjectScope, "ipam: request carries no project scope"), allocationFailureRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, ok := tc.err.(*apierrors.StatusError)
			require.True(t, ok)
			require.Equal(t, tc.want, classifyAllocationFailure(fromTheWire(t, status)))
		})
	}
}

// A reason added to IPAM after this build, and anything that never came from
// IPAM, land on the catch-all. Guessing would put a wrong reason on our API.
func TestClassifyAllocationFailureDoesNotGuess(t *testing.T) {
	added := &apierrors.StatusError{ErrStatus: metav1.Status{
		Status: metav1.StatusFailure,
		Code:   http.StatusBadRequest,
		Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{
			Type: metav1.CauseType("AddedAfterThisBuild"),
		}}},
	}}
	require.Equal(t, allocationFailureUnknown, classifyAllocationFailure(fromTheWire(t, added)))
	require.Equal(t, allocationFailureUnknown, classifyAllocationFailure(errors.New("connection refused")))
	require.Equal(t, allocationFailureUnknown,
		classifyAllocationFailure(apierrors.NewInternalError(errors.New("boom"))))
}

// An operator may upgrade this operator before IPAM. An older server answers
// exhaustion with a bare 507 and no reason, and that must still read as
// exhaustion rather than as an unexplained failure.
func TestClassifyAllocationFailureReadsAnOlderServer(t *testing.T) {
	legacy := apierrors.NewGenericServerResponse(
		507, "POST", ipclaimsResource, "", "IPPool exhausted", 0, false)
	require.Equal(t, allocationFailureExhausted, classifyAllocationFailure(fromTheWire(t, legacy)))
}

// Naming the pool that ran out was the point of asking IPAM for it. A message
// that says an address ran out without saying what ran out leaves an operator
// with nothing to widen.
func TestExhaustionMessageNamesThePool(t *testing.T) {
	err := fromTheWire(t, ipamerrors.NewPoolExhausted("datum-v4", `IPPool "datum-v4" is exhausted`))
	message := allocationFailureMessage(classifyAllocationFailure(err), v4Request(), err)

	require.Contains(t, message, "datum-v4", "the pool that ran out belongs in the condition")
	require.Contains(t, message, "an IPv4 address")
	require.Contains(t, message, `IPPool "datum-v4" is exhausted`, "IPAM's message is carried verbatim")
}

// Exhaustion while a level of the class chain is provisioned names no pool. An
// invented name would send an operator to the wrong place.
func TestExhaustionMessageWithoutAPoolNamesNone(t *testing.T) {
	err := fromTheWire(t, ipamerrors.New(ipamerrors.ReasonExhausted, "ipam: pool exhausted"))
	message := allocationFailureMessage(classifyAllocationFailure(err), classRequest(), err)

	require.NotContains(t, message, "IPPool")
	require.Contains(t, message, `an address of class "public-v4"`)
	require.Contains(t, message, "ipam: pool exhausted")
}

// The refusals that used to be one indistinguishable 400 now say which piece of
// configuration is missing.
func TestRejectionMessagesSayWhichConfigurationIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      *apierrors.StatusError
		contains string
	}{
		{"class not found", ipamerrors.New(ipamerrors.ReasonClassNotFound, "ipam: class not found"), "no address class"},
		{"no default class", ipamerrors.New(ipamerrors.ReasonNoDefaultClass, "ipam: no default class"), "no default address class"},
		{"no offering pool", ipamerrors.New(ipamerrors.ReasonNoOfferingPool, "ipam: no pool offers this class"), "No address pool offers"},
		{"prefix length", ipamerrors.New(ipamerrors.ReasonPrefixLengthRejected, "prefixLength 20 is outside"), "at the requested size"},
		{"no project scope", ipamerrors.New(ipamerrors.ReasonNoProjectScope, "ipam: request carries no project scope"), "without a project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fromTheWire(t, tc.err)
			message := allocationFailureMessage(classifyAllocationFailure(err), classRequest(), err)

			require.Contains(t, message, tc.contains)
			require.Contains(t, message, tc.err.ErrStatus.Message, "IPAM's message is carried verbatim")
			require.NotContains(t, message, "usually means",
				"the reason is known now, so the message must not hedge")
		})
	}
}

// A claim short two roles is one refusal naming both, so the condition names
// both rather than sending an operator back for the second.
func TestScopeRolesMessageNamesEveryMissingRole(t *testing.T) {
	err := fromTheWire(t, ipamerrors.NewScopeRolesMissing(
		[]string{"network", "location"}, "scope is missing roles"))
	message := allocationFailureMessage(classifyAllocationFailure(err), v4Request(), err)

	require.Contains(t, message, `"network"`)
	require.Contains(t, message, `"location"`)
	require.Contains(t, message, "scope roles")
}

// The hedge survives for exactly one case: a refusal this build cannot name.
func TestUnnamedRejectionStillHedges(t *testing.T) {
	err := fromTheWire(t, apierrors.NewBadRequest("something IPAM has not classified"))
	require.Equal(t, allocationFailureUnknown, classifyAllocationFailure(err))
	require.Contains(t,
		allocationFailureMessage(allocationFailureRejected, classRequest(), err),
		"usually means")
}

// The two conflicts are one condition reason to our consumers, but the message
// names the allocation that has to go when IPAM says which one it is.
func TestConflictMessageNamesTheRetainedAllocation(t *testing.T) {
	retained := fromTheWire(t, ipamerrors.NewRetainedAllocation(
		ipclaimsResource, "eth0-f-ipv4", "alloc-9f2c", "retained by an earlier claim"))
	message := allocationFailureMessage(classifyAllocationFailure(retained), v4Request(), retained)
	require.Contains(t, message, "alloc-9f2c")
	require.Contains(t, message, "retained by an earlier claim")

	exists := fromTheWire(t, ipamerrors.NewClaimExists(
		ipclaimsResource, "eth0-f-ipv4", "already holds an allocation"))
	require.NotContains(t,
		allocationFailureMessage(classifyAllocationFailure(exists), v4Request(), exists),
		"IPAllocation")
}

// Whatever the reason, IPAM's account of the failure reaches the condition.
// Parsing it instead would tie our conditions to its wording.
func TestEveryMessageCarriesIPAMsOwnMessage(t *testing.T) {
	err := fromTheWire(t, apierrors.NewInternalError(errors.New("postgres is unreachable")))
	message := allocationFailureMessage(classifyAllocationFailure(err), v4Request(), err)
	require.Contains(t, message, "postgres is unreachable")
}
