package server

import (
	"context"
	"fmt"
	"testing"
	"time"

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
	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
)

const otherController = "gateway.envoyproxy.io/gatewayclass-controller"

func gatewayTarget(name string) gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName {
	return gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.GroupName,
			Kind:  "Gateway",
			Name:  gatewayv1.ObjectName(name),
		},
	}
}

func testTPP(generation int64, targets ...string) *networkingv1alpha.TrafficProtectionPolicy {
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "my-tpp", Namespace: "proj-ns", Generation: generation},
		Spec:       networkingv1alpha.TrafficProtectionPolicySpec{Mode: networkingv1alpha.TrafficProtectionPolicyEnforce},
	}
	for _, target := range targets {
		tpp.Spec.TargetRefs = append(tpp.Spec.TargetRefs, gatewayTarget(target))
	}
	return tpp
}

// ancestor builds a status ancestor for target, written by controller, with
// Programmed=True at generation.
func ancestor(controller, target string, generation int64) gatewayv1.PolicyAncestorStatus {
	return gatewayv1.PolicyAncestorStatus{
		AncestorRef:    *ancestorRefForTarget("proj-ns", gatewayTarget(target)),
		ControllerName: gatewayv1.GatewayController(controller),
		Conditions: []metav1.Condition{{
			Type:               conditionTypeProgrammed,
			Status:             metav1.ConditionTrue,
			Reason:             string(conditionReasonProgrammed),
			Message:            "Policy has been programmed on this edge.",
			ObservedGeneration: generation,
			LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second)),
		}},
	}
}

func newReportServer(t *testing.T, objs ...client.Object) (*Server, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))
	var withStatus []client.Object
	for _, o := range objs {
		if _, ok := o.(*networkingv1alpha.TrafficProtectionPolicy); ok {
			withStatus = append(withStatus, o)
		}
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(withStatus...).WithObjects(objs...).Build()
	return New(cl, ServerConfig{}, discardLogger()), cl
}

func gateway(name string) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "proj-ns"}}
}

func getTPP(t *testing.T, cl client.Client) *networkingv1alpha.TrafficProtectionPolicy {
	t.Helper()
	got := &networkingv1alpha.TrafficProtectionPolicy{}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: "proj-ns", Name: "my-tpp"}, got))
	return got
}

// ancestorSummary renders ancestors as "controller|target|status|generation".
func ancestorSummary(ancestors []gatewayv1.PolicyAncestorStatus) []string {
	var out []string
	for _, a := range ancestors {
		for _, c := range a.Conditions {
			out = append(out, fmt.Sprintf("%s|%s|%s|%d", a.ControllerName, a.AncestorRef.Name, c.Status, c.ObservedGeneration))
		}
	}
	return out
}

func built(generation int64, targets ...string) *mutate.BuiltTPP {
	b := &mutate.BuiltTPP{Generation: generation}
	for _, target := range targets {
		b.Targets = append(b.Targets, gatewayTarget(target))
	}
	return b
}

func existing(gateways ...string) targetIndex {
	idx := targetIndex{gateways: map[objectKey]bool{}, routes: map[objectKey]bool{}, rules: map[objectKey]map[string]bool{}}
	for _, g := range gateways {
		idx.gateways[objectKey{"proj-ns", g}] = true
	}
	return idx
}

