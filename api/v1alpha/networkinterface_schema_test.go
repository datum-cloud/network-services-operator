// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func physicalInterfaceSpec() networkingv1alpha.NetworkInterfaceSpec {
	return networkingv1alpha.NetworkInterfaceSpec{
		Network:        networkingv1alpha.LocalNetworkRef{Name: "app"},
		AttachmentMode: networkingv1alpha.NetworkInterfaceAttachmentModePhysical,
		AttachmentPoint: &networkingv1alpha.NetworkInterfaceAttachmentPoint{
			MACAddress: "00:00:5e:00:53:01",
			Switch:     "rack-a1-tor-1",
			Port:       "Ethernet1/12",
		},
	}
}

func TestPhysicalInterfaceAdmission(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaces")
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*networkingv1alpha.NetworkInterfaceSpec)
		valid  bool
	}{
		{name: "physical with an attachment point", valid: true},
		{name: "boot purpose and private isolation", valid: true, mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
		}},
		{name: "physical without an attachment point", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.AttachmentPoint = nil
		}},
		{name: "attachment point on the default mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.AttachmentMode = ""
		}},
		{name: "purpose on another mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeNetns
			spec.AttachmentPoint = nil
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		}},
		{name: "isolation on another mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeHypervisor
			spec.AttachmentPoint = nil
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationShared
		}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface := &networkingv1alpha.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("create-%d", i), Namespace: "default"},
				Spec:       physicalInterfaceSpec(),
			}
			if tt.mutate != nil {
				tt.mutate(&iface.Spec)
			}
			err := k8sClient.Create(ctx, iface)
			if tt.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestPhysicalInterfaceFieldsAreImmutable(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaces")
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*networkingv1alpha.NetworkInterfaceSpec)
	}{
		{name: "change the port", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.AttachmentPoint.Port = "Ethernet1/13"
		}},
		{name: "set the purpose", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		}},
		{name: "set the isolation", mutate: func(spec *networkingv1alpha.NetworkInterfaceSpec) {
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
		}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface := &networkingv1alpha.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("update-%d", i), Namespace: "default"},
				Spec:       physicalInterfaceSpec(),
			}
			require.NoError(t, k8sClient.Create(ctx, iface))

			tt.mutate(&iface.Spec)
			require.Error(t, k8sClient.Update(ctx, iface))
		})
	}
}

// The operator changes an existing interface's MTU, claim and mode, so the new
// rules must leave interfaces that carry none of the physical fields alone.
func TestExistingInterfacesStillUpdate(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaces")
	ctx := context.Background()

	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "default"},
		Spec:       networkingv1alpha.NetworkInterfaceSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "app"}},
	}
	require.NoError(t, k8sClient.Create(ctx, iface))

	iface.Spec.MTU = 1400
	iface.Spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeHypervisor
	iface.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: "web-0-eth0"}
	require.NoError(t, k8sClient.Update(ctx, iface))
}
