// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"maps"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"
	"go.datum.net/galactic/pkg/serviceplan"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

var (
	spreadTiers = &metav1.LabelSelector{
		MatchLabels: map[string]string{"service.datum.net/internal-dns": "true"},
		MatchExpressions: []metav1.LabelSelectorRequirement{{
			Key: "internal-dns.datum.net/tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"node", "node-b"},
		}},
	}
	spreadResolver = config.InternalDNSResolver{
		Frontend: "fd53::53", ProducerSelector: spreadTiers,
		Distribution: config.InternalDNSDistributionSpread,
		HealthCheck:  &config.InternalDNSHealthCheck{Port: 8053},
	}
)

func TestInternalDNSEndpointSpec(t *testing.T) {
	t.Parallel()
	primary := map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node"}

	for _, distribution := range []config.InternalDNSDistribution{"", config.InternalDNSDistributionSingle} {
		single := internalDNSEndpointSpec(config.InternalDNSResolver{
			Frontend: "fd53::53", ProducerAttachmentSelector: primary, Distribution: distribution,
		}, networkv1alpha1.NetworkServiceProtocolUDP, "fd70:100::10", "us-central")
		want := networkv1alpha1.ServiceEndpointSpec{
			ServiceClass: "internal-dns", Address: "fd70:100::10", Port: 53,
			Protocol: networkv1alpha1.NetworkServiceProtocolUDP, Region: "us-central",
			DeliveryMode:       networkv1alpha1.ServiceEndpointDeliveryModePreferNodeLocal,
			AttachmentSelector: &metav1.LabelSelector{MatchLabels: primary},
		}
		if single.Distribution != "" || single.HealthCheck != nil {
			t.Errorf("distribution %q: Single endpoint gained distribution %q and health check %#v",
				distribution, single.Distribution, single.HealthCheck)
		}
		if single.ServiceClass != want.ServiceClass || single.Address != want.Address || single.Port != want.Port ||
			single.Protocol != want.Protocol || single.Region != want.Region || single.DeliveryMode != want.DeliveryMode ||
			!maps.Equal(single.AttachmentSelector.MatchLabels, primary) || len(single.AttachmentSelector.MatchExpressions) != 0 {
			t.Errorf("distribution %q: Single endpoint = %#v, want %#v", distribution, single, want)
		}
	}

	for _, protocol := range internalDNSServiceProtocols {
		spec := internalDNSEndpointSpec(spreadResolver, protocol, "fd70:100::10", "us-central")
		if spec.Distribution != networkv1alpha1.ServiceEndpointDistributionSpread {
			t.Errorf("%s: distribution = %q, want Spread", protocol, spec.Distribution)
		}
		wantCheck := networkv1alpha1.ServiceEndpointHealthCheck{
			Port: 8053, IntervalSeconds: 5, TimeoutSeconds: 2, UnhealthyThreshold: 3, HealthyThreshold: 3,
		}
		if spec.HealthCheck == nil || *spec.HealthCheck != wantCheck {
			t.Errorf("%s: health check = %#v, want %#v", protocol, spec.HealthCheck, wantCheck)
		}
		if spec.Protocol != protocol || spec.Port != 53 {
			t.Errorf("%s: endpoint serves %s/%d", protocol, spec.Protocol, spec.Port)
		}
		if len(spec.AttachmentSelector.MatchExpressions) != 1 ||
			spec.AttachmentSelector.MatchExpressions[0].Operator != metav1.LabelSelectorOpIn ||
			len(spec.AttachmentSelector.MatchExpressions[0].Values) != 2 {
			t.Errorf("%s: selector = %#v, want the resolver's full selector", protocol, spec.AttachmentSelector)
		}
		if spec.AttachmentSelector == spreadResolver.ProducerSelector {
			t.Errorf("%s: endpoint shares the configured selector instead of a copy", protocol)
		}
	}
}

