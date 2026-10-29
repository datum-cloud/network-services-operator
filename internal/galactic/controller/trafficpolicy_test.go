// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	galacticv1alpha1 "go.datum.net/galactic/api/v1alpha1"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	testNamespace = "tenant"
	testInterface = "nic-1"
	testContext   = "default-us-central-1"
	testNode      = "node-a"
)

type policyScenario struct {
	t      *testing.T
	ctx    context.Context
	client client.Client
	r      *NetworkInterfaceReconciler
	writes []string
}

func newPolicyScenario(t *testing.T, mode networkingv1alpha.NetworkInternetEgressMode, enforced bool) *policyScenario {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		networkingv1alpha.AddToScheme, cloudv1alpha1.AddToScheme, galacticv1alpha1.AddToScheme, nadv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: testContext, Namespace: testNamespace},
	}
	if mode != "" {
		networkContext.Spec.Egress = &networkingv1alpha.NetworkContextEgress{
			Internet: &networkingv1alpha.NetworkContextInternetEgress{Mode: mode},
		}
	}
	vpc := &cloudv1alpha1.VPC{ObjectMeta: metav1.ObjectMeta{Name: testContext, Namespace: testNamespace}}
	vpc.Status.VPC = "3kF9qP2x"
	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: testInterface, Namespace: testNamespace, UID: types.UID("iface-uid")},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			InterfaceName: "eth0",
			MTU:           1440,
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{
				Family: networkingv1alpha.IPv6Protocol, Address: "fd20:a1b:2c3d:1:0:1::/96",
				Gateway: "fd20:a1b:2c3d:1::1", Primary: true,
			}},
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase:             networkingv1alpha.NetworkInterfacePhaseBound,
			NetworkContextRef: &networkingv1alpha.LocalNetworkContextRef{Name: testContext},
			Conditions: []metav1.Condition{{
				Type: networkingv1alpha.NetworkInterfaceAllocated, Status: metav1.ConditionTrue,
				Reason: "Allocated", LastTransitionTime: metav1.Now(),
			}},
		},
	}

	s := &policyScenario{t: t, ctx: context.Background()}
	s.client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(networkContext, vpc, iface).
		WithStatusSubresource(iface, &cloudv1alpha1.VPCAttachment{}, &galacticv1alpha1.TrafficPolicyBinding{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				kind, err := c.GroupVersionKindFor(obj)
				if err != nil {
					return err
				}
				s.writes = append(s.writes, kind.Kind)
				return c.Create(ctx, obj, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object,
				opts ...client.SubResourceUpdateOption) error {
				if networkInterface, ok := obj.(*networkingv1alpha.NetworkInterface); ok &&
					apimeta.IsStatusConditionTrue(networkInterface.Status.Conditions, ConditionTypePrepared) {
					s.writes = append(s.writes, "Prepared")
				}
				return c.SubResource(subResource).Update(ctx, obj, opts...)
			},
		}).
		Build()
	s.r = &NetworkInterfaceReconciler{
		Client: s.client, APIReader: s.client, Scheme: scheme,
		AttachmentMode:        cloudv1alpha1.VPCAttachmentInterfaceModeHypervisor,
		TrafficPolicyEnforced: enforced,
	}
	return s
}

func (s *policyScenario) reconcile() {
	s.t.Helper()
	if _, err := s.r.Reconcile(s.ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testInterface},
	}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *policyScenario) policy(suffix string) *galacticv1alpha1.TrafficPolicy {
	s.t.Helper()
	var policy galacticv1alpha1.TrafficPolicy
	if err := s.client.Get(s.ctx, types.NamespacedName{Namespace: testNamespace, Name: testInterface + suffix}, &policy); err != nil {
		s.t.Fatal(err)
	}
	return &policy
}

func (s *policyScenario) binding() *galacticv1alpha1.TrafficPolicyBinding {
	s.t.Helper()
	var binding galacticv1alpha1.TrafficPolicyBinding
	if err := s.client.Get(s.ctx, types.NamespacedName{Namespace: testNamespace, Name: testInterface}, &binding); err != nil {
		s.t.Fatal(err)
	}
	return &binding
}

