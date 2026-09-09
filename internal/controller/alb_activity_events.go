// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

const (
	EventReasonProgrammed             = "Programmed"
	EventReasonProgrammingFailed      = "ProgrammingFailed"
	EventReasonHostnameInUse          = "HostnameInUse"
	EventReasonHostnamesUnverified    = "HostnamesUnverified"
	EventReasonCertificateIssued      = "CertificateIssued"
	EventReasonCertificateFailed      = "CertificateFailed"
	EventReasonDNSRecordFailed        = "DNSRecordFailed"
	EventReasonWaitingForCertificates = "WaitingForCertificates"
)

const albActivityReportingController = "networking.datumapis.com/network-services-operator"

type albActivityEvent struct {
	Type    string
	Reason  string
	Note    string
	Related *corev1.ObjectReference
}

func emitHTTPProxyActivityEvents(
	ctx context.Context,
	cl client.Client,
	proxy *networkingv1alpha.HTTPProxy,
	previous []metav1.Condition,
) {
	if cl == nil || proxy == nil {
		return
	}

	displayName := display.HTTPProxyDisplayName(proxy)
	if displayName == "" {
		displayName = proxy.Name
	}

	for _, ev := range httpProxyActivityEvents(previous, proxy.Status.Conditions, displayName) {
		emitALBActivityEvent(ctx, cl, proxy, ev, displayName, nil)
	}
}

func httpProxyActivityEvents(previous, current []metav1.Condition, displayName string) []albActivityEvent {
	var out []albActivityEvent

	if programmedBecameTrue(previous, current, networkingv1alpha.HTTPProxyConditionProgrammed) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeNormal,
			Reason: EventReasonProgrammed,
			Note:   fmt.Sprintf("Load balancer %s is live", displayName),
		})
	}
	if programmingFailed(previous, current) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeWarning,
			Reason: EventReasonProgrammingFailed,
			Note:   conditionMessage(current, networkingv1alpha.HTTPProxyConditionProgrammed, "Load balancer failed to program"),
		})
	}
	if becameTrue(previous, current, networkingv1alpha.HTTPProxyConditionHostnamesInUse) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeWarning,
			Reason: EventReasonHostnameInUse,
			Note:   conditionMessage(current, networkingv1alpha.HTTPProxyConditionHostnamesInUse, "Hostname is already in use"),
		})
	}
	if hostnamesUnverifiedOncePerGeneration(previous, current) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeNormal,
			Reason: EventReasonHostnamesUnverified,
			Note:   fmt.Sprintf("%s is waiting for domain verification", displayName),
		})
	}
	if becameTrue(previous, current, networkingv1alpha.HTTPProxyConditionCertificatesReady) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeNormal,
			Reason: EventReasonCertificateIssued,
			Note:   fmt.Sprintf("TLS certificate issued for %s", displayName),
		})
	}
	if certificateFailed(previous, current) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeWarning,
			Reason: EventReasonCertificateFailed,
			Note:   fmt.Sprintf("Failed to issue TLS certificate for %s", displayName),
		})
	}
	if dnsRecordFailed(previous, current) {
		out = append(out, albActivityEvent{
			Type:   corev1.EventTypeWarning,
			Reason: EventReasonDNSRecordFailed,
			Note:   fmt.Sprintf("Failed to program DNS for %s", displayName),
		})
	}
	return out
}

func emitTPPActivityEvents(
	ctx context.Context,
	cl client.Client,
	policy *networkingv1alpha.TrafficProtectionPolicy,
	original *networkingv1alpha.TrafficProtectionPolicy,
) {
	if cl == nil || policy == nil {
		return
	}

	displayName := policy.Annotations[display.AnnotationDisplayName]
	if displayName == "" {
		displayName = display.HTTPProxyDisplayName(lookupHTTPProxyForTPP(ctx, cl, policy))
	}
	if displayName == "" {
		if len(policy.Spec.TargetRefs) > 0 {
			displayName = string(policy.Spec.TargetRefs[0].Name)
		} else {
			displayName = policy.Name
		}
	}

	var related *corev1.ObjectReference
	if proxy := lookupHTTPProxyForTPP(ctx, cl, policy); proxy != nil {
		ref := httpProxyObjectRef(proxy)
		related = &ref
	}

	prevAccepted, prevProgrammed := tppAncestorConditions(original)
	currAccepted, currProgrammed := tppAncestorConditions(policy)

	var events []albActivityEvent
	if programmedBecameTrueConditions(prevProgrammed, currProgrammed) {
		events = append(events, albActivityEvent{
			Type:    corev1.EventTypeNormal,
			Reason:  EventReasonProgrammed,
			Note:    fmt.Sprintf("Traffic protection is live on %s", displayName),
			Related: related,
		})
	}
	if tppProgrammingFailed(prevProgrammed, currProgrammed) {
		events = append(events, albActivityEvent{
			Type:    corev1.EventTypeWarning,
			Reason:  EventReasonProgrammingFailed,
			Note:    conditionNote(currProgrammed, "Traffic protection failed to program"),
			Related: related,
		})
	}
	if tppWaitingForCertificatesFirstTime(prevAccepted, currAccepted) {
		events = append(events, albActivityEvent{
			Type:    corev1.EventTypeNormal,
			Reason:  EventReasonWaitingForCertificates,
			Note:    fmt.Sprintf("Traffic protection on %s is waiting for TLS certificates", displayName),
			Related: related,
		})
	}

	for _, ev := range events {
		emitALBActivityEvent(ctx, cl, policy, ev, displayName, ev.Related)
	}
}

