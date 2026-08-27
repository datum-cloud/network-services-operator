// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	// fabricIdentityBlockBits is the size of the block an identity is read out
	// of. The pool roots a /32 and hands out /64s, so the bits between them are
	// the block's index within the pool, and there are exactly 2^32 of them.
	fabricIdentityBlockBits = 64

	// fabricIdentityRootBits is where the pool's own prefix stops and the index
	// begins. Reading the index from a fixed offset rather than from the pool's
	// CIDR means the identity does not depend on an object this operator would
	// otherwise have to read on every allocation. A pool rooted longer than a
	// /32 simply leaves the leading bits of every index at zero, which is still
	// unique within the one pool the platform allocates from.
	fabricIdentityRootBits = 32
)

// reconcileFabricIdentity gives the network the identity the fabric knows it
// by, once, and never again.
//
// It reports whether it wrote anything and whether it wants to be called back.
// Allocation is independent of the network's address space: a network that
// claims no addresses still spans locations and still needs one identity there
// rather than a different one per location.
func (r *NetworkReconciler) reconcileFabricIdentity(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
) (changed bool, retry bool, err error) {
	if r.IPAM == nil || r.FabricIdentityClass == "" {
		return false, false, nil
	}

	// Allocated once. A network that already holds an identity is never asked
	// again, which is what makes this idempotent and what makes the identity
	// immutable in the only place that writes it.
	if network.Status.FabricIdentity != 0 {
		return r.reportFabricIdentity(ctx, cl, network, metav1.ConditionTrue,
			networkingv1alpha.NetworkFabricIdentityReasonAllocated,
			fmt.Sprintf("The fabric knows this network as %d", network.Status.FabricIdentity),
		), false, nil
	}

	ipamClient, err := r.IPAM.ClientForPlatform()
	if err != nil {
		return r.reportFabricIdentity(ctx, cl, network, metav1.ConditionFalse,
			networkingv1alpha.NetworkFabricIdentityReasonIdentitySpaceUnavailable,
			"No platform identity space is configured, so the network has no identity on the fabric",
		), false, nil
	}

	identity, err := r.claimFabricIdentity(ctx, ipamClient, network)
	if err != nil {
		var unusable *fabricIdentityUnusable
		if errors.As(err, &unusable) {
			return r.reportFabricIdentity(ctx, cl, network, metav1.ConditionFalse,
				networkingv1alpha.NetworkFabricIdentityReasonIdentityUnusable, unusable.Error(),
			), true, nil
		}
		log.FromContext(ctx).Info("the network's fabric identity cannot be allocated", "error", err.Error())
		return r.reportFabricIdentity(ctx, cl, network, metav1.ConditionFalse,
			networkingv1alpha.NetworkFabricIdentityReasonIdentitySpaceUnavailable, err.Error(),
		), true, nil
	}

	network.Status.FabricIdentity = identity
	apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkFabricIdentityAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             networkingv1alpha.NetworkFabricIdentityReasonAllocated,
		ObservedGeneration: network.Generation,
		Message:            fmt.Sprintf("The fabric knows this network as %d", identity),
	})

	if err := cl.Status().Update(ctx, network); err != nil {
		return false, false, fmt.Errorf("failed publishing the network's fabric identity: %w", err)
	}
	return true, false, nil
}