func TestAncestorsAfterReport(t *testing.T) {
	edge := edgeProgrammedControllerName
	inverted := func(tpp *networkingv1alpha.TrafficProtectionPolicy) *networkingv1alpha.TrafficProtectionPolicy {
		tpp.Spec.RuleSets = []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
			Type:             networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
			OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{ParanoiaLevels: networkingv1alpha.ParanoiaLevels{Blocking: 3, Detection: 1}},
		}}
		return tpp
	}
	tests := []struct {
		name    string
		policy  *networkingv1alpha.TrafficProtectionPolicy
		current []gatewayv1.PolicyAncestorStatus
		built   *mutate.BuiltTPP
		targets targetIndex
		want    []string
	}{
		{
			name:    "a build that contains the target claims it at the generation the build read",
			policy:  testTPP(4, "gw-a"),
			built:   built(3, "gw-a"),
			targets: existing("gw-a"),
			want:    []string{edge + "|gw-a|True|3"},
		},
		{
			name:    "a claim advances to a newer build",
			policy:  testTPP(5, "gw-a"),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(edge, "gw-a", 3)},
			built:   built(5, "gw-a"),
			targets: existing("gw-a"),
			want:    []string{edge + "|gw-a|True|5"},
		},
		{
			name:    "a build of an older generation never overwrites a newer claim (a replica behind)",
			policy:  testTPP(5, "gw-a"),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(edge, "gw-a", 5)},
			built:   built(4, "gw-a"),
			targets: existing("gw-a"),
			want:    []string{edge + "|gw-a|True|5"},
		},
		{
			name:    "a build without the target keeps its claim (another replica, or a partial build)",
			policy:  testTPP(3, "gw-a"),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(edge, "gw-a", 3)},
			built:   nil,
			targets: existing("gw-a"),
			want:    []string{edge + "|gw-a|True|3"},
		},
		{
			name:    "a target that left the spec loses its claim; other controllers keep theirs",
			policy:  testTPP(3, "gw-a"),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(otherController, "gw-b", 3), ancestor(edge, "gw-a", 3), ancestor(edge, "gw-b", 2)},
			built:   nil,
			targets: existing("gw-a", "gw-b"),
			want:    []string{otherController + "|gw-b|True|3", edge + "|gw-a|True|3"},
		},
		{
			name:    "a target that no longer exists loses its claim, even if a build still contains it",
			policy:  testTPP(3, "gw-a"),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(edge, "gw-a", 3)},
			built:   built(3, "gw-a"),
			targets: existing(),
			want:    []string{},
		},
		{
			name:    "an invalid policy (inverted paranoia levels) is never claimed, and loses an old claim",
			policy:  inverted(testTPP(3, "gw-a")),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(edge, "gw-a", 2)},
			built:   built(3, "gw-a"),
			targets: existing("gw-a"),
			want:    []string{},
		},
		{
			name:    "a claim for a generation the policy never had is replaced (recreated with its status, as a restore does)",
			policy:  testTPP(1, "gw-a"),
			current: []gatewayv1.PolicyAncestorStatus{ancestor(edge, "gw-a", 28)},
			built:   built(1, "gw-a"),
			targets: existing("gw-a"),
			want:    []string{edge + "|gw-a|True|1"},
		},
		{
			name:    "a target never built is not claimed",
			policy:  testTPP(1, "gw-a"),
			built:   nil,
			targets: existing("gw-a"),
			want:    []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.policy.Status.Ancestors = tt.current
			got := ancestorsAfterReport(tt.policy, tt.built, tt.targets)
			summary := ancestorSummary(got)
			if summary == nil {
				summary = []string{}
			}
			assert.Equal(t, tt.want, summary)
		})
	}
}

func TestTargetIndex_RuleScopedTargetNeedsItsRule(t *testing.T) {
	section := gatewayv1.SectionName("protected")
	ref := gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{Group: gatewayv1.GroupName, Kind: "HTTPRoute", Name: "proxy"},
		SectionName:                &section,
	}
	idx := targetIndex{routes: map[objectKey]bool{{"proj-ns", "proxy"}: true},
		rules: map[objectKey]map[string]bool{{"proj-ns", "proxy"}: {"protected": true}}}
	assert.True(t, idx.exists("proj-ns", ref))

	idx.rules[objectKey{"proj-ns", "proxy"}] = map[string]bool{"renamed": true}
	assert.False(t, idx.exists("proj-ns", ref), "a renamed rule is a target that no longer exists")
}

func TestReportProgrammed_ReadsTargetsFromTheCache(t *testing.T) {
	// gw-b's Gateway is gone: its old claim goes. gw-a exists and is built: it is claimed.
	tpp := testTPP(2, "gw-a", "gw-b")
	tpp.Status.Ancestors = []gatewayv1.PolicyAncestorStatus{ancestor(edgeProgrammedControllerName, "gw-b", 1)}
	srv, cl := newReportServer(t, tpp, gateway("gw-a"))

	srv.reportProgrammed(context.Background(), mutate.BuiltTPPs{"proj-ns/my-tpp": built(2, "gw-a", "gw-b")})

	assert.Equal(t, []string{edgeProgrammedControllerName + "|gw-a|True|2"}, ancestorSummary(getTPP(t, cl).Status.Ancestors))
	assert.Equal(t, ptr.To(gatewayv1.Namespace("proj-ns")), getTPP(t, cl).Status.Ancestors[0].AncestorRef.Namespace)
}

func TestReportProgrammed_UnchangedReportWritesNothing(t *testing.T) {
	tpp := testTPP(2, "gw-a")
	tpp.Status.Ancestors = []gatewayv1.PolicyAncestorStatus{ancestor(otherController, "gw-a", 2), ancestor(edgeProgrammedControllerName, "gw-a", 2)}
	srv, cl := newReportServer(t, tpp, gateway("gw-a"))
	before := getTPP(t, cl)

	srv.reportProgrammed(context.Background(), mutate.BuiltTPPs{"proj-ns/my-tpp": built(2, "gw-a")})
	srv.reportProgrammed(context.Background(), mutate.BuiltTPPs{})

	after := getTPP(t, cl)
	assert.Equal(t, before.ResourceVersion, after.ResourceVersion)
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
