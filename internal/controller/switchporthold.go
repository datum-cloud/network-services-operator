// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	miloProjectAPIGroup = "resourcemanager.miloapis.com"
	miloProjectKind     = "Project"

	// Each attempt starts from a fresh read, so a conflict only means another
	// writer moved first. A hold contended past this many attempts is left to
	// the next reconcile.
	switchPortHoldWriteAttempts = 5
)

func isPhysical(claim *networkingv1alpha.NetworkInterfaceClaim) bool {
	return claim.Spec.AttachmentMode == networkingv1alpha.NetworkInterfaceAttachmentModePhysical
}

func isBootClaim(claim *networkingv1alpha.NetworkInterfaceClaim) bool {
	return claim.Spec.Purpose == networkingv1alpha.NetworkInterfacePurposeBoot
}

func switchPortHoldKey(claim *networkingv1alpha.NetworkInterfaceClaim) client.ObjectKey {
	point := claim.Spec.AttachmentPoint
	return client.ObjectKey{Name: networkingv1alpha.SwitchPortHoldName(point.Switch, point.Port)}
}

func holdEntryFor(claim *networkingv1alpha.NetworkInterfaceClaim) networkingv1alpha.SwitchPortHoldClaimRef {
	return networkingv1alpha.SwitchPortHoldClaimRef{
		Namespace: claim.Namespace,
		Name:      claim.Name,
		Network:   claim.Spec.Network.Name,
	}
}

func sameClaim(entry networkingv1alpha.SwitchPortHoldClaimRef, claim *networkingv1alpha.NetworkInterfaceClaim) bool {
	return entry.Namespace == claim.Namespace && entry.Name == claim.Name
}

func holdIsForPort(hold *networkingv1alpha.SwitchPortHold, claim *networkingv1alpha.NetworkInterfaceClaim) bool {
	point := claim.Spec.AttachmentPoint
	return hold.Spec.Switch == point.Switch && hold.Spec.Port == point.Port
}

// holdRecords reports whether the port's hold carries an entry under the
// claim's name. An entry under its name for another network counts too: the
// realizer may have acted on it, so only the realizer may say the port is clear.
func holdRecords(hold *networkingv1alpha.SwitchPortHold, claim *networkingv1alpha.NetworkInterfaceClaim) bool {
	if !holdIsForPort(hold, claim) {
		return false
	}
	if hold.Spec.Boot != nil && sameClaim(*hold.Spec.Boot, claim) {
		return true
	}
	return slices.ContainsFunc(hold.Spec.NetworkClaims, func(entry networkingv1alpha.SwitchPortHoldClaimRef) bool {
		return sameClaim(entry, claim)
	})
}

// readHold reads the claim's hold from the API server, returning nil when the
// port has none. A cache could answer with a hold that another writer has
// since changed, and every decision here rests on the current one.
func readHold(
	ctx context.Context,
	reader client.Reader,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) (*networkingv1alpha.SwitchPortHold, error) {
	hold := &networkingv1alpha.SwitchPortHold{}
	if err := reader.Get(ctx, switchPortHoldKey(claim), hold); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed reading switch port hold: %w", err)
	}
	return hold, nil
}

// recordOnHold records the claim on its port's hold, creating the hold for a
// free port. It reports false when the port is held by claims this one cannot
// share it with, and leaves the hold as it was.
func recordOnHold(
	ctx context.Context,
	cl client.Client,
	reader client.Reader,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	project string,
) (bool, error) {
	var lastErr error
	for range switchPortHoldWriteAttempts {
		hold, err := readHold(ctx, reader, claim)
		if err != nil {
			return false, err
		}

		if hold == nil {
			hold = &networkingv1alpha.SwitchPortHold{}
			hold.Name = switchPortHoldKey(claim).Name
			hold.Spec.Switch = claim.Spec.AttachmentPoint.Switch
			hold.Spec.Port = claim.Spec.AttachmentPoint.Port
			admitToHold(hold, claim, project)
			clearEmptyConsumer(hold)
			// Only one create of a name succeeds, so a port taken in between
			// is read again and decided against what took it.
			if err := cl.Create(ctx, hold); err != nil {
				if apierrors.IsAlreadyExists(err) {
					lastErr = err
					continue
				}
				return false, fmt.Errorf("failed creating switch port hold: %w", err)
			}
			return true, nil
		}

		if !holdIsForPort(hold, claim) {
			return false, &bindingRefused{
				reason: networkingv1alpha.NetworkInterfaceClaimReasonAttachmentPointHeld,
				message: "The record for this switch port has the name of a record for another port, " +
					"so the claim cannot be recorded and no address is allocated for it",
			}
		}

		before := hold.Spec.DeepCopy()
		if err := pruneHold(ctx, reader, hold, claim); err != nil {
			return false, err
		}
		recorded := admitToHold(hold, claim, project)
		clearEmptyConsumer(hold)
		if equalHoldSpecs(before, &hold.Spec) {
			return recorded, nil
		}

		if err := cl.Update(ctx, hold); err != nil {
			// A hold deleted since the read is a free port again, and a create
			// on the next attempt takes it.
			if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
				lastErr = err
				continue
			}
			return false, fmt.Errorf("failed recording the claim on its switch port hold: %w", err)
		}
		return recorded, nil
	}
	return false, lastErr
}

