// SPDX-License-Identifier: AGPL-3.0-only

package retrigger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const testTPP = "tpp-1"

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

func reconcileTPP(t *testing.T, cl client.Client) {
	t.Helper()
	r := &TPPReconciler{Client: cl}
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

// TestTPPReconcile_StampsTargetedGateway: reconciling a TPP stamps its
// generation onto the trigger annotation of the Gateway it targets, so EG
// re-translates against the fresh cache.
func TestTPPReconcile_StampsTargetedGateway(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 7, testProxy), gateway()).
		Build()

	reconcileTPP(t, cl)

	assert.Equal(t, "7", gatewayTPPTrigger(t, cl, testProxy),
		"a targeted Gateway must be stamped with the TPP generation")
}

// TestTPPReconcile_ModeFlipChangesTrigger: a mode flip bumps the generation, so
// the trigger annotation value changes and EG re-translates.
func TestTPPReconcile_ModeFlipChangesTrigger(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(tpp(networkingv1alpha.TrafficProtectionPolicyObserve, 3, testProxy), gateway()).
		Build()

	reconcileTPP(t, cl)
	assert.Equal(t, "3", gatewayTPPTrigger(t, cl, testProxy))

	var current networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testTPP}, &current))
	current.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce
	current.Generation = 4
	require.NoError(t, cl.Update(context.Background(), &current))
	reconcileTPP(t, cl)

	assert.Equal(t, "4", gatewayTPPTrigger(t, cl, testProxy),
		"a spec change must bump the trigger annotation so EG re-translates")
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
		WithObjects(tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 2, testProxy, "proxy-2"), gateway(), gwB).
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

// TestTPPSpecChangedPredicate verifies the controller reconciles on creates and
// generation bumps, and ignores status/metadata churn and deletes.
func TestTPPSpecChangedPredicate(t *testing.T) {
	p := tppSpecChangedPredicate()

	observe := tpp(networkingv1alpha.TrafficProtectionPolicyObserve, 1, testProxy)
	enforce := tpp(networkingv1alpha.TrafficProtectionPolicyEnforce, 2, testProxy)

	assert.True(t, p.Create(event.CreateEvent{Object: observe}),
		"create is always admitted so existing TPPs stamp their Gateway on startup")
	assert.False(t, p.Delete(event.DeleteEvent{Object: observe}),
		"delete is not handled by this arm")
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: observe, ObjectNew: enforce}),
		"a generation bump (spec change) must reconcile")

	noSpecChange := observe.DeepCopy()
	noSpecChange.ResourceVersion = "999"
	assert.False(t, p.Update(event.UpdateEvent{ObjectOld: observe, ObjectNew: noSpecChange}),
		"a status/metadata-only update (same generation) must NOT reconcile")
}
