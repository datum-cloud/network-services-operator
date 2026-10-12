package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// downstreamTPP builds a downstream copy: its own generation, the upstream
// generation the replicator stamped on it (empty for none), and its ancestors.
func downstreamTPP(generation int64, upstreamGeneration string, ancestors ...gatewayv1.PolicyAncestorStatus) *networkingv1alpha.TrafficProtectionPolicy {
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: tppTestDownstreamNamespace, Name: "tpp-1", Generation: generation},
	}
	if upstreamGeneration != "" {
		tpp.Annotations = map[string]string{networkingv1alpha1.UpstreamGenerationAnnotation: upstreamGeneration}
	}
	tpp.Status.Ancestors = ancestors
	return tpp
}

// aggregatedAncestor is a downstream ancestor for a Gateway target.
func aggregatedAncestor(namespace, gateway string, status metav1.ConditionStatus, reason string, observedGeneration int64, message string) gatewayv1.PolicyAncestorStatus {
	return decodedAncestor(namespace, KindGateway, gateway, "", status, reason, observedGeneration, message)
}

// decodedAncestor is a downstream ancestor in the JSON form Karmada's
// aggregation writes, decoded as the controller reads it. The aggregation
// writes no sectionName for a target without one.
func decodedAncestor(namespace, kind, name, sectionName string, status metav1.ConditionStatus, reason string, observedGeneration int64, message string) gatewayv1.PolicyAncestorStatus {
	ref := map[string]any{"group": gatewayv1.GroupName, "kind": kind, "namespace": namespace, "name": name}
	if sectionName != "" {
		ref["sectionName"] = sectionName
	}
	data, err := json.Marshal(map[string]any{
		"ancestorRef":    ref,
		"controllerName": "networking.datumapis.com/envoy-gateway-extension-server",
		"conditions": []any{map[string]any{
			"type": conditionTypeProgrammed, "status": status, "reason": reason,
			"observedGeneration": observedGeneration, "message": message, "lastTransitionTime": "2026-10-06T00:00:00Z",
		}},
	})
	if err != nil {
		panic(err)
	}
	var ancestor gatewayv1.PolicyAncestorStatus
	if err := json.Unmarshal(data, &ancestor); err != nil {
		panic(err)
	}
	return ancestor
}

func sectionTarget(kind, name, section string) gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName {
	return gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.GroupName, Kind: gatewayv1.Kind(kind), Name: gatewayv1.ObjectName(name),
		},
		SectionName: ptr.To(gatewayv1.SectionName(section)),
	}
}

