// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// errInternalDNSIntentUnverified means a NetworkContext's DNS annotations do
// not name access that DNS accepted for the context's own network.
var errInternalDNSIntentUnverified = errors.New("DNS annotations do not match the access DNS accepted for this network")

// internalDNSIntentComplete reports whether the annotations carry everything a
// cell needs to program a DNS service path. A cell withdraws any path it has
// for a context whose intent is incomplete.
func internalDNSIntentComplete(annotations map[string]string) bool {
	return annotations[InternalDNSContextNameAnnotation] != "" &&
		annotations[InternalDNSAccessNameAnnotation] != "" &&
		annotations[InternalDNSServiceDestinationAnnotation] != "" &&
		annotations[InternalDNSRegionAnnotation] != ""
}

// verifyInternalDNSIntent checks the DNS annotations on a project
// NetworkContext against the resolver context and access binding DNS accepted
// for the context's network. It returns errInternalDNSIntentUnverified when
// they disagree, and any other error when the check could not be made.
func verifyInternalDNSIntent(
	ctx context.Context,
	cl client.Client,
	networkContext client.Object,
	networkName string,
	servicePrefix string,
) error {
	annotations := networkContext.GetAnnotations()
	namespace := networkContext.GetNamespace()

	network := &networkingv1alpha.Network{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: networkName}, network); err != nil {
		return unverifiedIfNotFound(err)
	}
	if network.UID == "" {
		return errInternalDNSIntentUnverified
	}

	dnsContext := newInternalDNSObject(internalDNSContextGVK, namespace, annotations[InternalDNSContextNameAnnotation])
	if err := cl.Get(ctx, client.ObjectKeyFromObject(dnsContext), dnsContext); err != nil {
		return unverifiedIfNotFound(err)
	}
	consumerID, _, _ := unstructured.NestedString(dnsContext.Object, "spec", "consumerID")
	if dnsContext.GetUID() == "" || !dnsContext.GetDeletionTimestamp().IsZero() ||
		dnsContext.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != string(network.UID) ||
		!strings.HasSuffix(consumerID, "/"+string(network.UID)) {
		return errInternalDNSIntentUnverified
	}
	if suffix := annotations[InternalDNSSuffixAnnotation]; suffix != "" {
		allocated, _, _ := unstructured.NestedString(dnsContext.Object, "status", "managedNamespace", "suffix")
		if suffix != allocated {
			return errInternalDNSIntentUnverified
		}
	}

	binding := newInternalDNSObject(internalDNSAccessGVK, namespace, annotations[InternalDNSAccessNameAnnotation])
	if err := cl.Get(ctx, client.ObjectKeyFromObject(binding), binding); err != nil {
		return unverifiedIfNotFound(err)
	}
	destination, err := internalDNSDestination(servicePrefix, dnsContext.GetUID())
	if err != nil {
		return err
	}
	if annotations[InternalDNSServiceDestinationAnnotation] != destination ||
		!binding.GetDeletionTimestamp().IsZero() ||
		binding.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != string(network.UID) ||
		!accessBindingMatches(binding, dnsContext, annotations[InternalDNSRegionAnnotation], annotations[InternalDNSServiceDestinationAnnotation]) ||
		!accessBindingAccepted(binding) {
		return errInternalDNSIntentUnverified
	}
	return nil
}

// accessBindingAccepted reports whether DNS accepted the binding's current
// generation, which is when DNS claims its destination for the context. A DNS
// controller outage leaves the last reported condition in place.
func accessBindingAccepted(binding *unstructured.Unstructured) bool {
	if binding.GetGeneration() < 1 {
		return false
	}
	raw, _, _ := unstructured.NestedSlice(binding.Object, "status", "conditions")
	conditions := make([]metav1.Condition, 0, len(raw))
	for _, entry := range raw {
		condition, _ := entry.(map[string]any)
		observed, _, _ := unstructured.NestedInt64(condition, "observedGeneration")
		conditionType, _ := condition["type"].(string)
		status, _ := condition["status"].(string)
		conditions = append(conditions, metav1.Condition{
			Type: conditionType, Status: metav1.ConditionStatus(status), ObservedGeneration: observed,
		})
	}
	accepted := apimeta.FindStatusCondition(conditions, "Accepted")
	return accepted != nil && accepted.Status == metav1.ConditionTrue && accepted.ObservedGeneration == binding.GetGeneration()
}

func unverifiedIfNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: %w", errInternalDNSIntentUnverified, err)
	}
	return err
}
