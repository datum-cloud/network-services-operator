// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"fmt"

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

// allocationFailureMessage says which address we were asking for and hands over
// to IPAM's account of why it said no.
//
// IPAM's message already carries the detail a reader acts on: the pool that ran
// out, the roles a scope was short and the class that required them, the
// allocation holding a name. Restating any of it here would print it twice, and
// reconstructing it would drift from what IPAM actually said. What IPAM cannot
// know is which of this claim's addresses was being allocated, so that is our
// half and the whole of it.
func allocationFailureMessage(reason allocationFailureReason, request allocationRequest, err error) string {
	switch reason {
	case allocationFailureExhausted:
		// The one message we write ourselves. IPAM's reads the same, but the
		// pool is the thing an operator has to widen, and taking it from the
		// error's details rather than its prose keeps it in the condition
		// whatever IPAM's wording does later.
		if pool, ok := ipamerrors.ExhaustedPool(err); ok {
			return fmt.Sprintf("No address is left for %s: IPPool %q is exhausted", request.describe(), pool)
		}
		// Exhaustion while a level of the class chain was provisioned names no
		// pool, and IPAM's message is the only account of which level ran out.
		return "No address is left for " + request.describe() + ": " + err.Error()
	case allocationFailureConflict:
		return "Another allocation still holds the name for " + request.describe() + ": " + err.Error()
	case allocationFailureRejected:
		return "IPAM would not allocate " + request.describe() + ": " + err.Error()
	default:
		return "Allocating " + request.describe() + " failed: " + err.Error()
	}
}
