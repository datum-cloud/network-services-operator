// SPDX-License-Identifier: AGPL-3.0-only

package retrigger

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	testTPP    = "tpp-1"
	testBootID = "boot"
)

func gatewayTargetRef(name string) gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName {
	return gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.GroupName,
			Kind:  gatewayv1.Kind("Gateway"),
			Name:  gatewayv1.ObjectName(name),
		},
	}
}

func tpp(mode networkingv1alpha.TrafficProtectionPolicyMode, generation int64, gwNames ...string) *networkingv1alpha.TrafficProtectionPolicy {
	refs := make([]gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName, 0, len(gwNames))
	for _, n := range gwNames {
		refs = append(refs, gatewayTargetRef(n))
	}
	return &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: testTPP, Namespace: testNS, Generation: generation},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode:       mode,
			TargetRefs: refs,
		},
	}
}

func withProgrammed(policy *networkingv1alpha.TrafficProtectionPolicy, observedGeneration int64) *networkingv1alpha.TrafficProtectionPolicy {
	policy = policy.DeepCopy()
	policy.Status.Ancestors = []gatewayv1.PolicyAncestorStatus{{
		AncestorRef: gatewayv1.ParentReference{
			Group:     ptr.To(gatewayv1.Group(gatewayv1.GroupName)),
			Kind:      ptr.To(gatewayv1.Kind("Gateway")),
			Namespace: ptr.To(gatewayv1.Namespace(testNS)),
			Name:      gatewayv1.ObjectName(testProxy),
		},
		ControllerName: "networking.datumapis.com/envoy-gateway-extension-server",
		Conditions: []metav1.Condition{{
			Type:               conditionTypeProgrammed,
			Status:             metav1.ConditionTrue,
			Reason:             "Programmed",
			Message:            "Policy has been programmed on this edge.",
			ObservedGeneration: observedGeneration,
			LastTransitionTime: metav1.Now(),
		}},
	}}
	return policy
}

func newTPPReconciler(cl client.Client) *TPPReconciler {
	return &TPPReconciler{Client: cl, bootID: testBootID}
}

func reconcileTPP(t *testing.T, cl client.Client) {
	t.Helper()
	reconcileTPPWith(t, newTPPReconciler(cl))
}

// reconcileTPPWith reconciles through a caller-owned reconciler so its in-memory
// previous-target map survives across reconciles (needed to exercise delete and
// targetRef removal).
func reconcileTPPWith(t *testing.T, r *TPPReconciler) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testTPP},
	})
	require.NoError(t, err)
}

func gatewayTPPTrigger(t *testing.T, cl client.Client, gwName string) string {
	t.Helper()
	var gw gatewayv1.Gateway
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: gwName}, &gw))
	return gw.Annotations[tppTriggerAnnotationKey(testTPP)]
}

func backfillTrigger(generation int64) string {
	return strconv.FormatInt(generation, 10) + ".programmed." + testBootID
}

// TestTPPReconcile_StampsTargetedGateway: reconciling a programmed TPP stamps
// its bare generation onto the trigger annotation of the Gateway it targets.
func TestTPPReconcile_StampsTargetedGateway(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 7, testProxy), 7),
			gateway(),
		).
		Build()

	reconcileTPP(t, cl)

	assert.Equal(t, "7", gatewayTPPTrigger(t, cl, testProxy),
		"a targeted Gateway must be stamped with the TPP generation")
}

// TestTPPReconcile_BackfillWhenProgrammedMissing: a Create-on-startup reconcile
// must change the trigger even when the annotation already equals the
// generation, so EG re-translates and the extension can stamp Programmed.
func TestTPPReconcile_BackfillWhenProgrammedMissing(t *testing.T) {
	scheme := testScheme(t)
	gw := gateway()
	gw.Annotations = map[string]string{tppTriggerAnnotationKey(testTPP): "7"}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 7, testProxy), gw).
		Build()

	reconcileTPP(t, cl)

	assert.Equal(t, backfillTrigger(7), gatewayTPPTrigger(t, cl, testProxy),
		"missing Programmed must force a distinct trigger so EG re-translates")
}

// TestTPPReconcile_BackfillWhenObservedGenerationStale: Programmed=True for an
// older generation still needs a nudge for the current generation.
func TestTPPReconcile_BackfillWhenObservedGenerationStale(t *testing.T) {
	scheme := testScheme(t)
	gw := gateway()
	gw.Annotations = map[string]string{tppTriggerAnnotationKey(testTPP): "7"}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 7, testProxy), 6),
			gw,
		).
		Build()

	reconcileTPP(t, cl)

	assert.Equal(t, backfillTrigger(7), gatewayTPPTrigger(t, cl, testProxy),
		"stale Programmed observedGeneration must force a backfill trigger")
}

// TestTPPReconcile_NoBackfillWhenProgrammedCurrent: once Programmed matches
// the generation, the bare generation stamp stays idempotent.
func TestTPPReconcile_NoBackfillWhenProgrammedCurrent(t *testing.T) {
	scheme := testScheme(t)
	gw := gateway()
	gw.Annotations = map[string]string{tppTriggerAnnotationKey(testTPP): "7"}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 7, testProxy), 7),
			gw,
		).
		Build()

	reconcileTPP(t, cl)

	assert.Equal(t, "7", gatewayTPPTrigger(t, cl, testProxy),
		"current Programmed must keep the bare generation trigger (no-op patch)")
}

