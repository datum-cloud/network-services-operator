// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/network/util"
)

func readyNetwork(name string) *networkingv1alpha.Network {
	return &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			MTU:  1440,
		},
		Status: networkingv1alpha.NetworkStatus{
			IPAM: &networkingv1alpha.NetworkIPAMStatus{IPv6Prefix: "fd20:1::/48"},
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

func TestVPCListNotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(scheme()).Build()

	var out bytes.Buffer
	err := vpcList(context.Background(), &out, c, "proj", "missing", util.OutputTable, false)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestVPCListFiltersInterfaces(t *testing.T) {
	net := readyNetwork("prod")
	otherNet := readyNetwork("staging")

	prodIF := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: "prod-eth0", Namespace: "default",
			Labels: map[string]string{
				workloadNameLabel: "web",
				networkingv1alpha.NetworkInterfaceLocationLabel: "us-east",
			},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:       networkingv1alpha.LocalNetworkRef{Name: "prod"},
			InterfaceName: "eth0",
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{Phase: networkingv1alpha.NetworkInterfacePhaseBound},
	}
	stagingIF := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: "staging-eth0", Namespace: "default"},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:       networkingv1alpha.LocalNetworkRef{Name: "staging"},
			InterfaceName: "eth0",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(net, otherNet, prodIF, stagingIF).Build()

	var out bytes.Buffer
	err := vpcList(context.Background(), &out, c, "proj", "prod", util.OutputTable, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "prod-eth0") {
		t.Error("expected prod interface in output")
	}
	if strings.Contains(out.String(), "staging-eth0") {
		t.Error("staging interface should not appear")
	}
}

func TestVPCListSubnetsByLabel(t *testing.T) {
	net := readyNetwork("prod")

	startAddr := "fd20:1:0:1::"
	prefixLen := int32(64)
	prodSubnet := &networkingv1alpha.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "prod-us-east", Namespace: "default",
			Labels: map[string]string{networkingv1alpha.NetworkLabel: "prod"},
		},
		Spec: networkingv1alpha.SubnetSpec{
			Location: locationsv1alpha1.LocationReference{Name: "us-east"},
		},
		Status: networkingv1alpha.SubnetStatus{
			StartAddress: &startAddr,
			PrefixLength: &prefixLen,
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	otherSubnet := &networkingv1alpha.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "staging-us-east", Namespace: "default",
			Labels: map[string]string{networkingv1alpha.NetworkLabel: "staging"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(net, prodSubnet, otherSubnet).Build()

	var out bytes.Buffer
	err := vpcList(context.Background(), &out, c, "proj", "prod", util.OutputTable, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "prod-us-east") {
		t.Error("expected prod subnet in output")
	}
	if strings.Contains(out.String(), "staging-us-east") {
		t.Error("staging subnet should not appear")
	}
}

func TestVPCListServiceMatching(t *testing.T) {
	net := readyNetwork("prod")

	prodIF := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: "prod-eth0", Namespace: "default",
			Labels: map[string]string{
				workloadNameLabel: "web",
			},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "prod"},
		},
	}

	matchingSvc := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Name: "web-svc", Namespace: "default"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{workloadNameLabel: "web"},
				},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080, Protocol: "TCP"}},
		},
	}

	nonMatchingSvc := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Name: "other-svc", Namespace: "default"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{workloadNameLabel: "api"},
				},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "grpc", Port: 9090, Protocol: "TCP"}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(net, prodIF, matchingSvc, nonMatchingSvc).Build()

	var out bytes.Buffer
	err := vpcList(context.Background(), &out, c, "proj", "prod", util.OutputTable, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "web-svc") {
		t.Error("expected matching service in output")
	}
	if strings.Contains(out.String(), "other-svc") {
		t.Error("non-matching service should not appear")
	}
}

func TestVPCListJSON(t *testing.T) {
	net := readyNetwork("prod")

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(net).Build()

	var out bytes.Buffer
	err := vpcList(context.Background(), &out, c, "proj", "prod", util.OutputJSON, false)
	if err != nil {
		t.Fatal(err)
	}

	var detail vpcDetail
	if err := json.Unmarshal(out.Bytes(), &detail); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if detail.Network.Name != "prod" {
		t.Errorf("expected prod network, got %q", detail.Network.Name)
	}
}