// claimFabricIdentity holds one block of the identifier space and reads the
// identity out of it.
//
// The claim's name is derived from the network's UID, so a reconcile that lost
// its answer finds the same block again instead of taking a second one, and a
// network deleted and recreated under the same name is a different network with
// a different identity. IPAM binds on create and refuses a duplicate name, so
// the read comes first.
func (r *NetworkReconciler) claimFabricIdentity(
	ctx context.Context,
	ipamClient client.Client,
	network *networkingv1alpha.Network,
) (int64, error) {
	ipClaim := &ipamv1alpha1.IPClaim{}
	ipClaim.Namespace = r.FabricIdentityNamespace
	ipClaim.Name = fabricIdentityClaimName(network)
	ipClaim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName:    r.FabricIdentityClass,
		Target:       ipamv1alpha1.TargetBlock,
		PrefixLength: ptr.To(int32(fabricIdentityBlockBits)),

		// An identity is never given back. A Route Target still installed in a
		// remote location's import policy would silently merge a new network
		// into a dead one's routes, so holding the block forever is the safe
		// failure and reissuing it is not a failure anything can see.
		ReclaimPolicy: ipamv1alpha1.ReclaimRetain,
	}

	existing := &ipamv1alpha1.IPClaim{}
	getErr := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), existing)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return 0, fmt.Errorf("failed reading the identity claim %q: %w", ipClaim.Name, getErr)
	}

	if getErr == nil {
		ipClaim = existing
	} else if createErr := ipamClient.Create(ctx, ipClaim); createErr != nil {
		// The create can still lose a race with another writer, so ask again
		// before calling this a failure to allocate.
		raced := &ipamv1alpha1.IPClaim{}
		if err := ipamClient.Get(ctx, client.ObjectKeyFromObject(ipClaim), raced); err != nil {
			return 0, fmt.Errorf("failed claiming a fabric identity: %w", createErr)
		}
		ipClaim = raced
	}

	if ipClaim.Status.AllocatedCIDR == "" {
		return 0, fmt.Errorf("the identity space allocated nothing for this network (phase %q)", ipClaim.Status.Phase)
	}

	return fabricIdentityFromBlock(ipClaim.Status.AllocatedCIDR)
}

// fabricIdentityFromBlock reads the identity out of the block the identifier
// space handed out. The block's index within the pool is the identity, and the
// index is the 32 bits between the pool's root and the block, which is exactly
// the width that survives into the Route Target.
func fabricIdentityFromBlock(cidr string) (int64, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return 0, &fabricIdentityUnusable{message: fmt.Sprintf(
			"the identity space answered with %q, which is not a prefix", cidr)}
	}

	address := prefix.Addr()
	if !address.Is6() || address.Is4In6() {
		return 0, &fabricIdentityUnusable{message: fmt.Sprintf(
			"the identity space answered with %q; identifiers are read out of an IPv6 space", cidr)}
	}

	// Shorter than a /64 and two networks could be handed blocks that share an
	// index; longer and one block's index is not the whole of it.
	if prefix.Bits() != fabricIdentityBlockBits {
		return 0, &fabricIdentityUnusable{message: fmt.Sprintf(
			"the identity space answered with %q; identifiers are read out of a /%d",
			cidr, fabricIdentityBlockBits)}
	}

	octets := address.As16()
	identity := int64(binary.BigEndian.Uint32(octets[fabricIdentityRootBits/8 : fabricIdentityBlockBits/8]))
	if identity == 0 {
		return 0, &fabricIdentityUnusable{message: fmt.Sprintf(
			"the identity space answered with %q, whose index is zero; zero is what an unallocated network reads as, so the pool must not hand out its first block",
			cidr)}
	}
	return identity, nil
}

// fabricIdentityUnusable says the identity space answered, and its answer
// cannot be turned into an identity. Retrying reaches the same block, so this
// is a wait on an operator rather than on the service.
type fabricIdentityUnusable struct {
	message string
}

func (e *fabricIdentityUnusable) Error() string { return e.message }

// reportFabricIdentity records why the network does or does not carry an
// identity, and reports whether that changed anything.
func (r *NetworkReconciler) reportFabricIdentity(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	status metav1.ConditionStatus,
	reason string,
	message string,
) bool {
	if !apimeta.SetStatusCondition(&network.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkFabricIdentityAllocated,
		Status:             status,
		Reason:             reason,
		ObservedGeneration: network.Generation,
		Message:            message,
	}) {
		return false
	}

	if err := cl.Status().Update(ctx, network); err != nil {
		log.FromContext(ctx).Error(err, "failed recording the network's fabric identity")
		return false
	}
	return true
}

func fabricIdentityClaimName(network *networkingv1alpha.Network) string {
	return "fabric-identity-" + string(network.UID)
}