// TestTPPReconcile_ModeFlipChangesTrigger: a mode flip bumps the generation, so
// the trigger annotation value changes and EG re-translates.
func TestTPPReconcile_ModeFlipChangesTrigger(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyObserve, 3, testProxy), 3),
			gateway(),
		).
		Build()

	reconcileTPP(t, cl)
	assert.Equal(t, "3", gatewayTPPTrigger(t, cl, testProxy))

	var current networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testTPP}, &current))
	current.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce
	current.Generation = 4
	// Spec change invalidates prior Programmed for generation 3.
	current.Status = networkingv1alpha.TrafficProtectionPolicyStatus{}
	require.NoError(t, cl.Update(context.Background(), &current))
	reconcileTPP(t, cl)

	assert.Equal(t, backfillTrigger(4), gatewayTPPTrigger(t, cl, testProxy),
		"a spec change without Programmed yet must use the backfill trigger")
}

// TestTPPReconcile_NoGateway_NoError: a missing Gateway is ignored — EG reads
// the fresh cache when it later creates the Gateway.
func TestTPPReconcile_NoGateway_NoError(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 1, testProxy)).
		Build()

	reconcileTPP(t, cl) // require.NoError inside
}

// TestTPPReconcile_MultipleTargets stamps every targeted Gateway.
func TestTPPReconcile_MultipleTargets(t *testing.T) {
	scheme := testScheme(t)
	gwB := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "proxy-2", Namespace: testNS}}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 2, testProxy, "proxy-2"), 2),
			gateway(),
			gwB,
		).
		Build()

	reconcileTPP(t, cl)

	assert.Equal(t, "2", gatewayTPPTrigger(t, cl, testProxy))
	assert.Equal(t, "2", gatewayTPPTrigger(t, cl, "proxy-2"))
}

// TestTPPReconcile_PerTPPKey: distinct TPPs targeting the same Gateway use
// distinct annotation keys and do not clobber each other.
func TestTPPReconcile_PerTPPKey(t *testing.T) {
	assert.NotEqual(t, tppTriggerAnnotationKey("a"), tppTriggerAnnotationKey("b"),
		"each TPP must own a distinct annotation slot to avoid flip-flop churn")
}

// TestTPPReconcile_DeletionClearsGateways: after a TPP is stamped onto its
// Gateway, deleting the TPP clears the trigger annotation so EG re-translates
// against the now-empty cache and drops the orphaned WAF program.
func TestTPPReconcile_DeletionClearsGateways(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 5, testProxy), 5),
			gateway(),
		).
		Build()
	r := newTPPReconciler(cl)

	reconcileTPPWith(t, r)
	require.Equal(t, "5", gatewayTPPTrigger(t, cl, testProxy))

	var current networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testTPP}, &current))
	require.NoError(t, cl.Delete(context.Background(), &current))
	reconcileTPPWith(t, r)

	assert.Empty(t, gatewayTPPTrigger(t, cl, testProxy),
		"deleting a TPP must clear its trigger annotation so EG drops the orphaned WAF")
}

// TestTPPReconcile_TargetRefRemovalClearsDropped: dropping a Gateway from
// spec.targetRefs clears that Gateway's trigger annotation while the retained
// target is re-stamped with the new generation.
func TestTPPReconcile_TargetRefRemovalClearsDropped(t *testing.T) {
	scheme := testScheme(t)
	gwB := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "proxy-2", Namespace: testNS}}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 2, testProxy, "proxy-2"), 2),
			gateway(),
			gwB,
		).
		Build()
	r := newTPPReconciler(cl)

	reconcileTPPWith(t, r)
	require.Equal(t, "2", gatewayTPPTrigger(t, cl, testProxy))
	require.Equal(t, "2", gatewayTPPTrigger(t, cl, "proxy-2"))

	var current networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testTPP}, &current))
	current.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTargetRef(testProxy)}
	current.Generation = 3
	current.Status = networkingv1alpha.TrafficProtectionPolicyStatus{}
	require.NoError(t, cl.Update(context.Background(), &current))
	reconcileTPPWith(t, r)

	assert.Equal(t, backfillTrigger(3), gatewayTPPTrigger(t, cl, testProxy),
		"a retained target must be re-stamped for the new generation")
	assert.Empty(t, gatewayTPPTrigger(t, cl, "proxy-2"),
		"a dropped target must have its trigger annotation cleared")
}

// TestTPPSpecChangedPredicate verifies the controller reconciles on creates,
// generation bumps, and deletes, and ignores status/metadata churn.
func TestTPPSpecChangedPredicate(t *testing.T) {
	p := tppSpecChangedPredicate()

	observe := tpp(networkingv1alpha.TrafficProtectionPolicyObserve, 1, testProxy)
	enforce := tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 2, testProxy)

	assert.True(t, p.Create(event.CreateEvent{Object: observe}),
		"create is always admitted so existing TPPs stamp their Gateway on startup")
	assert.True(t, p.Delete(event.DeleteEvent{Object: observe}),
		"delete is admitted so a removed TPP's Gateways are re-translated")
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: observe, ObjectNew: enforce}),
		"a generation bump (spec change) must reconcile")

	noSpecChange := observe.DeepCopy()
	noSpecChange.ResourceVersion = "999"
	assert.False(t, p.Update(event.UpdateEvent{ObjectOld: observe, ObjectNew: noSpecChange}),
		"a status/metadata-only update (same generation) must NOT reconcile")
}

func TestTPPProgrammedForGeneration(t *testing.T) {
	assert.False(t, tppProgrammedForGeneration(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 3, testProxy)))
	assert.False(t, tppProgrammedForGeneration(withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 3, testProxy), 2)))
	assert.True(t, tppProgrammedForGeneration(withProgrammed(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 3, testProxy), 3)))
}
