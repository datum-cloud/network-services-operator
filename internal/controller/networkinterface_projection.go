// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"maps"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
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
	labels := map[string]string{
		networkingv1alpha.NetworkInterfaceProjectionLabel:      "true",
		networkingv1alpha.NetworkInterfaceSourceNamespaceLabel: source.Namespace,
	}

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

// writeProjection converges a copy onto what the source says. The copy is
// overwritten in full on every pass, so an edit to it never survives and never
// reaches the source: no controller reads a copy to decide anything.
func writeProjection(
	ctx context.Context,
	cl client.Client,
	namespace string,
	desired *networkingv1alpha.NetworkInterface,
	extraLabels map[string]string,
	owner func(*networkingv1alpha.NetworkInterface) error,
) error {
	copied := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: namespace},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, cl, copied, func() error {
		if copied.Labels == nil {
			copied.Labels = map[string]string{}
		}
		maps.Copy(copied.Labels, desired.Labels)
		maps.Copy(copied.Labels, extraLabels)
		copied.Spec = *desired.Spec.DeepCopy()
		if owner != nil {
			return owner(copied)
		}
		return nil
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

// collectProjection removes a copy, and only ever a copy. A copy holds no
// finalizer and nothing real depends on it, so removing one cannot strand an
// address or hold a namespace open.
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
