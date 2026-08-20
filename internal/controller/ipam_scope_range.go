// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
)

// scopeRange is the space IPAM holds for one class at one scope. A claim for it
// binds the pool rather than a block inside it, so a holder can report the
// space its consumers are addressed from without taking an address out of it.
type scopeRange struct {
	cidr     string
	poolName string
}

// scopeRangeRequest names the range a controller holds, and the reasons that
// controller reports when IPAM will not give it one. The reasons are the
// holder's own, so a network and a network context each answer in the language
// of their own API.
type scopeRangeRequest struct {
	className string
	claimName string
	namespace string
	scope     map[string]ipamv1alpha1.ScopeRef

	// subject names what the range is for, in messages an operator reads.
	subject string

	namespaceNotFoundReason string
	rangeUnsupportedReason  string
}

// holdScopeRange holds the range the request names, and reports it.
//
// The claim's name is derived from the holder's UID, so a reconcile that lost
// its answer finds the same range again instead of taking a second one. IPAM
// binds on create and refuses a duplicate name, so the read comes first.
func holdScopeRange(
	ctx context.Context,
	ipamClient client.Client,
	routing projectRouting,
	request scopeRangeRequest,
) (scopeRange, error) {
	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = request.namespace
	ipClaim.Name = request.claimName
	ipClaim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName: request.className,
		Target:    ipamv1alpha1.TargetScopeRange,
		Scope:     request.scope,
	}

	existing := &ipamv1alpha1.IPClaim{}
	getErr := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), existing)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return scopeRange{}, fmt.Errorf("failed reading IPClaim %q: %w", ipClaim.Name, getErr)
	}

	if getErr == nil {
		ipClaim = existing
	} else {
		createErr := ipamClient.Create(ctx, ipClaim)
		if createErr != nil {
			if isNamespaceNotFound(createErr, request.namespace) {
				return scopeRange{}, &bindingRefused{
					reason: request.namespaceNotFoundReason,
					message: fmt.Sprintf(
						"Project %q has no namespace %q in its control plane, so no address space can be allocated for it",
						routing.project, request.namespace),
				}
			}

			// The create can still lose a race with another writer, so ask
			// again before calling this a failure to allocate.
			raced := &ipamv1alpha1.IPClaim{}
			if err := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), raced); err != nil {
				reason := classifyAllocationFailure(createErr)
				return scopeRange{}, &allocationFailure{
					reason:  reason,
					message: allocationFailureMessage(reason, allocationRequest{className: request.className}, createErr),
				}
			}
			ipClaim = raced
		}
	}

	if ipClaim.Spec.Target != ipamv1alpha1.TargetScopeRange {
		return scopeRange{}, &bindingRefused{
			reason: request.rangeUnsupportedReason,
			message: fmt.Sprintf(
				"IPAM did not keep the request for a range on claim %q, so it cannot report the address space %s is drawn from",
				ipClaim.Name, request.subject),
		}
	}

	if ipClaim.Status.AllocatedCIDR == "" {
		return scopeRange{}, &allocationFailure{
			reason: allocationFailureUnknown,
			message: fmt.Sprintf("IPAM allocated no address space for %s (phase %q)",
				request.subject, ipClaim.Status.Phase),
		}
	}

	held := scopeRange{cidr: ipClaim.Status.AllocatedCIDR}
	if ipClaim.Status.PoolRef != nil {
		held.poolName = ipClaim.Status.PoolRef.Name
	}
	return held, nil
}

// releaseScopeRange gives a held range back. IPAM refuses with a conflict while
// anything is still allocated inside it, which is a wait rather than a failure.
func releaseScopeRange(
	ctx context.Context,
	ipamClient client.Client,
	namespace string,
	claimName string,
) error {
	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = namespace
	ipClaim.Name = claimName

	err := ipamClient.Delete(ctx, ipClaim)
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	if apierrors.IsConflict(err) {
		return &rangeOccupied{message: err.Error()}
	}
	return fmt.Errorf("failed releasing IPClaim %q: %w", ipClaim.Name, err)
}

// rangeOccupied reports a release IPAM refused because something is still
// allocated inside the range. Deleting the interfaces holding those addresses
// is what clears it, so the holder waits and says so rather than failing in a
// loop nothing outside the logs can see.
type rangeOccupied struct {
	message string
}

func (e *rangeOccupied) Error() string { return e.message }