func (s *policyScenario) internetCondition() *metav1.Condition {
	s.t.Helper()
	var iface networkingv1alpha.NetworkInterface
	if err := s.client.Get(s.ctx, types.NamespacedName{Namespace: testNamespace, Name: testInterface}, &iface); err != nil {
		s.t.Fatal(err)
	}
	return apimeta.FindStatusCondition(iface.Status.Conditions, networkingv1alpha.NetworkInterfaceInternetEgressReady)
}

func (s *policyScenario) setMode(mode networkingv1alpha.NetworkInternetEgressMode) {
	s.t.Helper()
	var networkContext networkingv1alpha.NetworkContext
	if err := s.client.Get(s.ctx, types.NamespacedName{Namespace: testNamespace, Name: testContext}, &networkContext); err != nil {
		s.t.Fatal(err)
	}
	networkContext.Spec.Egress = &networkingv1alpha.NetworkContextEgress{
		Internet: &networkingv1alpha.NetworkContextInternetEgress{Mode: mode},
	}
	if err := s.client.Update(s.ctx, &networkContext); err != nil {
		s.t.Fatal(err)
	}
}

// landOnNode stands in for the CNI recording the node and for that node's
// router reporting what it installed.
func (s *policyScenario) landOnNode(state galacticv1alpha1.AppliedState) {
	s.t.Helper()
	var attachment cloudv1alpha1.VPCAttachment
	if err := s.client.Get(s.ctx, types.NamespacedName{Namespace: testNamespace, Name: testInterface}, &attachment); err != nil {
		s.t.Fatal(err)
	}
	attachment.Status.Node = testNode
	if err := s.client.Status().Update(s.ctx, &attachment); err != nil {
		s.t.Fatal(err)
	}

	applied := func(policy *galacticv1alpha1.TrafficPolicy) *galacticv1alpha1.AppliedPolicy {
		return &galacticv1alpha1.AppliedPolicy{
			State: state,
			Policy: &galacticv1alpha1.PolicyIdentity{
				Name: policy.Name, UID: string(policy.UID), Generation: policy.Generation,
			},
		}
	}
	binding := s.binding()
	binding.Status.Nodes = []galacticv1alpha1.NodeBindingStatus{{
		Node:    testNode,
		Egress:  applied(s.policy(egressPolicySuffix)),
		Ingress: applied(s.policy(ingressPolicySuffix)),
	}}
	if err := s.client.Status().Update(s.ctx, binding); err != nil {
		s.t.Fatal(err)
	}
}

func ruleActions(policy *galacticv1alpha1.TrafficPolicy) map[galacticv1alpha1.ForwardingPath]galacticv1alpha1.RuleAction {
	actions := map[galacticv1alpha1.ForwardingPath]galacticv1alpha1.RuleAction{}
	for _, rule := range policy.Spec.Rules {
		actions[*rule.Match.Path] = rule.Action
	}
	return actions
}

func TestEachInterfaceGetsItsOwnPoliciesAndBinding(t *testing.T) {
	s := newPolicyScenario(t, networkingv1alpha.NetworkInternetEgressEnabled, true)
	s.reconcile()

	binding := s.binding()
	if binding.Spec.EgressPolicyRef == nil || binding.Spec.EgressPolicyRef.Name != testInterface+egressPolicySuffix {
		t.Fatalf("egress reference = %#v", binding.Spec.EgressPolicyRef)
	}
	if binding.Spec.IngressPolicyRef == nil || binding.Spec.IngressPolicyRef.Name != testInterface+ingressPolicySuffix {
		t.Fatalf("ingress reference = %#v", binding.Spec.IngressPolicyRef)
	}
	for _, object := range []client.Object{binding, s.policy(egressPolicySuffix), s.policy(ingressPolicySuffix)} {
		owner := metav1.GetControllerOf(object)
		if owner == nil || owner.Kind != "NetworkInterface" || owner.Name != testInterface {
			t.Fatalf("%s is owned by %#v, want the interface", object.GetName(), owner)
		}
	}
}

