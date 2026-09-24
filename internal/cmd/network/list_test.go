// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/network/util"
)

func scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = networkingv1alpha.AddToScheme(s)
	return s
}

func TestListEmpty(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(scheme()).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "my-project", listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "No VPCs found") {
		t.Errorf("expected empty message, got %q", errOut.String())
	}
}

func TestListOneReady(t *testing.T) {
	net := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "default"},
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

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(net).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "my-project", listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + data row, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "NAME") {
		t.Errorf("expected header, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "prod") {
		t.Errorf("expected prod in row, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "fd20:1::/48") {
		t.Errorf("expected prefix in row, got %q", lines[1])
	}
}

func TestListNotReady(t *testing.T) {
	net := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			MTU:  1440,
		},
		Status: networkingv1alpha.NetworkStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionFalse, Reason: "IPv6Required",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(net).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "p", listOpts{format: util.OutputWide})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "IPv6Required") {
		t.Errorf("expected reason in wide output, got %q", out.String())
	}
}

func TestListWide(t *testing.T) {
	net := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			MTU:  8856,
		},
		Status: networkingv1alpha.NetworkStatus{
			IPAM: &networkingv1alpha.NetworkIPAMStatus{IPv6Prefix: "fd20:1::/48"},
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(net).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "p", listOpts{format: util.OutputWide})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "MTU") {
		t.Errorf("expected MTU header in wide, got %q", out.String())
	}
	if !strings.Contains(out.String(), "8856") {
		t.Errorf("expected MTU value in wide, got %q", out.String())
	}
}

func TestListJSON(t *testing.T) {
	net := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "default"},
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

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(net).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "p", listOpts{format: util.OutputJSON})
	if err != nil {
		t.Fatal(err)
	}

	var views []networkView
	if err := json.Unmarshal(out.Bytes(), &views); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(views) != 1 || views[0].Name != "prod" {
		t.Errorf("expected one prod view, got %+v", views)
	}
}

func TestListCounts(t *testing.T) {
	net1 := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "default"},
		Spec:       networkingv1alpha.NetworkSpec{IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto}, MTU: 1440},
		Status: networkingv1alpha.NetworkStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}
	net2 := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "default"},
		Spec:       networkingv1alpha.NetworkSpec{IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto}, MTU: 1440},
		Status: networkingv1alpha.NetworkStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}
	ctx1 := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alpha-us-east", Namespace: "default",
			Labels: map[string]string{networkingv1alpha.NetworkLabel: "alpha"},
		},
	}
	ctx2 := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alpha-us-west", Namespace: "default",
			Labels: map[string]string{networkingv1alpha.NetworkLabel: "alpha"},
		},
	}
	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha-eth0", Namespace: "default"},
		Spec:       networkingv1alpha.NetworkInterfaceSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "alpha"}},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(net1, net2, ctx1, ctx2, iface).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "p", listOpts{format: util.OutputJSON})
	if err != nil {
		t.Fatal(err)
	}

	var views []networkView
	if err := json.Unmarshal(out.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
	for _, v := range views {
		switch v.Name {
		case "alpha":
			if v.Locations != 2 {
				t.Errorf("alpha: expected 2 locations, got %d", v.Locations)
			}
			if v.Interfaces != 1 {
				t.Errorf("alpha: expected 1 interface, got %d", v.Interfaces)
			}
		case "beta":
			if v.Locations != 0 {
				t.Errorf("beta: expected 0 locations, got %d", v.Locations)
			}
			if v.Interfaces != 0 {
				t.Errorf("beta: expected 0 interfaces, got %d", v.Interfaces)
			}
		}
	}
}

func TestListNoHeaders(t *testing.T) {
	net := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "default"},
		Spec:       networkingv1alpha.NetworkSpec{IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto}, MTU: 1440},
		Status: networkingv1alpha.NetworkStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(net).Build()

	var out, errOut bytes.Buffer
	err := listNetworks(context.Background(), &out, &errOut, c, "p", listOpts{noHeaders: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "NAME") {
		t.Errorf("expected no headers, got %q", out.String())
	}
}
