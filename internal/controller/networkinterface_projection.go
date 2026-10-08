// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"maps"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// projectedInterface is the interface a consumer is shown. It carries what
// describes their NIC and drops what only names something inside the cell:
// status.networkContextRef, status.attachmentRef and status.vpc all identify
// objects that do not exist where the copy is published, and spec.claimRef names
// a claim that does not either.
//
// The claim's name survives as a label instead of a reference, because it is the
// slot identity a consumer recognises and status.phase means nothing without it.
func projectedInterface(source *networkingv1alpha.NetworkInterface, location string) *networkingv1alpha.NetworkInterface {
	projection := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name:   source.Name,
			Labels: projectionLabels(source, location),
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:           source.Spec.Network,
			InterfaceName:     source.Spec.InterfaceName,
			MTU:               source.Spec.MTU,
			Addresses:         append([]networkingv1alpha.NetworkInterfaceAddress(nil), source.Spec.Addresses...),
			ExternalAddresses: append([]networkingv1alpha.NetworkInterfaceExternalAddress(nil), source.Spec.ExternalAddresses...),
			ReclaimPolicy:     source.Spec.ReclaimPolicy,
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase:      source.Status.Phase,
			Conditions: append([]metav1.Condition(nil), source.Status.Conditions...),
		},
	}

	return projection
}

func projectionLabels(source *networkingv1alpha.NetworkInterface, location string) map[string]string {
	labels := map[string]string{}
	for key, value := range source.Labels {
		if hasAnyPrefix(key, consumerLabelPrefixes) {
			labels[key] = value
		}
	}

	labels[networkingv1alpha.NetworkInterfaceProjectionLabel] = "true"
	labels[networkingv1alpha.NetworkInterfaceSourceNamespaceLabel] = source.Namespace

	if location != "" {
		labels[networkingv1alpha.NetworkInterfaceLocationLabel] = location
	}
	if ref := source.Spec.ClaimRef; ref != nil {
		labels[networkingv1alpha.NetworkInterfaceHolderLabel] = ref.Name
	}

	return labels
}

// isProjection reports whether an interface is a published copy. Nothing else
// may be written or collected by the controllers that maintain them.
func isProjection(iface *networkingv1alpha.NetworkInterface) bool {
	return iface.Labels[networkingv1alpha.NetworkInterfaceProjectionLabel] == "true"
}

// projectionSlotRetry paces a writer that found the name it publishes under
// still held. A teardown takes one reconcile of the controller holding it, so
// what is waited on here is measured in seconds.
const projectionSlotRetry = 5 * time.Second

// projectionSlotHeld reports that the copy occupying a name is still being torn
// down, so the name is not free to write yet.
//
// A copy is collected while the interface it was published from is still held,
// and the name is free once that has finished. A writer that arrives mid
// teardown waits for it rather than writing through what is going.
type projectionSlotHeld struct {
	key client.ObjectKey
}

func (e *projectionSlotHeld) Error() string {
	return fmt.Sprintf("the interface copy at %s has not finished being torn down", e.key)
}

// inspectProjectionSlot reports the copy occupying a name.
func inspectProjectionSlot(
	ctx context.Context,
	cl client.Client,
	key client.ObjectKey,
) (*networkingv1alpha.NetworkInterface, error) {
	var occupant networkingv1alpha.NetworkInterface
	if err := cl.Get(ctx, key, &occupant); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed reading the interface copy: %w", err)
	}

	// Nothing here owns an interface somebody else made, and an interface that is
	// not a copy is the only thing in this namespace that could be holding the
	// name for a reason.
	if !isProjection(&occupant) {
		return nil, fmt.Errorf(
			"an interface that is not a published copy already holds %s, refusing to write over it", key)
	}

	return &occupant, nil
}

// applyProjection converges a copy onto what the source says.
//
// A label the source has dropped is removed from the copy rather than left in
// place. A copy is selected by its labels, so one the source no longer carries
// is a member of a service the interface has left.
func applyProjection(
	copied *networkingv1alpha.NetworkInterface,
	desired *networkingv1alpha.NetworkInterface,
	extraLabels map[string]string,
	owner func(*networkingv1alpha.NetworkInterface) error,
) error {
	wanted := map[string]string{}
	maps.Copy(wanted, desired.Labels)
	maps.Copy(wanted, extraLabels)

	copied.Labels, _ = replaceOwnedLabels(copied.Labels, wanted, platformLabelPrefixes, nil)
	copied.Spec = *desired.Spec.DeepCopy()
	if owner != nil {
		return owner(copied)
	}
	return nil
}

// writeProjection converges a copy onto what the source says. The copy is
// overwritten in full on every pass, so an edit to it never survives and never
// reaches the source: no controller reads a copy to decide anything.
//
// The name an interface is published under is one a replacement reuses. Nothing
// here empties the name first, because the copy that held it is collected while
// the interface it was published from is still there to say where it went, and a
// replacement that arrives before that has finished waits for it.
func writeProjection(
	ctx context.Context,
	cl client.Client,
	namespace string,
	desired *networkingv1alpha.NetworkInterface,
	extraLabels map[string]string,
	owner func(*networkingv1alpha.NetworkInterface) error,
) error {
	key := client.ObjectKey{Namespace: namespace, Name: desired.Name}

	occupant, err := inspectProjectionSlot(ctx, cl, key)
	if err != nil {
		return err
	}
	// A copy on its way out is not something to write through. What is left of it
	// goes when its teardown finishes, and the name is free then.
	if occupant != nil && !occupant.DeletionTimestamp.IsZero() {
		return &projectionSlotHeld{key: key}
	}

	copied := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, cl, copied, func() error {
		return applyProjection(copied, desired, extraLabels, owner)
	}); err != nil {
		return fmt.Errorf("failed writing the interface copy: %w", err)
	}

	if apiequality.Semantic.DeepEqual(copied.Status, desired.Status) {
		return nil
	}

	copied.Status = *desired.Status.DeepCopy()
	if err := cl.Status().Update(ctx, copied); err != nil {
		return fmt.Errorf("failed writing the interface copy status: %w", err)
	}

	return nil
}

// collectProjection removes a copy, and only ever a copy. Nothing real depends
// on a copy and a copy in a project control plane holds no finalizer, so
// removing one cannot strand an address or hold a namespace open.
func collectProjection(ctx context.Context, cl client.Client, key client.ObjectKey) error {
	var copied networkingv1alpha.NetworkInterface
	if err := cl.Get(ctx, key, &copied); err != nil {
		return client.IgnoreNotFound(err)
	}

	if !isProjection(&copied) {
		return nil
	}

	return client.IgnoreNotFound(cl.Delete(ctx, &copied))
}
