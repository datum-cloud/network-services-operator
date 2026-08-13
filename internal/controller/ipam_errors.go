// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"errors"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// allocationFailureReason classifies why IPAM refused an allocation, by status
// code only. IPAM's own message is passed through unread.
type allocationFailureReason string

const (
	allocationFailureExhausted allocationFailureReason = "AddressPoolExhausted"
	allocationFailureConflict  allocationFailureReason = "RetainedAddressConflict"
	// Usually an address class or pool that is not configured, rather than a
	// bad request, and the status code does not tell the two apart.
	allocationFailureRejected allocationFailureReason = "AddressAllocationRejected"
	allocationFailureUnknown  allocationFailureReason = "AllocationFailed"
)

const httpStatusInsufficientStorage = 507

func classifyAllocationFailure(err error) allocationFailureReason {
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		return allocationFailureUnknown
	}

	switch int(status.Status().Code) {
	case httpStatusInsufficientStorage:
		return allocationFailureExhausted
	case http.StatusConflict:
		return allocationFailureConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return allocationFailureRejected
	default:
		return allocationFailureUnknown
	}
}

func allocationFailureMessage(reason allocationFailureReason, request allocationRequest, err error) string {
	switch reason {
	case allocationFailureExhausted:
		return "No address is left for " + request.describe() + ": " + err.Error()
	case allocationFailureConflict:
		return "A retained allocation still holds the name for " + request.describe() + ": " + err.Error()
	case allocationFailureRejected:
		return "IPAM would not allocate " + request.describe() +
			". This usually means the address class, or a pool backing it, is not configured in this project: " + err.Error()
	default:
		return "Allocating " + request.describe() + " failed: " + err.Error()
	}
}
