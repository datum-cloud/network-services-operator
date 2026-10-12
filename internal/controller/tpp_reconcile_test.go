package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

const (
	tppTestNamespace           = "project-ns"
	tppTestDownstreamNamespace = "ns-project-ns-uid"
)

func gatewayTarget(name string) gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName {
	return gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.GroupName, Kind: KindGateway, Name: gatewayv1.ObjectName(name),
		},
	}
}

// tppReconcileFixture runs the TrafficProtectionPolicy reconciler against fake
// upstream and downstream clients.
type tppReconcileFixture struct {
	upstream   client.Client
	downstream client.Client
	reconciler *TrafficProtectionPolicyReconciler
}

func newTPPReconcileFixture(t *testing.T, upstreamObjs []client.Object, downstreamObjs ...client.Object) *tppReconcileFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, eventsv1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tppTestNamespace, UID: types.UID("project-ns-uid")}}
	upstream := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(append(upstreamObjs, namespace)...).
		WithStatusSubresource(&networkingv1alpha.TrafficProtectionPolicy{}).
		Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).WithObjects(downstreamObjs...).Build()

	reconciler := &TrafficProtectionPolicyReconciler{
		Config:            config.NetworkServicesOperator{Gateway: config.GatewayConfig{ControllerName: testControllerName}},
		DownstreamCluster: &replicatorFakeCluster{scheme: scheme, c: downstream},
	}
	reconciler.mgr = &replicatorFakeManager{clusters: map[multicluster.ClusterName]cluster.Cluster{
		"upstream": &replicatorFakeCluster{scheme: scheme, c: upstream},
	}}
	return &tppReconcileFixture{upstream: upstream, downstream: downstream, reconciler: reconciler}
}

// reconcile runs one reconcile and returns the upstream policy's ancestors.
func (f *tppReconcileFixture) reconcile(t *testing.T) []gatewayv1.PolicyAncestorStatus {
	t.Helper()
	_, err := f.reconciler.Reconcile(t.Context(), NamespaceReconcileRequest{Namespace: tppTestNamespace, ClusterName: "upstream"})
	require.NoError(t, err)
	var policy networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, f.upstream.Get(t.Context(), client.ObjectKey{Namespace: tppTestNamespace, Name: "tpp-1"}, &policy))
	return policy.Status.Ancestors
}

func upstreamPolicy(generation int64, targets []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName, ancestors ...gatewayv1.PolicyAncestorStatus) *networkingv1alpha.TrafficProtectionPolicy {
	tpp := newTrafficProtectionPolicy(tppTestNamespace, "tpp-1", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
		tpp.Generation = generation
		tpp.Spec.TargetRefs = targets
		tpp.Status.Ancestors = ancestors
	})
	return &tpp
}

// upstreamGateway returns gw-a, the upstream Gateway the reconcile tests'
// policies target.
func upstreamGateway() *gatewayv1.Gateway {
	return &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: tppTestNamespace, Name: "gw-a"}}
}

// TestReconcileRemovesEveryAncestorThatLeftTheSpec checks that adjacent
// ancestors of removed targets are all removed, at the start of the list and at
// its end, and that another controller's ancestor is kept.
func TestReconcileRemovesEveryAncestorThatLeftTheSpec(t *testing.T) {
	const other = "example.com/other-controller"
	ancestor := func(controller, gateway string) gatewayv1.PolicyAncestorStatus {
		return gatewayv1.PolicyAncestorStatus{
			AncestorRef:    *getAncestorRefForTarget(tppTestNamespace, gatewayTarget(gateway)),
			ControllerName: gatewayv1.GatewayController(controller),
			Conditions: []metav1.Condition{{
				Type: string(gatewayv1.PolicyConditionAccepted), Status: metav1.ConditionTrue, Reason: "Accepted",
				ObservedGeneration: 1, LastTransitionTime: metav1.Now(),
			}},
		}
	}
	tests := map[string][]gatewayv1.PolicyAncestorStatus{
		"removed targets first": {
			ancestor(testControllerName, "gw-old-1"), ancestor(testControllerName, "gw-old-2"),
			ancestor(other, "gw-old-3"), ancestor(testControllerName, "gw-a"),
		},
		"removed targets last": {
			ancestor(testControllerName, "gw-a"), ancestor(other, "gw-old-3"),
			ancestor(testControllerName, "gw-old-1"), ancestor(testControllerName, "gw-old-2"),
		},
	}
	for name, ancestors := range tests {
		t.Run(name, func(t *testing.T) {
			policy := upstreamPolicy(1, []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTarget("gw-a")}, ancestors...)
			f := newTPPReconcileFixture(t, []client.Object{policy, upstreamGateway()})

			reconciled := f.reconcile(t)
			got := make([]string, 0, len(reconciled))
			for _, a := range reconciled {
				got = append(got, string(a.ControllerName)+"|"+string(a.AncestorRef.Name))
			}
			assert.ElementsMatch(t, []string{other + "|gw-old-3", testControllerName + "|gw-a"}, got)
		})
	}
}