// spreadPath builds a policy and an endpoint for a consumer on node-a served
// by two producers on that same node.
func spreadPath(distribution networkv1alpha1.ServiceEndpointDistribution) (
	*networkv1alpha1.ServiceRoutePolicy, *networkv1alpha1.ServiceEndpoint, []*cloudv1alpha1.VPCAttachment,
) {
	consumer := readyAttachment("tenant", "consumer", "consumer-uid", "blue", "vpc-fabric-id", "node-a", "consumer0",
		map[string]string{internalDNSConsumerVPCLabel: "vpc-uid"})
	first := readyAttachment("dns", "resolver-a", "resolver-a-uid", "dns-service", "service-vpc-id", "node-a", "resolver0",
		map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node"})
	second := readyAttachment("dns", "resolver-b", "resolver-b-uid", "dns-service", "service-vpc-id", "node-a", "resolver1",
		map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node-b"})
	resolver := spreadResolver
	if distribution != networkv1alpha1.ServiceEndpointDistributionSpread {
		resolver = config.InternalDNSResolver{Frontend: "fd53::53", ProducerSelector: spreadTiers}
	}
	endpoint := &networkv1alpha1.ServiceEndpoint{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "dns", UID: "endpoint-uid", Generation: 1},
		Spec: internalDNSEndpointSpec(resolver, networkv1alpha1.NetworkServiceProtocolUDP,
			"fd70:100::10", "us-central"),
	}
	policy := &networkv1alpha1.ServiceRoutePolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "dns", UID: "policy-uid", Generation: 1},
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
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type: networkv1alpha1.ConditionTypeServiceRouteAccepted, Status: metav1.ConditionTrue, Reason: "Accepted",
			}},
		},
	}
	return policy, endpoint, []*cloudv1alpha1.VPCAttachment{consumer, first, second}
}

func TestPolicyProgrammedWithTwoProducersOnANode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	deadline := metav1.NewTime(now.Add(time.Minute))

	policy, endpoint, attachments := spreadPath(networkv1alpha1.ServiceEndpointDistributionSpread)
	unhealthy := networkv1alpha1.ServiceEndpointProducerReport{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "dns-node-a"},
		Spec: networkv1alpha1.ServiceEndpointProducerReportSpec{
			EndpointName: endpoint.Name, EndpointUID: endpoint.UID, Node: "node-a",
			Producers: []networkv1alpha1.ServiceEndpointProducerStatus{{
				Namespace: "dns", Name: "resolver-a", AttachmentUID: "resolver-a-uid", Node: "node-a", Port: 8053,
				State: networkv1alpha1.BackendHealthUnhealthy, Reason: "ProbeFailed",
				LastTransitionTime: metav1.NewTime(now.Add(-time.Minute)),
				RenewTime:          &metav1.Time{Time: now.Add(-50 * time.Second)},
			}},
		},
	}
	view := serviceplan.WithProducerReports(endpoint, []networkv1alpha1.ServiceEndpointProducerReport{unhealthy})
	if len(view.Status.Producers) != 1 {
		t.Fatalf("producer report was not merged into the endpoint view: %#v", view.Status.Producers)
	}
	if expiry, ok := serviceplan.NextProducerReportExpiry(view, now); !ok || expiry != 10*time.Second {
		t.Fatalf("next report expiry = %v/%v, want 10s", expiry, ok)
	}

	plan, err := serviceplan.ProgrammingPlanAt(policy, view, attachments, "vpc-fabric-id", now)
	if err != nil {
		t.Fatalf("Spread endpoint with two producers on one node was refused: %v", err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0] != "node-a" {
		t.Fatalf("plan nodes = %v, want [node-a]", plan.Nodes)
	}
	if _, ready := policyProgrammed(policy, view, attachments, "vpc-fabric-id", now); ready {
		t.Fatal("a path no node acknowledged was reported ready")
	}
	if condition := unreadyPathCondition(policy, view, attachments, "vpc-fabric-id", now); condition.Reason !=
		networkingv1alpha.NetworkContextDNSReadyReasonNetworkPathNotReady {
		t.Fatalf("unacknowledged Spread path reported %s, want NetworkPathNotReady", condition.Reason)
	}
	policy.Status.Nodes = []networkv1alpha1.ServiceRouteNodeStatus{{
		NodeName: "node-a", PolicyUID: string(policy.UID), ObservedGeneration: policy.Generation,
		InputDigest: plan.InputDigest, Ready: true, ValidUntil: deadline,
	}}
	if until, ready := policyProgrammed(policy, view, attachments, "vpc-fabric-id", now); !ready || !until.Equal(deadline.Time) {
		t.Fatalf("acknowledged Spread path not ready: ready=%v until=%v", ready, until)
	}

	single, singleEndpoint, _ := spreadPath("")
	single.Status.Nodes = policy.Status.Nodes
	if _, ready := policyProgrammed(single, singleEndpoint, attachments, "vpc-fabric-id", now); ready {
		t.Fatal("a Single endpoint selecting two producers on one node was reported ready")
	}
	if _, err := serviceplan.ProgrammingPlanAt(single, singleEndpoint, attachments, "vpc-fabric-id", now); err == nil {
		t.Fatal("the planner accepted a Single endpoint with two producers on one node")
	}
}