func TestInternetAccessDecidesOnlyTheTranslatorPath(t *testing.T) {
	tests := []struct {
		mode     networkingv1alpha.NetworkInternetEgressMode
		internet galacticv1alpha1.RuleAction
	}{
		{networkingv1alpha.NetworkInternetEgressEnabled, galacticv1alpha1.ActionAllow},
		{networkingv1alpha.NetworkInternetEgressDisabled, galacticv1alpha1.ActionDeny},
		{"", galacticv1alpha1.ActionAllow},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			s := newPolicyScenario(t, test.mode, true)
			s.reconcile()

			for _, suffix := range []string{egressPolicySuffix, ingressPolicySuffix} {
				actions := ruleActions(s.policy(suffix))
				if actions[galacticv1alpha1.PathPrivate] != galacticv1alpha1.ActionAllow {
					t.Errorf("%s: private traffic must always be allowed", suffix)
				}
				if actions[galacticv1alpha1.PathPublicService] != galacticv1alpha1.ActionAllow {
					t.Errorf("%s: traffic through an external address is out of scope and must stay allowed", suffix)
				}
				if actions[galacticv1alpha1.PathPublicNAT] != test.internet {
					t.Errorf("%s: internet path = %q, want %q", suffix, actions[galacticv1alpha1.PathPublicNAT], test.internet)
				}
			}
		})
	}
}

// Galactic denies everything on an interface whose binding has not arrived,
// so the binding has to exist before anything lets an instance attach.
func TestPolicyIsWrittenBeforeTheInstanceCanAttach(t *testing.T) {
	s := newPolicyScenario(t, networkingv1alpha.NetworkInternetEgressDisabled, true)
	s.reconcile()

	index := map[string]int{}
	for i, write := range s.writes {
		if _, seen := index[write]; !seen {
			index[write] = i
		}
	}
	for _, before := range []string{"TrafficPolicy", "TrafficPolicyBinding"} {
		for _, after := range []string{"NetworkAttachmentDefinition", "Prepared"} {
			b, okBefore := index[before]
			a, okAfter := index[after]
			if !okBefore || !okAfter || b > a {
				t.Fatalf("%s must be written before %s; writes were %v", before, after, s.writes)
			}
		}
	}
}

func TestTurningInternetOffReachesARunningInstance(t *testing.T) {
	s := newPolicyScenario(t, networkingv1alpha.NetworkInternetEgressEnabled, true)
	s.reconcile()
	s.landOnNode(galacticv1alpha1.StateApplied)
	s.reconcile()
	bindingBefore := s.binding().Spec.DeepCopy()

	s.setMode(networkingv1alpha.NetworkInternetEgressDisabled)
	requests := s.r.interfacesForNetworkContext(s.ctx, &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: testContext, Namespace: testNamespace},
	})
	if len(requests) != 1 || requests[0].Name != testInterface {
		t.Fatalf("a context change must wake its interfaces, got %v", requests)
	}
	s.reconcile()

	for _, suffix := range []string{egressPolicySuffix, ingressPolicySuffix} {
		if got := ruleActions(s.policy(suffix))[galacticv1alpha1.PathPublicNAT]; got != galacticv1alpha1.ActionDeny {
			t.Fatalf("%s: internet path = %q after turning access off", suffix, got)
		}
	}
	if after := s.binding().Spec; *after.EgressPolicyRef != *bindingBefore.EgressPolicyRef ||
		*after.IngressPolicyRef != *bindingBefore.IngressPolicyRef {
		t.Fatal("the binding must keep pointing at the same policies, so the node updates them in place")
	}
}

