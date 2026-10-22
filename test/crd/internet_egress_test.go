// SPDX-License-Identifier: AGPL-3.0-only

package crd

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"
)

func egressNetwork(name string, internet *networkingv1alpha.NetworkInternetEgress) *networkingv1alpha.Network {
	network := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		},
	}
	if internet != nil {
		network.Spec.Egress = &networkingv1alpha.NetworkEgress{Internet: internet}
	}
	return network
}

// TestNetworkInternetEgressModeHasNoSchemaDefault asserts the schema leaves the
// mode unset. The platform default is an operator setting stamped by the
// admission webhook, which a static schema default would shadow.
func TestNetworkInternetEgressModeHasNoSchemaDefault(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-mode-default", &networkingv1alpha.NetworkInternetEgress{})
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	require.NotNil(t, got.Spec.Egress)
	require.NotNil(t, got.Spec.Egress.Internet)
	assert.Empty(t, got.Spec.Egress.Internet.Mode)
}

// TestNetworkWithoutEgressBlockStaysAbsent asserts the schema does not stamp an
// egress block onto a network that declares none.
func TestNetworkWithoutEgressBlockStaysAbsent(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-absent", nil)
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	assert.Nil(t, got.Spec.Egress)
}

// TestNetworkKeepsEachEgressMode asserts both modes are stored as written.
func TestNetworkKeepsEachEgressMode(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	for _, mode := range []networkingv1alpha.NetworkInternetEgressMode{
		networkingv1alpha.NetworkInternetEgressEnabled,
		networkingv1alpha.NetworkInternetEgressDisabled,
	} {
		name := "egress-" + strings.ToLower(string(mode))
		network := egressNetwork(name, &networkingv1alpha.NetworkInternetEgress{Mode: mode})
		require.NoError(t, cl.Create(ctx, network))
		t.Cleanup(func() { _ = cl.Delete(ctx, network) })

		var got networkingv1alpha.Network
		require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
		assert.Equal(t, mode, got.Spec.Egress.Internet.Mode)
	}
}

// TestNetworkRejectsUnknownEgressMode asserts the mode enum turns away a value
// no component implements, rather than storing it for a controller to ignore.
func TestNetworkRejectsUnknownEgressMode(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-bad-mode", &networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressMode("Paused"),
	})
	err := cl.Create(ctx, network)
	require.Error(t, err, "a mode outside the enum must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.mode")
}

func egressClaim(
	name string,
	egress *networkingv1alpha.NetworkInterfaceClaimEgress,
) *networkingv1alpha.NetworkInterfaceClaim {
	return &networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.NetworkInterfaceClaimSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "some-network"},
			Egress:  egress,
		},
	}
}

// TestClaimEgressDefaultsToInherit asserts an interface written today records
// Inherit, which is what lets Enabled and Disabled be accepted later without
// changing what an existing interface means.
func TestClaimEgressDefaultsToInherit(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	claim := egressClaim("claim-egress-default", nil)
	require.NoError(t, cl.Create(ctx, claim))
	t.Cleanup(func() { _ = cl.Delete(ctx, claim) })

	var got networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(claim), &got))
	require.NotNil(t, got.Spec.Egress, "the reserved block must be recorded, not left absent")
	require.NotNil(t, got.Spec.Egress.Internet)
	assert.Equal(t,
		networkingv1alpha.NetworkInterfaceClaimInternetEgressInherit,
		got.Spec.Egress.Internet.Mode)
}

// TestClaimRejectsNonInheritEgressMode asserts per-interface control cannot be
// asked for before any component routes per interface.
func TestClaimRejectsNonInheritEgressMode(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	for _, mode := range []string{"Enabled", "Disabled"} {
		t.Run(mode, func(t *testing.T) {
			claim := egressClaim("claim-egress-"+mode, &networkingv1alpha.NetworkInterfaceClaimEgress{
				Internet: &networkingv1alpha.NetworkInterfaceClaimInternetEgress{
					Mode: networkingv1alpha.NetworkInterfaceClaimInternetEgressMode(mode),
				},
			})
			err := cl.Create(ctx, claim)
			require.Errorf(t, err, "only Inherit may be accepted, %s must not be", mode)
			assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
			assert.Contains(t, err.Error(), "spec.egress.internet.mode")
		})
	}
}

