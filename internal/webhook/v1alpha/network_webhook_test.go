// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func networkWithFamilies(families ...networkingv1alpha.IPFamily) *networkingv1alpha.Network {
	return &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "net", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: families,
			MTU:        1440,
		},
	}
}

func TestNetworkValidateCreate(t *testing.T) {
	validator := &NetworkCustomValidator{}
	ctx := context.Background()

	tests := []struct {
		name      string
		families  []networkingv1alpha.IPFamily
		wantError bool
	}{
		{name: "IPv6", families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}},
		{
			name:      "dual-stack",
			families:  []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol},
			wantError: true,
		},
		{
			name:      "IPv4 only",
			families:  []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validator.ValidateCreate(ctx, networkWithFamilies(test.families...))
			if test.wantError && err == nil {
				t.Fatal("expected the network to be refused")
			}
			if !test.wantError && err != nil {
				t.Fatalf("expected the network to be admitted, got %v", err)
			}
		})
	}
}

func TestNetworkValidateUpdate(t *testing.T) {
	validator := &NetworkCustomValidator{}
	ctx := context.Background()

	v4 := []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	v6 := []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	dual := []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol}
	withFinalizer := func(n *networkingv1alpha.Network) *networkingv1alpha.Network {
		n.Finalizers = []string{"networking.datumapis.com/network-controller"}
		return n
	}

	tests := []struct {
		name      string
		old       []networkingv1alpha.IPFamily
		updated   []networkingv1alpha.IPFamily
		oldObj    func() *networkingv1alpha.Network
		mutate    func(updated *networkingv1alpha.Network)
		wantError bool
	}{
		{
			name:    "a finalizer lands on a network that carries no IPv6",
			old:     v4,
			updated: v4,
			mutate: func(updated *networkingv1alpha.Network) {
				updated.Finalizers = []string{"networking.datumapis.com/network-controller"}
			},
		},
		{
			name:    "an unrelated spec field is patched on a network that carries no IPv6",
			old:     v4,
			updated: v4,
			mutate:  func(updated *networkingv1alpha.Network) { updated.Spec.MTU = 1500 },
		},
		{
			name:    "a network that carries no IPv6 is repaired",
			old:     v4,
			updated: v6,
		},
		{
			name:    "a network being deleted is left alone",
			old:     v6,
			updated: v4,
			mutate: func(updated *networkingv1alpha.Network) {
				updated.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			},
		},
		{
			name:    "a finalizer is removed from a dual-stack network being deleted",
			oldObj:  func() *networkingv1alpha.Network { return withFinalizer(networkWithFamilies(dual...)) },
			old:     dual,
			updated: dual,
			mutate: func(updated *networkingv1alpha.Network) {
				updated.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			},
		},
		{
			name:    "a finalizer is removed from an IPv4 network that is not being deleted",
			oldObj:  func() *networkingv1alpha.Network { return withFinalizer(networkWithFamilies(v4...)) },
			old:     v4,
			updated: v4,
		},
		{
			name:      "a working network adds IPv4",
			old:       v6,
			updated:   dual,
			wantError: true,
		},
		{
			name:      "a working network is narrowed to IPv4",
			old:       v6,
			updated:   v4,
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated := networkWithFamilies(test.updated...)
			if test.mutate != nil {
				test.mutate(updated)
			}

			old := networkWithFamilies(test.old...)
			if test.oldObj != nil {
				old = test.oldObj()
			}

			_, err := validator.ValidateUpdate(ctx, old, updated)
			if test.wantError && err == nil {
				t.Fatal("expected the update to be refused")
			}
			if !test.wantError && err != nil {
				t.Fatalf("expected the update to be admitted, got %v", err)
			}
		})
	}
}

func networkWithMode(mode networkingv1alpha.NetworkInternetEgressMode) *networkingv1alpha.Network {
	network := networkWithFamilies(networkingv1alpha.IPv6Protocol)
	if mode != "" {
		network.Spec.Egress = &networkingv1alpha.NetworkEgress{
			Internet: &networkingv1alpha.NetworkInternetEgress{Mode: mode},
		}
	}
	return network
}

func admissionContext(t *testing.T, operation admissionv1.Operation, old *networkingv1alpha.Network) context.Context {
	t.Helper()
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: operation}}
	if old != nil {
		raw, err := json.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		req.OldObject = runtime.RawExtension{Raw: raw}
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

func TestNetworkDefaultInternetEgressMode(t *testing.T) {
	enabled := networkingv1alpha.NetworkInternetEgressEnabled
	disabled := networkingv1alpha.NetworkInternetEgressDisabled

	tests := []struct {
		name        string
		platform    networkingv1alpha.NetworkInternetEgressMode
		operation   admissionv1.Operation
		old         *networkingv1alpha.Network
		declared    networkingv1alpha.NetworkInternetEgressMode
		want        networkingv1alpha.NetworkInternetEgressMode
		wantNoBlock bool
	}{
		{name: "create stamps the platform default", platform: disabled, operation: admissionv1.Create, want: disabled},
		{name: "create keeps a declared mode", platform: disabled, operation: admissionv1.Create, declared: enabled, want: enabled},
		{
			name: "update dropping the mode keeps the stored one", platform: disabled, operation: admissionv1.Update,
			old: networkWithMode(enabled), want: enabled,
		},
		{
			name: "update of an unstamped network stamps the platform default", platform: enabled,
			operation: admissionv1.Update, old: networkWithMode(""), want: enabled,
		},
		{
			name: "update may change the mode", platform: enabled, operation: admissionv1.Update,
			old: networkWithMode(enabled), declared: disabled, want: disabled,
		},
		{name: "no platform default stamps nothing", operation: admissionv1.Create, wantNoBlock: true},
	}

	t.Run("missing admission request is refused", func(t *testing.T) {
		defaulter := &NetworkCustomDefaulter{DefaultInternetMode: enabled}
		if err := defaulter.Default(context.Background(), networkWithMode("")); err == nil {
			t.Fatal("expected an error without an admission request")
		}
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defaulter := &NetworkCustomDefaulter{DefaultInternetMode: test.platform}
			network := networkWithMode(test.declared)
			if err := defaulter.Default(admissionContext(t, test.operation, test.old), network); err != nil {
				t.Fatal(err)
			}
			if test.wantNoBlock {
				if network.Spec.Egress != nil {
					t.Fatalf("expected no egress block, got %#v", network.Spec.Egress)
				}
				return
			}
			if got := internetEgressMode(network); got != test.want {
				t.Fatalf("mode = %q, want %q", got, test.want)
			}
		})
	}
}
