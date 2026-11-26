// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func startSchemaEnv(t *testing.T, crdName string) client.Client {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest assets are required")
	}

	data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "networking.datumapis.com_"+crdName+".yaml"))
	require.NoError(t, err)
	var crd apiextensionsv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))

	env := &envtest.Environment{CRDs: []*apiextensionsv1.CustomResourceDefinition{&crd}}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	return k8sClient
}

func physicalClaimSpec() networkingv1alpha.NetworkInterfaceClaimSpec {
	return networkingv1alpha.NetworkInterfaceClaimSpec{
		Network:        networkingv1alpha.LocalNetworkRef{Name: "app"},
		AttachmentMode: networkingv1alpha.NetworkInterfaceAttachmentModePhysical,
		AttachmentPoint: &networkingv1alpha.NetworkInterfaceAttachmentPoint{
			MACAddress: "00:00:5e:00:53:01",
			Switch:     "rack-a1-tor-1",
			Port:       "Ethernet1/12",
		},
	}
}

func TestPhysicalClaimAdmission(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaceclaims")
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*networkingv1alpha.NetworkInterfaceClaimSpec)
		valid  bool
	}{
		{name: "physical with an attachment point", valid: true},
		{name: "boot purpose and private isolation", valid: true, mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
		}},
		{name: "shared isolation", valid: true, mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationShared
		}},
		{name: "upper-case MAC address", valid: true, mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.MACAddress = "00:00:5E:00:53:0A"
		}},
		{name: "physical without an attachment point", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint = nil
		}},
		{name: "attachment point on another mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeHypervisor
		}},
		{name: "attachment point on the default mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentMode = ""
		}},
		{name: "purpose on another mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeNetns
			spec.AttachmentPoint = nil
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		}},
		{name: "isolation on another mode", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentMode = networkingv1alpha.NetworkInterfaceAttachmentModeNetns
			spec.AttachmentPoint = nil
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
		}},
		{name: "unknown purpose", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Purpose = "Install"
		}},
		{name: "unknown isolation", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = "Isolated"
		}},
		{name: "MAC address with five pairs", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.MACAddress = "00:00:5e:00:53"
		}},
		{name: "MAC address with dashes", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.MACAddress = "00-00-5e-00-53-01"
		}},
		{name: "empty switch", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.Switch = ""
		}},
		{name: "empty port", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.Port = ""
		}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &networkingv1alpha.NetworkInterfaceClaim{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("create-%d", i), Namespace: "default"},
				Spec:       physicalClaimSpec(),
			}
			if tt.mutate != nil {
				tt.mutate(&claim.Spec)
			}
			err := k8sClient.Create(ctx, claim)
			if tt.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestPhysicalClaimFieldsAreImmutable(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaceclaims")
	ctx := context.Background()

	tests := []struct {
		name   string
		start  func(*networkingv1alpha.NetworkInterfaceClaimSpec)
		mutate func(*networkingv1alpha.NetworkInterfaceClaimSpec)
	}{
		{name: "change the port", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.Port = "Ethernet1/13"
		}},
		{name: "change the MAC address", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.AttachmentPoint.MACAddress = "00:00:5e:00:53:02"
		}},
		{name: "set the purpose", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		}},
		{name: "clear the purpose", start: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		}, mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Purpose = ""
		}},
		{name: "set the isolation", mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationShared
		}},
		{name: "change the isolation", start: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
		}, mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationShared
		}},
		{name: "clear the isolation", start: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = networkingv1alpha.NetworkInterfaceIsolationPrivate
		}, mutate: func(spec *networkingv1alpha.NetworkInterfaceClaimSpec) {
			spec.Isolation = ""
		}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &networkingv1alpha.NetworkInterfaceClaim{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("update-%d", i), Namespace: "default"},
				Spec:       physicalClaimSpec(),
			}
			if tt.start != nil {
				tt.start(&claim.Spec)
			}
			require.NoError(t, k8sClient.Create(ctx, claim))

			tt.mutate(&claim.Spec)
			require.Error(t, k8sClient.Update(ctx, claim))
		})
	}
}

// A provider compares the claim it reads back with the claim it wrote, so a
// default written by the server would read as a conflict.
func TestPhysicalClaimFieldsHaveNoServerDefaults(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaceclaims")
	ctx := context.Background()

	for name, spec := range map[string]networkingv1alpha.NetworkInterfaceClaimSpec{
		"physical": physicalClaimSpec(),
		"netns":    {Network: networkingv1alpha.LocalNetworkRef{Name: "app"}},
	} {
		t.Run(name, func(t *testing.T) {
			claim := &networkingv1alpha.NetworkInterfaceClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "defaults-" + name, Namespace: "default"},
				Spec:       spec,
			}
			require.NoError(t, k8sClient.Create(ctx, claim))

			var stored networkingv1alpha.NetworkInterfaceClaim
			require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), &stored))
			require.Empty(t, stored.Spec.Purpose)
			require.Empty(t, stored.Spec.Isolation)
			require.Equal(t, spec.AttachmentPoint, stored.Spec.AttachmentPoint)
		})
	}
}

// Claims stored before the physical fields existed carry none of them, and
// must still accept the updates the operator and compute make.
func TestExistingClaimsStillUpdate(t *testing.T) {
	k8sClient := startSchemaEnv(t, "networkinterfaceclaims")
	ctx := context.Background()

	claim := &networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "default"},
		Spec:       networkingv1alpha.NetworkInterfaceClaimSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "app"}},
	}
	require.NoError(t, k8sClient.Create(ctx, claim))

	claim.Labels = map[string]string{"example.com/touched": "true"}
	claim.Spec.Egress = &networkingv1alpha.NetworkInterfaceClaimEgress{
		Internet: &networkingv1alpha.NetworkInterfaceClaimInternetEgress{Mode: networkingv1alpha.NetworkInterfaceClaimInternetEgressInherit},
	}
	require.NoError(t, k8sClient.Update(ctx, claim))
}