// TestProgrammedFromDownstream uses a downstream generation (3) that differs
// from the upstream generation (7), as the counters of two API servers do.
func TestProgrammedFromDownstream(t *testing.T) {
	t.Parallel()

	const (
		waiting    = "Waiting for policy generation 7 to be programmed on edges"
		partial    = "Policy generation 7 programmed on some edges, not yet on all"
		programmed = "Policy generation 7 programmed on edges"
	)
	ns := tppTestDownstreamNamespace
	tests := []struct {
		name       string
		downstream *networkingv1alpha.TrafficProtectionPolicy
		target     gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName
		status     metav1.ConditionStatus
		reason     gatewayv1.PolicyConditionReason
		message    string
	}{
		{
			name:   "no downstream copy",
			target: gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "every edge programmed the upstream generation",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionTrue, reason: PolicyReasonProgrammed, message: programmed,
		},
		{
			name:       "one edge on the new generation and one on the old",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionFalse, "PartialFailure", 3, "1/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPartialFailure, message: partial,
		},
		{
			name:       "no edge programmed",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionFalse, "Pending", 3, "0/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "a recreated downstream copy starts its generation again",
			downstream: downstreamTPP(1, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 1, "2/2 edges programmed generation 1")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionTrue, reason: PolicyReasonProgrammed, message: programmed,
		},
		{
			name:       "the downstream copy holds an older upstream spec",
			downstream: downstreamTPP(3, "6", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "the downstream copy carries no upstream generation",
			downstream: downstreamTPP(3, "", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "the upstream generation is not a number",
			downstream: downstreamTPP(3, "seven", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "the aggregation is behind the downstream generation",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 2, "2/2 edges programmed generation 2")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "an observed generation equal to the upstream counter is not the downstream one",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 7, "2/2 edges programmed generation 7")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name: "each target reads its own ancestor",
			downstream: downstreamTPP(3, "7",
				aggregatedAncestor(ns, "gw-b", metav1.ConditionFalse, "PartialFailure", 3, "1/2 edges programmed generation 3"),
				aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target: gatewayTarget("gw-a"), status: metav1.ConditionTrue, reason: PolicyReasonProgrammed, message: programmed,
		},
		{
			name:       "no downstream ancestor for the target",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-b", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "an ancestor in the upstream namespace is not the downstream one",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(tppTestNamespace, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "the downstream copy holds a newer upstream spec than the one read",
			downstream: downstreamTPP(3, "8", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     gatewayTarget("gw-a"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
		{
			name:       "a listener target",
			downstream: downstreamTPP(3, "7", decodedAncestor(ns, KindGateway, "gw-a", "https", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     sectionTarget(KindGateway, "gw-a", "https"), status: metav1.ConditionTrue, reason: PolicyReasonProgrammed, message: programmed,
		},
		{
			name:       "a route rule target",
			downstream: downstreamTPP(3, "7", decodedAncestor(ns, KindHTTPRoute, "web", "rule-1", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     sectionTarget(KindHTTPRoute, "web", "rule-1"), status: metav1.ConditionTrue, reason: PolicyReasonProgrammed, message: programmed,
		},
		{
			name:       "the whole Gateway's ancestor is not a listener's",
			downstream: downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3")),
			target:     sectionTarget(KindGateway, "gw-a", "https"), status: metav1.ConditionFalse, reason: PolicyReasonProgrammedPending, message: waiting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ref := getAncestorRefForTarget(tppTestDownstreamNamespace, tt.target)
			status, reason, message := programmedFromDownstream(tt.downstream, ref, 7)
			assert.Equal(t, tt.status, status)
			assert.Equal(t, tt.reason, reason)
			assert.Equal(t, tt.message, message)
		})
	}
}

// ancestorCondition returns the condition of this controller's ancestor for target.
func ancestorCondition(t *testing.T, ancestors []gatewayv1.PolicyAncestorStatus, target gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName, conditionType string) *metav1.Condition {
	t.Helper()
	ref := getAncestorRefForTarget(tppTestNamespace, target)
	for i := range ancestors {
		if string(ancestors[i].ControllerName) == testControllerName && equality.Semantic.DeepEqual(ancestors[i].AncestorRef, *ref) {
			return apimeta.FindStatusCondition(ancestors[i].Conditions, conditionType)
		}
	}
	require.Failf(t, "no ancestor", "target %s", target.Name)
	return nil
}

// TestProgrammedIgnoresTheDownstreamMessage checks that the downstream
// condition's message changes nothing: the result follows its status, reason and
// generations only.
func TestProgrammedIgnoresTheDownstreamMessage(t *testing.T) {
	t.Parallel()

	ns := tppTestDownstreamNamespace
	ref := getAncestorRefForTarget(ns, gatewayTarget("gw-a"))
	messages := []string{"", "programmed", "9/9 edges programmed generation 99", "1/2 edges programmed generation 3"}
	for _, cond := range []struct {
		status metav1.ConditionStatus
		reason string
	}{
		{metav1.ConditionTrue, "Programmed"},
		{metav1.ConditionFalse, "PartialFailure"},
		{metav1.ConditionFalse, "Pending"},
	} {
		wantStatus, wantReason, wantMessage := programmedFromDownstream(
			downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", cond.status, cond.reason, 3, "2/2 edges programmed generation 3")), ref, 7)
		for _, message := range messages {
			status, reason, got := programmedFromDownstream(
				downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", cond.status, cond.reason, 3, message)), ref, 7)
			assert.Equal(t, wantStatus, status, "reason %s, message %q", cond.reason, message)
			assert.Equal(t, wantReason, reason, "reason %s, message %q", cond.reason, message)
			assert.Equal(t, wantMessage, got, "reason %s, message %q", cond.reason, message)
		}
	}
}

// TestReconcileProgrammedFollowsTheUpstreamGeneration checks that with one edge
// on the new upstream generation and one on the old, the policy is not
// Programmed, and that the message names the upstream generation.
func TestReconcileProgrammedFollowsTheUpstreamGeneration(t *testing.T) {
	ns := tppTestDownstreamNamespace
	f := newTPPReconcileFixture(t,
		[]client.Object{upstreamPolicy(7, []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTarget("gw-a")}), upstreamGateway()},
		downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionFalse, "PartialFailure", 3, "1/2 edges programmed generation 3")),
	)

	programmed := ancestorCondition(t, f.reconcile(t), gatewayTarget("gw-a"), conditionTypeProgrammed)
	require.NotNil(t, programmed)
	assert.Equal(t, metav1.ConditionFalse, programmed.Status)
	assert.Equal(t, string(PolicyReasonProgrammedPartialFailure), programmed.Reason)
	assert.Equal(t, "Policy generation 7 programmed on some edges, not yet on all", programmed.Message)
	assert.Equal(t, int64(7), programmed.ObservedGeneration)

	// The second edge builds the generation.
	hub := downstreamTPP(3, "7", aggregatedAncestor(ns, "gw-a", metav1.ConditionTrue, "Programmed", 3, "2/2 edges programmed generation 3"))
	var current networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, f.downstream.Get(t.Context(), client.ObjectKeyFromObject(hub), &current))
	current.Status = hub.Status
	require.NoError(t, f.downstream.Update(t.Context(), &current))

	programmed = ancestorCondition(t, f.reconcile(t), gatewayTarget("gw-a"), conditionTypeProgrammed)
	require.NotNil(t, programmed)
	assert.Equal(t, metav1.ConditionTrue, programmed.Status)
	assert.Equal(t, "Policy generation 7 programmed on edges", programmed.Message)
}

// TestReconcileMissingTargetIsNotAccepted checks that a target that does not
// exist reads Accepted=False with reason TargetNotFound, and loses the
// Programmed condition it had while it existed.
func TestReconcileMissingTargetIsNotAccepted(t *testing.T) {
	httpRoute := gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{Group: gatewayv1.GroupName, Kind: KindHTTPRoute, Name: "web"},
	}
	targets := []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTarget("gw-gone"), httpRoute}
	wasProgrammed := func(target gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName) gatewayv1.PolicyAncestorStatus {
		return gatewayv1.PolicyAncestorStatus{
			AncestorRef:    *getAncestorRefForTarget(tppTestNamespace, target),
			ControllerName: testControllerName,
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.PolicyConditionAccepted), Status: metav1.ConditionTrue, Reason: "Accepted", ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
				{Type: conditionTypeProgrammed, Status: metav1.ConditionTrue, Reason: "Programmed", ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
			},
		}
	}
	f := newTPPReconcileFixture(t, []client.Object{upstreamPolicy(1, targets, wasProgrammed(targets[0]), wasProgrammed(targets[1]))})

	ancestors := f.reconcile(t)
	for _, target := range targets {
		accepted := ancestorCondition(t, ancestors, target, string(gatewayv1.PolicyConditionAccepted))
		require.NotNil(t, accepted, target.Name)
		assert.Equal(t, metav1.ConditionFalse, accepted.Status, target.Name)
		assert.Equal(t, string(gatewayv1.PolicyReasonTargetNotFound), accepted.Reason, target.Name)
		assert.Equal(t, string(target.Kind)+` "`+string(target.Name)+`" not found`, accepted.Message, target.Name)
		assert.Nil(t, ancestorCondition(t, ancestors, target, conditionTypeProgrammed), target.Name)
	}
}
