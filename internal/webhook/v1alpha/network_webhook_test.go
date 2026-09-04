// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func networkWithFamilies(families ...networkingv1alpha.IPFamily) *networkingv1alpha.Network {
	return &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "net", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: families,
			MTU:        1460,
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
			name:     "dual-stack",
			families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol},
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

	tests := []struct {
		name      string
		old       []networkingv1alpha.IPFamily
		updated   []networkingv1alpha.IPFamily
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

			_, err := validator.ValidateUpdate(ctx, networkWithFamilies(test.old...), updated)
			if test.wantError && err == nil {
				t.Fatal("expected the update to be refused")
			}
			if !test.wantError && err != nil {
				t.Fatalf("expected the update to be admitted, got %v", err)
			}
		})
	}
}
