// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
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
			Protocol:     networkv1alpha1.NetworkServiceProtocolUDP,
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
			Authorization:  &networkv1alpha1.ServiceRouteAuthorization{Persistent: true},
			AttachmentSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				internalDNSConsumerVPCLabel: "vpc-uid",
			}},
			ProtocolPorts: []networkv1alpha1.ServiceRouteProtocolPort{{
				Protocol: networkv1alpha1.NetworkServiceProtocolUDP, Port: 53,
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
				APIGroup: "galactic.datumapis.com", Kind: "VPCAttachment", Name: "attachment-" + name,
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

func TestInternalDNSCellProgramsOnePathPerResolver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		networkingv1alpha.AddToScheme, cloudv1alpha1.AddToScheme, networkv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	networkContext := testNetworkContext("context", "context-uid")
	networkContext.Annotations = testDNSIntent(time.Time{})
	iface, attachment, vpc := consumerPath("one", networkContext.Name, "blue", "vpc-uid", "vpc-fabric-id")
	legacyName := boundedDNSName("context-dns-udp", "context-uid/udp")
	legacy := &networkv1alpha1.ServiceEndpoint{ObjectMeta: metav1.ObjectMeta{
		Namespace: "tenant", Name: legacyName, UID: "legacy-endpoint-uid",
	}}
	if err := controllerutil.SetControllerReference(networkContext, legacy, scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(networkContext.DeepCopy(), iface, attachment, vpc, legacy).Build()
	hub := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&networkingv1alpha.NetworkContext{}).
		WithObjects(networkContext.DeepCopy()).Build()

	primary := map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node"}
	secondary := map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node-b"}
	r := &InternalDNSCellReconciler{
		Config: config.InternalDNSConfig{Resolvers: []config.InternalDNSResolver{
			{Frontend: "fd53::53", ProducerAttachmentSelector: primary},
			{Frontend: "fd53::54", ProducerAttachmentSelector: secondary},
		}},
		HubCluster: &fakeCluster{cl: hub},
	}
	if _, err := r.reconcile(ctx, cl, networkContext); err != nil {
		t.Fatal(err)
	}

	var endpoints networkv1alpha1.ServiceEndpointList
	if err := cl.List(ctx, &endpoints, client.InNamespace("tenant")); err != nil {
		t.Fatal(err)
	}
	var policies networkv1alpha1.ServiceRoutePolicyList
	if err := cl.List(ctx, &policies, client.InNamespace("tenant")); err != nil {
		t.Fatal(err)
	}
	if len(endpoints.Items) != 4 || len(policies.Items) != 4 {
		t.Fatalf("got %d endpoints and %d policies, want 4 of each", len(endpoints.Items), len(policies.Items))
	}
	endpointsByName := map[string]networkv1alpha1.ServiceEndpoint{}
	for _, endpoint := range endpoints.Items {
		endpointsByName[endpoint.Name] = endpoint
		if endpoint.Spec.Address != "fd70:100::10" {
			t.Errorf("endpoint %s address = %s, want the shared destination", endpoint.Name, endpoint.Spec.Address)
		}
	}
	type path struct {
		frontend string
		selector map[string]string
	}
	want := map[string]path{
		legacyName: {frontend: "fd53::53", selector: primary},
		boundedDNSName("context-dns-tcp", "context-uid/tcp"):          {frontend: "fd53::53", selector: primary},
		boundedDNSName("context-dns-udp", "context-uid/udp/fd53::54"): {frontend: "fd53::54", selector: secondary},
		boundedDNSName("context-dns-tcp", "context-uid/tcp/fd53::54"): {frontend: "fd53::54", selector: secondary},
	}
	for _, policy := range policies.Items {
		expected, ok := want[policy.Name]
		if !ok {
			t.Fatalf("unexpected policy %s", policy.Name)
		}
		if policy.Spec.Frontend == nil || policy.Spec.Frontend.Address != expected.frontend {
			t.Errorf("policy %s frontend = %v, want %s", policy.Name, policy.Spec.Frontend, expected.frontend)
		}
		endpoint, ok := endpointsByName[policy.Name]
		if !ok || policy.Spec.ServiceRef.Name != endpoint.Name {
			t.Fatalf("policy %s has no endpoint of the same name", policy.Name)
		}
		if !maps.Equal(endpoint.Spec.AttachmentSelector.MatchLabels, expected.selector) {
			t.Errorf("endpoint %s selects %v, want %v", endpoint.Name, endpoint.Spec.AttachmentSelector.MatchLabels, expected.selector)
		}
	}
	if endpoint := endpointsByName[legacyName]; endpoint.UID != "legacy-endpoint-uid" {
		t.Errorf("first resolver's endpoint was replaced instead of updated in place: uid %q", endpoint.UID)
	}

	r.Config = config.InternalDNSConfig{Frontend: "fd53::53", ProducerAttachmentSelector: primary}
	if _, err := r.reconcile(ctx, cl, networkContext); err != nil {
		t.Fatal(err)
	}
	assertServicePaths(t, ctx, cl, legacyName, boundedDNSName("context-dns-tcp", "context-uid/tcp"))

	current := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(networkContext), current); err != nil {
		t.Fatal(err)
	}
	r.Config = config.InternalDNSConfig{Resolvers: []config.InternalDNSResolver{
		{Frontend: "fd53::53", ProducerAttachmentSelector: primary},
		{Frontend: "fd53::54", ProducerAttachmentSelector: secondary},
	}}
	if _, err := r.reconcile(ctx, cl, current); err != nil {
		t.Fatal(err)
	}
	current.Annotations = nil
	if _, err := r.reconcile(ctx, cl, current); err != nil {
		t.Fatal(err)
	}
	assertServicePaths(t, ctx, cl)
}

func assertServicePaths(t *testing.T, ctx context.Context, cl client.Client, names ...string) {
	t.Helper()
	var endpoints networkv1alpha1.ServiceEndpointList
	if err := cl.List(ctx, &endpoints, client.InNamespace("tenant")); err != nil {
		t.Fatal(err)
	}
	var policies networkv1alpha1.ServiceRoutePolicyList
	if err := cl.List(ctx, &policies, client.InNamespace("tenant")); err != nil {
		t.Fatal(err)
	}
	gotEndpoints := []string{}
	for _, endpoint := range endpoints.Items {
		gotEndpoints = append(gotEndpoints, endpoint.Name)
	}
	gotPolicies := []string{}
	for _, policy := range policies.Items {
		gotPolicies = append(gotPolicies, policy.Name)
	}
	slices.Sort(names)
	slices.Sort(gotEndpoints)
	slices.Sort(gotPolicies)
	if !slices.Equal(gotEndpoints, names) || !slices.Equal(gotPolicies, names) {
		t.Fatalf("endpoints %v and policies %v, want %v", gotEndpoints, gotPolicies, names)
	}
}