func TestInternetEgressState(t *testing.T) {
	enabled := networkingv1alpha.NetworkInternetEgressEnabled
	disabled := networkingv1alpha.NetworkInternetEgressDisabled

	policy := func(name string, generation int64) *galacticv1alpha1.TrafficPolicy {
		return &galacticv1alpha1.TrafficPolicy{ObjectMeta: metav1.ObjectMeta{
			Name: name, UID: types.UID(name + "-uid"), Generation: generation,
		}}
	}
	egress, ingress := policy("nic-egress", 2), policy("nic-ingress", 2)
	applied := func(p *galacticv1alpha1.TrafficPolicy, state galacticv1alpha1.AppliedState, generation int64) *galacticv1alpha1.AppliedPolicy {
		return &galacticv1alpha1.AppliedPolicy{State: state, Policy: &galacticv1alpha1.PolicyIdentity{
			Name: p.Name, UID: string(p.UID), Generation: generation,
		}}
	}
	policies := func(nodes ...galacticv1alpha1.NodeBindingStatus) *interfacePolicies {
		binding := &galacticv1alpha1.TrafficPolicyBinding{}
		binding.Status.Nodes = nodes
		return &interfacePolicies{egress: egress, ingress: ingress, binding: binding}
	}
	onNode := func(node string) *cloudv1alpha1.VPCAttachment {
		attachment := &cloudv1alpha1.VPCAttachment{}
		attachment.Status.Node = node
		return attachment
	}
	installed := galacticv1alpha1.NodeBindingStatus{
		Node: testNode, Egress: applied(egress, galacticv1alpha1.StateApplied, 2),
		Ingress: applied(ingress, galacticv1alpha1.StateApplied, 2),
	}
	stale := galacticv1alpha1.NodeBindingStatus{
		Node: testNode, Egress: applied(egress, galacticv1alpha1.StateApplied, 1),
		Ingress: applied(ingress, galacticv1alpha1.StateApplied, 2),
	}
	failed := galacticv1alpha1.NodeBindingStatus{
		Node: testNode, Egress: applied(egress, galacticv1alpha1.StateFailed, 2),
		Ingress: applied(ingress, galacticv1alpha1.StateApplied, 2),
	}
	elsewhere := installed
	elsewhere.Node = "node-b"

	tests := []struct {
		name       string
		enforced   bool
		mode       networkingv1alpha.NetworkInternetEgressMode
		attachment *cloudv1alpha1.VPCAttachment
		policies   *interfacePolicies
		status     metav1.ConditionStatus
		reason     string
	}{
		{"enabled and enforced", true, enabled, onNode(testNode), policies(installed),
			metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceInternetEgressReasonEnabled},
		{"disabled and enforced", true, disabled, onNode(testNode), policies(installed),
			metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceInternetEgressReasonDisabled},
		{"disabled but not enforced never claims to block", false, disabled, onNode(testNode), policies(installed),
			metav1.ConditionFalse, networkingv1alpha.NetworkInterfaceInternetEgressReasonNotEnforced},
		{"enabled and not enforced reaches the internet", false, enabled, onNode(""), policies(),
			metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceInternetEgressReasonEnabled},
		{"not on a node yet", true, disabled, onNode(""), policies(),
			metav1.ConditionUnknown, networkingv1alpha.NetworkInterfaceInternetEgressReasonPending},
		{"the node has not reported", true, disabled, onNode(testNode), policies(elsewhere),
			metav1.ConditionUnknown, networkingv1alpha.NetworkInterfaceInternetEgressReasonPending},
		{"the node still runs an earlier generation", true, disabled, onNode(testNode), policies(stale),
			metav1.ConditionUnknown, networkingv1alpha.NetworkInterfaceInternetEgressReasonPending},
		{"the node failed", true, disabled, onNode(testNode), policies(failed),
			metav1.ConditionFalse, networkingv1alpha.NetworkInterfaceInternetEgressReasonFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, reason, _ := internetEgressState(test.enforced, test.mode, test.attachment, test.policies)
			if status != test.status || reason != test.reason {
				t.Fatalf("got %s/%s, want %s/%s", status, reason, test.status, test.reason)
			}
		})
	}
}

