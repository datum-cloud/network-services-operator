package server

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestMarkTPPsProgrammed_SetsEdgeStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "my-tpp",
			Namespace:  "proj-ns",
			Generation: 3,
		},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyEnforce,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Group: gatewayv1.GroupName,
					Kind:  "Gateway",
					Name:  "edge-gw",
				},
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(tpp).
		WithObjects(tpp).
		Build()

	s := New(cl, ServerConfig{}, slog.Default())
	s.markTPPsProgrammed(context.Background(), map[string]int64{"proj-ns/my-tpp": 3})

	got := &networkingv1alpha.TrafficProtectionPolicy{}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(tpp), got))
	require.Len(t, got.Status.Ancestors, 1)
	require.Len(t, got.Status.Ancestors[0].Conditions, 1)

	cond := got.Status.Ancestors[0].Conditions[0]
	assert.Equal(t, conditionTypeProgrammed, cond.Type)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, string(conditionReasonProgrammed), cond.Reason)
	assert.Equal(t, int64(3), cond.ObservedGeneration)
	assert.Equal(t, gatewayv1.ObjectName("edge-gw"), got.Status.Ancestors[0].AncestorRef.Name)
	assert.Equal(t, ptr.To(gatewayv1.Namespace("proj-ns")), got.Status.Ancestors[0].AncestorRef.Namespace)
}

func TestMarkTPPsProgrammed_Idempotent(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "my-tpp", Namespace: "proj-ns", Generation: 2},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Group: gatewayv1.GroupName,
					Kind:  "Gateway",
					Name:  "edge-gw",
				},
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(tpp).
		WithObjects(tpp).
		Build()

	s := New(cl, ServerConfig{}, slog.Default())
	applied := map[string]int64{"proj-ns/my-tpp": 2}
	s.markTPPsProgrammed(context.Background(), applied)
	s.markTPPsProgrammed(context.Background(), applied)

	got := &networkingv1alpha.TrafficProtectionPolicy{}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(tpp), got))
	require.Len(t, got.Status.Ancestors, 1)
	require.Len(t, got.Status.Ancestors[0].Conditions, 1)
}

func TestSplitNamespaceName(t *testing.T) {
	ns, name, ok := splitNamespaceName("a/b")
	assert.True(t, ok)
	assert.Equal(t, "a", ns)
	assert.Equal(t, "b", name)

	_, _, ok = splitNamespaceName("noslash")
	assert.False(t, ok)
	_, _, ok = splitNamespaceName("a/b/c")
	assert.False(t, ok)
}