func emitALBActivityEvent(
	ctx context.Context,
	cl client.Client,
	obj client.Object,
	ev albActivityEvent,
	displayName string,
	related *corev1.ObjectReference,
) {
	annotations := map[string]string{
		display.AnnotationDisplayName: displayName,
	}
	if value := obj.GetAnnotations()[display.AnnotationDisplayValue]; value != "" {
		annotations[display.AnnotationDisplayValue] = value
	}

	evt := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:        albEventName(obj.GetName()),
			Namespace:   obj.GetNamespace(),
			Annotations: annotations,
		},
		EventTime:           metav1.NewMicroTime(time.Now()),
		Action:              ev.Reason,
		Reason:              ev.Reason,
		Note:                ev.Note,
		Type:                ev.Type,
		ReportingController: albActivityReportingController,
		ReportingInstance:   albReportingInstance(),
		Regarding:           objectRef(obj),
		Related:             related,
	}

	if err := cl.Create(ctx, evt); err != nil {
		log.FromContext(ctx).Error(err, "failed to emit ALB activity event",
			"reason", ev.Reason, "name", obj.GetName(), "kind", obj.GetObjectKind().GroupVersionKind().Kind)
	}
}

func programmedBecameTrue(previous, current []metav1.Condition, condType string) bool {
	return programmedBecameTrueConditions(
		meta.FindStatusCondition(previous, condType),
		meta.FindStatusCondition(current, condType),
	)
}

func programmedBecameTrueConditions(prev, curr *metav1.Condition) bool {
	if curr == nil || curr.Status != metav1.ConditionTrue {
		return false
	}
	if prev == nil {
		return true
	}
	return prev.Status != metav1.ConditionTrue
}

func programmingFailed(previous, current []metav1.Condition) bool {
	if accepted := meta.FindStatusCondition(current, networkingv1alpha.HTTPProxyConditionAccepted); accepted != nil &&
		accepted.Reason == networkingv1alpha.HTTPProxyReasonInvalid {
		prevAccepted := meta.FindStatusCondition(previous, networkingv1alpha.HTTPProxyConditionAccepted)
		return prevAccepted == nil || prevAccepted.Reason != networkingv1alpha.HTTPProxyReasonInvalid
	}

	prev := meta.FindStatusCondition(previous, networkingv1alpha.HTTPProxyConditionProgrammed)
	curr := meta.FindStatusCondition(current, networkingv1alpha.HTTPProxyConditionProgrammed)
	if curr == nil || curr.Status != metav1.ConditionFalse {
		return false
	}
	if curr.Reason == networkingv1alpha.HTTPProxyReasonInvalid ||
		curr.Reason == networkingv1alpha.HTTPProxyReasonInstanceBackendNotFound {
		return prev == nil || prev.Reason != curr.Reason || prev.Status != metav1.ConditionFalse
	}
	return prev != nil && prev.Status == metav1.ConditionTrue
}

func becameTrue(previous, current []metav1.Condition, condType string) bool {
	prev := meta.FindStatusCondition(previous, condType)
	curr := meta.FindStatusCondition(current, condType)
	if curr == nil || curr.Status != metav1.ConditionTrue {
		return false
	}
	return prev == nil || prev.Status != metav1.ConditionTrue
}

func hostnamesUnverifiedOncePerGeneration(previous, current []metav1.Condition) bool {
	curr := meta.FindStatusCondition(current, networkingv1alpha.HTTPProxyConditionHostnamesVerified)
	if curr == nil || curr.Status != metav1.ConditionFalse || curr.Reason != networkingv1alpha.UnverifiedHostnamesPresent {
		return false
	}
	prev := meta.FindStatusCondition(previous, networkingv1alpha.HTTPProxyConditionHostnamesVerified)
	if prev == nil {
		return true
	}
	if prev.Status != metav1.ConditionFalse || prev.Reason != networkingv1alpha.UnverifiedHostnamesPresent {
		return true
	}
	return prev.ObservedGeneration != curr.ObservedGeneration
}

