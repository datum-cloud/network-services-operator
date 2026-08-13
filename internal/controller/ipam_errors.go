// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"fmt"
	"strings"

	"go.miloapis.com/ipam/pkg/ipamerrors"
)

// allocationFailureReason classifies why IPAM refused an allocation. The values
// are condition reasons on our own API, so they are ours to keep stable and
// deliberately coarser than IPAM's taxonomy.
type allocationFailureReason string

const (
	allocationFailureExhausted allocationFailureReason = "AddressPoolExhausted"
	allocationFailureConflict  allocationFailureReason = "RetainedAddressConflict"
	allocationFailureRejected  allocationFailureReason = "AddressAllocationRejected"
	allocationFailureUnknown   allocationFailureReason = "AllocationFailed"
)

// classifyAllocationFailure asks IPAM why it refused rather than reading the
// status code. A reason this build does not know, and a server too old to send
// one, both land on the catch-all instead of being guessed at.
func classifyAllocationFailure(err error) allocationFailureReason {
	switch ipamerrors.ReasonFor(err) {
	case ipamerrors.ReasonExhausted:
		return allocationFailureExhausted
	case ipamerrors.ReasonAllocationRetained, ipamerrors.ReasonClaimExists:
		return allocationFailureConflict
	case ipamerrors.ReasonClassNotFound,
		ipamerrors.ReasonNoDefaultClass,
		ipamerrors.ReasonNoOfferingPool,
		ipamerrors.ReasonPrefixLengthRejected,
		ipamerrors.ReasonScopeRolesMissing,
		ipamerrors.ReasonNoProjectScope:
		return allocationFailureRejected
	default:
		return allocationFailureUnknown
	}
}

// allocationFailureMessage writes the condition message. IPAM's own message is
// appended verbatim in every branch: it is the only account of the failure, and
// parsing it would tie us to its wording.
func allocationFailureMessage(reason allocationFailureReason, request allocationRequest, err error) string {
	switch reason {
	case allocationFailureExhausted:
		return "No address is left for " + request.describe() + exhaustedPoolSuffix(err) + ": " + err.Error()
	case allocationFailureConflict:
		return "A retained allocation still holds the name for " + request.describe() +
			retainedAllocationSuffix(err) + ": " + err.Error()
	case allocationFailureRejected:
		return rejectionMessage(request, err) + ": " + err.Error()
	default:
		return "Allocating " + request.describe() + " failed: " + err.Error()
	}
}

// rejectionMessage says which piece of configuration is missing. IPAM used to
// answer every one of these with an indistinguishable 400, so this could only
// guess; it now asks.
func rejectionMessage(request allocationRequest, err error) string {
	switch ipamerrors.ReasonFor(err) {
	case ipamerrors.ReasonClassNotFound:
		return "IPAM has no address class for " + request.describe() +
			". The class must exist in this project"
	case ipamerrors.ReasonNoDefaultClass:
		return "IPAM has no default address class for " + request.describe() +
			", and the claim named no class"
	case ipamerrors.ReasonNoOfferingPool:
		return "No address pool offers the class for " + request.describe()
	case ipamerrors.ReasonPrefixLengthRejected:
		return "IPAM would not allocate " + request.describe() +
			" at the requested size, which its class does not allow"
	case ipamerrors.ReasonScopeRolesMissing:
		return "The claim for " + request.describe() + " is missing the scope " +
			missingScopeRoles(err) + " its class requires"
	case ipamerrors.ReasonNoProjectScope:
		return "The claim for " + request.describe() + " reached IPAM without a project"
	default:
		// A refusal this build cannot name, from a server that sends no reason
		// or one added since. The hedge belongs here and nowhere else.
		return "IPAM would not allocate " + request.describe() +
			". This usually means the address class, or a pool backing it, is not configured in this project"
	}
}

// exhaustedPoolSuffix names the pool that ran out. A claim names a class rather
// than a pool, so without this an operator is told an address ran out with
// nothing to widen. Exhaustion while provisioning an ancestor pool names none,
// and reporting the wrong one would be worse than reporting nothing.
func exhaustedPoolSuffix(err error) string {
	pool, ok := ipamerrors.ExhaustedPool(err)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" in IPPool %q", pool)
}

// retainedAllocationSuffix names the IPAllocation holding the address, which is
// the object that has to go for the name to be reusable.
func retainedAllocationSuffix(err error) string {
	allocation, ok := ipamerrors.RetainedAllocation(err)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" (IPAllocation %q)", allocation)
}

// missingScopeRoles names every role the claim was short, in one message. IPAM
// reports them all at once so an operator is not sent back a second time.
func missingScopeRoles(err error) string {
	roles := ipamerrors.MissingScopeRoles(err)
	if len(roles) == 0 {
		return "roles"
	}
	quoted := make([]string, len(roles))
	for i, role := range roles {
		quoted[i] = fmt.Sprintf("%q", role)
	}
	noun := "roles"
	if len(roles) == 1 {
		noun = "role"
	}
	return noun + " " + strings.Join(quoted, ", ")
}