func TestInternalDNSCellProgramsSpreadResolver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
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

	// The UDP endpoint exists from an earlier Single configuration, so its
	// producer report has an endpoint UID to describe.
	udpName := boundedDNSName("context-dns-udp", "context-uid/udp")
	existing := &networkv1alpha1.ServiceEndpoint{ObjectMeta: metav1.ObjectMeta{
		Namespace: "tenant", Name: udpName, UID: "udp-endpoint-uid",
	}}
	if err := controllerutil.SetControllerReference(networkContext, existing, scheme); err != nil {
		t.Fatal(err)
	}
	report := &networkv1alpha1.ServiceEndpointProducerReport{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "udp-node-a"},
		Spec: networkv1alpha1.ServiceEndpointProducerReportSpec{
			EndpointName: udpName, EndpointUID: existing.UID, Node: "node-a",
			Producers: []networkv1alpha1.ServiceEndpointProducerStatus{{
				Namespace: "dns", Name: "resolver-a", AttachmentUID: "resolver-a-uid", Node: "node-a", Port: 8053,
				State: networkv1alpha1.BackendHealthHealthy, Reason: "ProbeSucceeded",
				LastTransitionTime: metav1.NewTime(now.Add(-time.Minute)),
				RenewTime:          &metav1.Time{Time: now.Add(-55 * time.Second)},
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(networkContext.DeepCopy(), iface, attachment, vpc, existing, report).Build()
	hub := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&networkingv1alpha.NetworkContext{}).
		WithObjects(networkContext.DeepCopy()).Build()
	r := &InternalDNSCellReconciler{
		Location:    config.LocationConfig{Name: "ord-1"},
		localReader: noServingLocations{cl},
		Config:      config.InternalDNSConfig{Resolvers: []config.InternalDNSResolver{spreadResolver}},
		HubCluster:  &fakeCluster{cl: hub},
		Now:         func() time.Time { return now },
	}
	result, err := r.reconcile(ctx, cl, networkContext)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != 5*time.Second {
		t.Errorf("requeue after %v, want 5s, when the producer report expires", result.RequeueAfter)
	}

	var endpoints networkv1alpha1.ServiceEndpointList
	if err := cl.List(ctx, &endpoints, client.InNamespace("tenant")); err != nil {
		t.Fatal(err)
	}
	if len(endpoints.Items) != 2 {
		t.Fatalf("got %d endpoints, want one per protocol", len(endpoints.Items))
	}
	for _, endpoint := range endpoints.Items {
		if endpoint.Spec.Distribution != networkv1alpha1.ServiceEndpointDistributionSpread ||
			endpoint.Spec.HealthCheck == nil || endpoint.Spec.HealthCheck.Port != 8053 {
			t.Errorf("endpoint %s = %s/%#v, want Spread with the configured health check",
				endpoint.Name, endpoint.Spec.Distribution, endpoint.Spec.HealthCheck)
		}
		if endpoint.Name == udpName && endpoint.UID != existing.UID {
			t.Errorf("switching to Spread replaced the endpoint instead of updating it")
		}
	}
}

func TestProducerReportsDiffer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	base := &networkv1alpha1.ServiceEndpointProducerReport{
		Spec: networkv1alpha1.ServiceEndpointProducerReportSpec{
			EndpointName: "dns", EndpointUID: "endpoint-uid", Node: "node-a",
			Producers: []networkv1alpha1.ServiceEndpointProducerStatus{{
				Namespace: "dns", Name: "resolver-a", AttachmentUID: "resolver-a-uid", Node: "node-a", Port: 8053,
				State: networkv1alpha1.BackendHealthHealthy, Reason: "ProbeSucceeded",
				RenewTime: &metav1.Time{Time: now.Add(-20 * time.Second)},
			}},
		},
	}
	renewed := base.DeepCopy()
	renewed.Spec.Producers[0].RenewTime = &metav1.Time{Time: now}
	if producerReportsDiffer(base, renewed, now) {
		t.Error("a renewal of a current entry passed the filter")
	}
	unhealthy := renewed.DeepCopy()
	unhealthy.Spec.Producers[0].State = networkv1alpha1.BackendHealthUnhealthy
	if !producerReportsDiffer(renewed, unhealthy, now) {
		t.Error("a health change was filtered out")
	}
	expired := base.DeepCopy()
	expired.Spec.Producers[0].RenewTime = &metav1.Time{Time: now.Add(-2 * time.Minute)}
	if !producerReportsDiffer(expired, renewed, now) {
		t.Error("an expired entry becoming current was filtered out")
	}
	added := renewed.DeepCopy()
	added.Spec.Producers = append(added.Spec.Producers, networkv1alpha1.ServiceEndpointProducerStatus{
		Namespace: "dns", Name: "resolver-b", AttachmentUID: "resolver-b-uid", Node: "node-a", Port: 8053,
		State: networkv1alpha1.BackendHealthUnknown, Reason: "Pending",
	})
	if !producerReportsDiffer(renewed, added, now) {
		t.Error("a new producer entry was filtered out")
	}
}