func certificateFailed(previous, current []metav1.Condition) bool {
	curr := meta.FindStatusCondition(current, networkingv1alpha.HTTPProxyConditionCertificatesReady)
	if curr == nil || curr.Reason != networkingv1alpha.CertificatesReadyReasonCertificatesFailed {
		return false
	}
	prev := meta.FindStatusCondition(previous, networkingv1alpha.HTTPProxyConditionCertificatesReady)
	return prev == nil || prev.Reason != curr.Reason
}

func dnsRecordFailed(previous, current []metav1.Condition) bool {
	curr := meta.FindStatusCondition(current, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
	if curr == nil || curr.Status != metav1.ConditionFalse {
		return false
	}
	if curr.Reason != networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure &&
		curr.Reason != networkingv1alpha.DNSRecordReasonFailed &&
		curr.Reason != networkingv1alpha.DNSRecordReasonConflict {
		return false
	}
	prev := meta.FindStatusCondition(previous, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
	return prev == nil || prev.Status != metav1.ConditionFalse || prev.Reason != curr.Reason
}

func tppAncestorConditions(policy *networkingv1alpha.TrafficProtectionPolicy) (accepted, programmed *metav1.Condition) {
	if policy == nil {
		return nil, nil
	}
	for i := range policy.Status.Ancestors {
		accepted = meta.FindStatusCondition(policy.Status.Ancestors[i].Conditions, string(gatewayv1.PolicyConditionAccepted))
		programmed = meta.FindStatusCondition(policy.Status.Ancestors[i].Conditions, conditionTypeProgrammed)
		if accepted != nil || programmed != nil {
			return accepted, programmed
		}
	}
	return nil, nil
}

func tppProgrammingFailed(prev, curr *metav1.Condition) bool {
	if curr == nil || curr.Status != metav1.ConditionFalse {
		return false
	}
	if curr.Reason == string(PolicyReasonProgrammedPartialFailure) {
		return prev == nil || prev.Reason != curr.Reason || prev.Status != metav1.ConditionFalse
	}
	return prev != nil && prev.Status == metav1.ConditionTrue
}

func tppWaitingForCertificatesFirstTime(prev, curr *metav1.Condition) bool {
	if curr == nil || curr.Status != metav1.ConditionFalse || curr.Reason != string(PolicyReasonWaitingForCertificates) {
		return false
	}
	if prev == nil {
		return true
	}
	return prev.Reason != string(PolicyReasonWaitingForCertificates)
}

func conditionMessage(conditions []metav1.Condition, condType, fallback string) string {
	if cond := meta.FindStatusCondition(conditions, condType); cond != nil && cond.Message != "" {
		return cond.Message
	}
	return fallback
}

func conditionNote(cond *metav1.Condition, fallback string) string {
	if cond != nil && cond.Message != "" {
		return cond.Message
	}
	return fallback
}

func lookupHTTPProxyForTPP(ctx context.Context, cl client.Client, policy *networkingv1alpha.TrafficProtectionPolicy) *networkingv1alpha.HTTPProxy {
	if cl == nil || policy == nil || len(policy.Spec.TargetRefs) == 0 {
		return nil
	}
	var proxy networkingv1alpha.HTTPProxy
	key := types.NamespacedName{Namespace: policy.Namespace, Name: string(policy.Spec.TargetRefs[0].Name)}
	if err := cl.Get(ctx, key, &proxy); err != nil {
		return nil
	}
	return &proxy
}

func objectRef(obj client.Object) corev1.ObjectReference {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		switch obj.(type) {
		case *networkingv1alpha.HTTPProxy:
			gvk = networkingv1alpha.GroupVersion.WithKind(KindHTTPProxy)
		case *networkingv1alpha.TrafficProtectionPolicy:
			gvk = networkingv1alpha.GroupVersion.WithKind(KindTrafficProtectionPolicy)
		}
	}
	return corev1.ObjectReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
	}
}

func httpProxyObjectRef(proxy *networkingv1alpha.HTTPProxy) corev1.ObjectReference {
	return corev1.ObjectReference{
		APIVersion: networkingv1alpha.GroupVersion.String(),
		Kind:       KindHTTPProxy,
		Namespace:  proxy.Namespace,
		Name:       proxy.Name,
		UID:        proxy.UID,
	}
}

func albEventName(subject string) string {
	name := fmt.Sprintf("%s.%x", subject, time.Now().UnixNano())
	if len(name) > 253 {
		return name[len(name)-253:]
	}
	return name
}

func albReportingInstance() string {
	name := os.Getenv("POD_NAME")
	if name == "" {
		if h, err := os.Hostname(); err == nil {
			name = h
		}
	}
	if name == "" {
		name = "network-services-operator"
	}
	if len(name) > 128 {
		return name[:128]
	}
	return name
}