func TestInternetEgressDisabledNamesAnExternalAddress(t *testing.T) {
	disabled := networkingv1alpha.NetworkInternetEgressDisabled
	egress := &galacticv1alpha1.TrafficPolicy{ObjectMeta: metav1.ObjectMeta{Name: "nic-egress", UID: "e", Generation: 1}}
	ingress := &galacticv1alpha1.TrafficPolicy{ObjectMeta: metav1.ObjectMeta{Name: "nic-ingress", UID: "i", Generation: 1}}
	applied := func(p *galacticv1alpha1.TrafficPolicy) *galacticv1alpha1.AppliedPolicy {
		return &galacticv1alpha1.AppliedPolicy{State: galacticv1alpha1.StateApplied, Policy: &galacticv1alpha1.PolicyIdentity{
			Name: p.Name, UID: string(p.UID), Generation: p.Generation,
		}}
	}
	binding := &galacticv1alpha1.TrafficPolicyBinding{}
	binding.Status.Nodes = []galacticv1alpha1.NodeBindingStatus{{Node: testNode, Egress: applied(egress), Ingress: applied(ingress)}}
	policies := &interfacePolicies{egress: egress, ingress: ingress, binding: binding}
	attachment := &cloudv1alpha1.VPCAttachment{}
	attachment.Status.Node = testNode

	r := &NetworkInterfaceReconciler{TrafficPolicyEnforced: true}
	for _, test := range []struct {
		name     string
		external []networkingv1alpha.NetworkInterfaceExternalAddress
		want     string
	}{
		{"private only", nil, "nothing outside the platform"},
		{"with an external address", []networkingv1alpha.NetworkInterfaceExternalAddress{
			{Family: networkingv1alpha.IPv4Protocol, Address: "203.0.113.10"},
		}, "has an external address"},
	} {
		t.Run(test.name, func(t *testing.T) {
			networkInterface := &networkingv1alpha.NetworkInterface{}
			networkInterface.Spec.ExternalAddresses = test.external
			r.setInternetEgressCondition(networkInterface, attachment, policies, disabled)
			condition := apimeta.FindStatusCondition(networkInterface.Status.Conditions,
				networkingv1alpha.NetworkInterfaceInternetEgressReady)
			if condition == nil || condition.Reason != networkingv1alpha.NetworkInterfaceInternetEgressReasonDisabled ||
				!strings.Contains(condition.Message, test.want) {
				t.Fatalf("condition = %#v, want Disabled mentioning %q", condition, test.want)
			}
		})
	}
}

func TestInternetEgressConditionFollowsTheNode(t *testing.T) {
	s := newPolicyScenario(t, networkingv1alpha.NetworkInternetEgressDisabled, true)
	s.reconcile()
	if condition := s.internetCondition(); condition == nil ||
		condition.Reason != networkingv1alpha.NetworkInterfaceInternetEgressReasonPending {
		t.Fatalf("condition = %#v, want Pending before the instance starts", condition)
	}

	s.landOnNode(galacticv1alpha1.StateApplied)
	s.reconcile()
	if condition := s.internetCondition(); condition == nil || condition.Status != metav1.ConditionTrue ||
		condition.Reason != networkingv1alpha.NetworkInterfaceInternetEgressReasonDisabled {
		t.Fatalf("condition = %#v, want True/Disabled once the node applies the policy", condition)
	}
}

func TestAnUnenforcedCellNeverReportsANetworkAsPrivate(t *testing.T) {
	s := newPolicyScenario(t, networkingv1alpha.NetworkInternetEgressDisabled, false)
	s.reconcile()
	s.landOnNode(galacticv1alpha1.StateApplied)
	s.reconcile()

	condition := s.internetCondition()
	if condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.Reason != networkingv1alpha.NetworkInterfaceInternetEgressReasonNotEnforced {
		t.Fatalf("condition = %#v, want False/NotEnforced", condition)
	}
	if got := ruleActions(s.policy(egressPolicySuffix))[galacticv1alpha1.PathPublicNAT]; got != galacticv1alpha1.ActionDeny {
		t.Fatalf("the policy must still deny the internet, got %q", got)
	}
}