// egressInterface builds a NetworkInterface holding only what the schema
// requires, so a status write is the only thing a test varies.
func egressInterface(name string) *networkingv1alpha.NetworkInterface {
	return &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "some-network"},
		},
	}
}

// TestInterfaceEgressDefaultsToInherit asserts the intent the claim declares is
// recorded beside the result it is reported against, so a realizer reads one
// object rather than following the claim.
func TestInterfaceEgressDefaultsToInherit(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	iface := egressInterface("iface-egress-default")
	require.NoError(t, cl.Create(ctx, iface))
	t.Cleanup(func() { _ = cl.Delete(ctx, iface) })

	var got networkingv1alpha.NetworkInterface
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(iface), &got))
	require.NotNil(t, got.Spec.Egress, "the reserved block must be recorded, not left absent")
	require.NotNil(t, got.Spec.Egress.Internet)
	assert.Equal(t,
		networkingv1alpha.NetworkInterfaceInternetEgressInherit,
		got.Spec.Egress.Internet.Mode)
}

// TestInterfaceRejectsNonInheritEgressMode asserts an interface accepts no more
// than the claim it is carried from does.
func TestInterfaceRejectsNonInheritEgressMode(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	for _, mode := range []string{"Enabled", "Disabled"} {
		t.Run(mode, func(t *testing.T) {
			iface := egressInterface("iface-egress-" + mode)
			iface.Spec.Egress = &networkingv1alpha.NetworkInterfaceEgress{
				Internet: &networkingv1alpha.NetworkInterfaceInternetEgress{
					Mode: networkingv1alpha.NetworkInterfaceInternetEgressMode(mode),
				},
			}
			err := cl.Create(ctx, iface)
			require.Errorf(t, err, "only Inherit may be accepted, %s must not be", mode)
			assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
			assert.Contains(t, err.Error(), "spec.egress.internet.mode")
		})
	}
}

// TestInterfaceEgressIsMutable asserts the field follows a claim that changes,
// unlike the rest of what the claim carries onto an interface.
func TestInterfaceEgressIsMutable(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	iface := egressInterface("iface-egress-mutable")
	require.NoError(t, cl.Create(ctx, iface))
	t.Cleanup(func() { _ = cl.Delete(ctx, iface) })

	iface.Spec.Egress = &networkingv1alpha.NetworkInterfaceEgress{
		Internet: &networkingv1alpha.NetworkInterfaceInternetEgress{
			Mode: networkingv1alpha.NetworkInterfaceInternetEgressInherit,
		},
	}
	require.NoError(t, cl.Update(ctx, iface))
}

func egressContext(
	name string,
	internet *networkingv1alpha.NetworkContextInternetEgress,
) *networkingv1alpha.NetworkContext {
	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: "some-network"},
			Location: locationsv1alpha1.LocationReference{Name: "loc"},
		},
	}
	if internet != nil {
		networkContext.Spec.Egress = &networkingv1alpha.NetworkContextEgress{Internet: internet}
	}
	return networkContext
}

// TestNetworkContextEgressIntentAbsentByDefault asserts the context spec
// carries no default mode, so a context projected before the field existed
// can be told apart from one instructed to block the internet.
func TestNetworkContextEgressIntentAbsentByDefault(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent-absent", nil)
	require.NoError(t, cl.Create(ctx, networkContext))
	t.Cleanup(func() { _ = cl.Delete(ctx, networkContext) })

	var got networkingv1alpha.NetworkContext
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(networkContext), &got))
	require.Nil(t, got.Spec.Egress, "no egress block may be stamped onto a context that was never projected")
}

// TestNetworkContextEgressIntentRoundTrips asserts the mode a location acts on
// survives a write.
func TestNetworkContextEgressIntentRoundTrips(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent", &networkingv1alpha.NetworkContextInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressDisabled,
	})
	require.NoError(t, cl.Create(ctx, networkContext))
	t.Cleanup(func() { _ = cl.Delete(ctx, networkContext) })

	var got networkingv1alpha.NetworkContext
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(networkContext), &got))
	require.NotNil(t, got.Spec.Egress)
	require.NotNil(t, got.Spec.Egress.Internet)
	assert.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, got.Spec.Egress.Internet.Mode)
}