// admitToHold records the claim if the port can carry it, reporting whether
// the hold now records it. A port carries one boot claim, and network claims
// from one project with each network at most once.
func admitToHold(
	hold *networkingv1alpha.SwitchPortHold,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	project string,
) bool {
	entry := holdEntryFor(claim)

	// An entry under this claim's name for another network, or in the other
	// slot, is left from an earlier claim of that name.
	if isBootClaim(claim) {
		removeNetworkClaim(hold, claim)
		if hold.Spec.Boot != nil && !sameClaim(*hold.Spec.Boot, claim) {
			return false
		}
		hold.Spec.Boot = &entry
		return true
	}

	if hold.Spec.Boot != nil && sameClaim(*hold.Spec.Boot, claim) {
		hold.Spec.Boot = nil
	}
	if slices.Contains(hold.Spec.NetworkClaims, entry) {
		return true
	}
	removeNetworkClaim(hold, claim)

	if len(hold.Spec.NetworkClaims) > 0 {
		if hold.Spec.ConsumerRef == nil || hold.Spec.ConsumerRef.Name != project {
			return false
		}
		if slices.ContainsFunc(hold.Spec.NetworkClaims, func(held networkingv1alpha.SwitchPortHoldClaimRef) bool {
			return held.Namespace == entry.Namespace && held.Network == entry.Network
		}) {
			return false
		}
	}

	hold.Spec.ConsumerRef = &networkingv1alpha.SwitchPortHoldConsumerRef{
		APIGroup: miloProjectAPIGroup,
		Kind:     miloProjectKind,
		Name:     project,
	}
	hold.Spec.NetworkClaims = append(hold.Spec.NetworkClaims, entry)
	return true
}

func removeNetworkClaim(hold *networkingv1alpha.SwitchPortHold, claim *networkingv1alpha.NetworkInterfaceClaim) {
	hold.Spec.NetworkClaims = slices.DeleteFunc(hold.Spec.NetworkClaims, func(entry networkingv1alpha.SwitchPortHoldClaimRef) bool {
		return sameClaim(entry, claim)
	})
}

// clearEmptyConsumer drops the project from a hold its last network claim has
// left, so a hold kept only by a boot claim names no project.
func clearEmptyConsumer(hold *networkingv1alpha.SwitchPortHold) {
	if len(hold.Spec.NetworkClaims) == 0 {
		hold.Spec.NetworkClaims = nil
		hold.Spec.ConsumerRef = nil
	}
}

// pruneHold drops every entry whose claim is gone, read from the API server, so
// no entry outlives its claim. A claim of the same name that names another
// network, another port, or the other kind of claim is a different claim. The
// claim being written is never pruned here.
func pruneHold(
	ctx context.Context,
	reader client.Reader,
	hold *networkingv1alpha.SwitchPortHold,
	writing *networkingv1alpha.NetworkInterfaceClaim,
) error {
	live := func(entry networkingv1alpha.SwitchPortHoldClaimRef, boot bool) (bool, error) {
		if sameClaim(entry, writing) {
			return true, nil
		}
		var claim networkingv1alpha.NetworkInterfaceClaim
		if err := reader.Get(ctx, client.ObjectKey{Namespace: entry.Namespace, Name: entry.Name}, &claim); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed reading claim %s/%s recorded on switch port hold %q: %w",
				entry.Namespace, entry.Name, hold.Name, err)
		}
		return isPhysical(&claim) && claim.Spec.AttachmentPoint != nil && holdIsForPort(hold, &claim) &&
			claim.Spec.Network.Name == entry.Network && isBootClaim(&claim) == boot, nil
	}

	if hold.Spec.Boot != nil {
		keep, err := live(*hold.Spec.Boot, true)
		if err != nil {
			return err
		}
		if !keep {
			hold.Spec.Boot = nil
		}
	}

	kept := hold.Spec.NetworkClaims[:0:0]
	for _, entry := range hold.Spec.NetworkClaims {
		keep, err := live(entry, false)
		if err != nil {
			return err
		}
		if keep {
			kept = append(kept, entry)
		}
	}
	hold.Spec.NetworkClaims = kept
	return nil
}

// leaveHold removes the claim's entries from its port's hold, and deletes the
// hold once it records nothing. The delete is conditional on the hold being
// unchanged since it was read, so a claim recorded in between keeps it.
func leaveHold(
	ctx context.Context,
	cl client.Client,
	reader client.Reader,
	claim *networkingv1alpha.NetworkInterfaceClaim,
) error {
	var lastErr error
	for range switchPortHoldWriteAttempts {
		hold, err := readHold(ctx, reader, claim)
		if err != nil {
			return err
		}
		if hold == nil || !holdIsForPort(hold, claim) {
			return nil
		}

		before := hold.Spec.DeepCopy()
		if hold.Spec.Boot != nil && sameClaim(*hold.Spec.Boot, claim) {
			hold.Spec.Boot = nil
		}
		removeNetworkClaim(hold, claim)
		if err := pruneHold(ctx, reader, hold, claim); err != nil {
			return err
		}
		clearEmptyConsumer(hold)

		if hold.Spec.Boot == nil && len(hold.Spec.NetworkClaims) == 0 {
			version := hold.ResourceVersion
			err = cl.Delete(ctx, hold, client.Preconditions{ResourceVersion: &version})
		} else if !equalHoldSpecs(before, &hold.Spec) {
			err = cl.Update(ctx, hold)
		}
		switch {
		case err == nil, apierrors.IsNotFound(err):
			return nil
		case apierrors.IsConflict(err):
			lastErr = err
			continue
		default:
			return fmt.Errorf("failed removing the claim from its switch port hold: %w", err)
		}
	}
	return lastErr
}

func equalHoldSpecs(a, b *networkingv1alpha.SwitchPortHoldSpec) bool {
	if (a.Boot == nil) != (b.Boot == nil) || (a.Boot != nil && *a.Boot != *b.Boot) {
		return false
	}
	if (a.ConsumerRef == nil) != (b.ConsumerRef == nil) || (a.ConsumerRef != nil && *a.ConsumerRef != *b.ConsumerRef) {
		return false
	}
	return slices.Equal(a.NetworkClaims, b.NetworkClaims)
}
