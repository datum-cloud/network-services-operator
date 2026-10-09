// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cloudv1alpha1 "go.datum.net/cloud/api/v1alpha1"
	networkv1alpha1 "go.datum.net/network/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/dnsaccess"
)

func TestPolicyProgrammedRequiresExactCurrentNodeReports(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	deadline := metav1.NewTime(now.Add(time.Minute))
	consumer := readyAttachment("tenant", "consumer", "consumer-uid", "blue", "vpc-fabric-id", "node-a", "consumer0", map[string]string{
		internalDNSConsumerVPCLabel: "vpc-uid",
	})
	producer := readyAttachment("tenant", "producer", "producer-uid", "dns-service", "service-vpc-id", "node-b", "producer0", map[string]string{
		"service.datum.net/internal-dns": "true",
	})
	endpoint := &networkv1alpha1.ServiceEndpoint{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "dns", UID: "endpoint-uid", Generation: 2},
		Spec: networkv1alpha1.ServiceEndpointSpec{
			ServiceClass: "internal-dns", Address: "fd70:100::10", Port: 53,
			Protocol:     networkv1alpha1.NetworkRuleProtocolUDP,
			DeliveryMode: networkv1alpha1.ServiceEndpointDeliveryModePreferNodeLocal,
			AttachmentSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"service.datum.net/internal-dns": "true",
			}},
		},
	}
	policy := &networkv1alpha1.ServiceRoutePolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "dns", UID: "policy-uid", Generation: 3},
		Spec: networkv1alpha1.ServiceRoutePolicySpec{
			ServiceRef:     networkv1alpha1.ServiceEndpointReference{Name: endpoint.Name},
			ConsumerVPCRef: &networkv1alpha1.ServiceRouteVPCReference{Name: "blue", UID: "vpc-uid"},
			Frontend:       &networkv1alpha1.ServiceRouteFrontend{Address: "fd53::53"},
			Authorization:  &networkv1alpha1.ServiceRouteAuthorization{ValidUntil: deadline},
			AttachmentSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				internalDNSConsumerVPCLabel: "vpc-uid",
			}},
			ProtocolPorts: []networkv1alpha1.ServiceRouteProtocolPort{{
				Protocol: networkv1alpha1.NetworkRuleProtocolUDP, Port: 53,
			}},
		},
		Status: networkv1alpha1.ServiceRoutePolicyStatus{
			ObservedGeneration: 3,
			Conditions: []metav1.Condition{{
				Type: networkv1alpha1.ConditionTypeServiceRouteAccepted, Status: metav1.ConditionTrue,
				Reason: "Accepted",
			}},
		},
	}
	attachments := []*cloudv1alpha1.VPCAttachment{consumer, producer}
	plan, err := dnsaccess.ProgrammingPlan(policy, endpoint, attachments, "vpc-fabric-id")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range plan.Nodes {
		policy.Status.Nodes = append(policy.Status.Nodes, networkv1alpha1.ServiceRouteNodeStatus{
			NodeName: node, PolicyUID: string(policy.UID), ObservedGeneration: policy.Generation,
			InputDigest: plan.InputDigest, Ready: true, ValidUntil: deadline,
		})
	}
	if until, ready := policyProgrammed(policy, endpoint, attachments, "vpc-fabric-id", now); !ready || !until.Equal(deadline.Time) {
		t.Fatalf("current exact reports not accepted: ready=%v until=%v", ready, until)
	}

	policy.Status.Nodes[0].InputDigest = "stale"
	if _, ready := policyProgrammed(policy, endpoint, attachments, "vpc-fabric-id", now); ready {
		t.Fatal("stale input digest was accepted")
	}
	policy.Status.Nodes[0].InputDigest = plan.InputDigest
	policy.Status.Nodes[0].ValidUntil = metav1.NewTime(now.Add(-time.Second))
	if _, ready := policyProgrammed(policy, endpoint, attachments, "vpc-fabric-id", now); ready {
		t.Fatal("expired node report was accepted")
	}
}

func TestNetworkAuthorizationDeadlineNeverOutlivesDNS(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	dnsDeadline := now.Add(time.Minute)
	policy := &networkv1alpha1.ServiceRoutePolicy{Spec: networkv1alpha1.ServiceRoutePolicySpec{
		Authorization: &networkv1alpha1.ServiceRouteAuthorization{
			ValidUntil: metav1.NewTime(now.Add(90 * time.Second)),
		},
	}}
	if got := networkAuthorizationDeadline(policy, now, dnsDeadline); !got.Equal(dnsDeadline) {
		t.Fatalf("authorization deadline = %v, want DNS deadline %v", got, dnsDeadline)
	}
}

func TestConsumerVPCRejectsMultipleVPCLifetimes(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cloudv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	contextObject := testNetworkContext("context", "context-uid")
	firstInterface, firstAttachment, firstVPC := consumerPath("one", contextObject.Name, "vpc-one", "vpc-one-uid", "fabric-one")
	secondInterface, secondAttachment, secondVPC := consumerPath("two", contextObject.Name, "vpc-two", "vpc-two-uid", "fabric-two")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		firstInterface, firstAttachment, firstVPC,
		secondInterface, secondAttachment, secondVPC,
	).Build()
	r := &InternalDNSCellReconciler{}
	if _, _, err := r.consumerVPC(context.Background(), cl, contextObject); err == nil {
		t.Fatal("multiple VPC lifetimes were accepted for one network context")
	}
}

func readyAttachment(
	namespace, name, uid, vpcName, vpcIdentity, node, hostInterface string,
	labels map[string]string,
) *cloudv1alpha1.VPCAttachment {
	return &cloudv1alpha1.VPCAttachment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID(uid), Generation: 1, Labels: labels},
		Spec:       cloudv1alpha1.VPCAttachmentSpec{VPC: cloudv1alpha1.VPCRef{Name: vpcName}},
		Status: cloudv1alpha1.VPCAttachmentStatus{
			ObservedGeneration: 1, VPC: vpcIdentity, VPCAttachment: name + "-fabric",
			Node: node, HostInterface: hostInterface,
			Conditions: []metav1.Condition{
				{Type: cloudv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: "Ready"},
				{Type: cloudv1alpha1.ConditionTypeProgrammed, Status: metav1.ConditionTrue, Reason: "Programmed"},
			},
		},
	}
}

func consumerPath(
	name, contextName, vpcName string,
	vpcUID types.UID,
	vpcIdentity string,
) (*networkingv1alpha.NetworkInterface, *cloudv1alpha1.VPCAttachment, *cloudv1alpha1.VPC) {
	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "interface-" + name},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			NetworkContextRef: &networkingv1alpha.LocalNetworkContextRef{Name: contextName},
			AttachmentRef: &networkingv1alpha.NetworkInterfaceAttachmentRef{
				APIGroup: "cloud.datumapis.com", Kind: "VPCAttachment", Name: "attachment-" + name,
			},
		},
	}
	attachment := &cloudv1alpha1.VPCAttachment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "attachment-" + name},
		Spec:       cloudv1alpha1.VPCAttachmentSpec{VPC: cloudv1alpha1.VPCRef{Name: vpcName}},
	}
	vpc := &cloudv1alpha1.VPC{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: vpcName, UID: vpcUID},
		Status:     cloudv1alpha1.VPCStatus{VPC: vpcIdentity},
	}
	return iface, attachment, vpc
}
