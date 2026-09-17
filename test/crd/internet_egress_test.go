// SPDX-License-Identifier: AGPL-3.0-only

package crd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
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

// TestNetworkInternetEgressDefaultsToDisabled asserts a network that declares
// egress without a mode reaches nothing. Egress is opted into, so the schema
// default has to be the safe value rather than the useful one.
func TestNetworkInternetEgressDefaultsToDisabled(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-mode-default", &networkingv1alpha.NetworkInternetEgress{
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
	})
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	require.NotNil(t, got.Spec.Egress)
	require.NotNil(t, got.Spec.Egress.Internet)
	assert.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, got.Spec.Egress.Internet.Mode)
}

// TestNetworkWithoutEgressBlockStaysAbsent asserts the schema does not stamp an
// egress block onto a network that declares none. An absent block and an
// explicit Disabled mean the same thing, and a reader that finds nothing must
// reach the same answer as one reading Disabled.
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

// TestNetworkKeepsExplicitEgressMode asserts the default does not overwrite a
// consumer who asked for egress.
func TestNetworkKeepsExplicitEgressMode(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-enabled", &networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol},
		Class: "shared",
	})
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	assert.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, got.Spec.Egress.Internet.Mode)
	assert.Equal(t, "shared", got.Spec.Egress.Internet.Class)
	assert.Equal(t, []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}, got.Spec.Egress.Internet.Reach)
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

// TestNetworkRejectsRepeatedReachFamily asserts a family may be named once. A
// repeated family says nothing a single entry does not.
func TestNetworkRejectsRepeatedReachFamily(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-repeated-reach", &networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv4Protocol,
		},
	})
	err := cl.Create(ctx, network)
	require.Error(t, err, "a repeated reach family must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.reach")
}

// TestNetworkReachNeedNotBeCarried pins that reach names destinations rather
// than the families the network holds. An IPv6-only network reaching IPv4 is
// the case the design exists for, so no rule may require reach to be a subset
// of ipFamilies.
func TestNetworkReachNeedNotBeCarried(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-reach-v4-on-v6", &networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
	})
	network.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	require.NoError(t, cl.Create(ctx, network),
		"an IPv6 network must be allowed to reach IPv4 destinations")
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })
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

func egressClass(name string, spec networkingv1alpha1.InternetEgressClassSpec) *networkingv1alpha1.InternetEgressClass {
	if spec.ControllerName == "" {
		spec.ControllerName = "networking.datumapis.com/cell-egress"
	}
	return &networkingv1alpha1.InternetEgressClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
}

// TestInternetEgressClassDefaultsController asserts a class written without a
// controller name gets one, the way ConnectorClass does. The object is built
// unstructured because a typed client sends controllerName as an empty string,
// which the apiserver treats as a value supplied rather than one omitted.
func TestInternetEgressClassDefaultsController(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": networkingv1alpha1.GroupVersion.String(),
		"kind":       "InternetEgressClass",
		"metadata": map[string]any{
			"name": "controller-default",
		},
		"spec": map[string]any{
			"sharing": string(networkingv1alpha1.InternetEgressSharingShared),
			"reach":   []any{string(networkingv1alpha1.IPv6Protocol)},
		},
	}}
	require.NoError(t, cl.Create(ctx, class))
	t.Cleanup(func() { _ = cl.Delete(ctx, class) })

	var got networkingv1alpha1.InternetEgressClass
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: "controller-default"}, &got))
	assert.Equal(t, "networking.datumapis.com/cell-egress", got.Spec.ControllerName)
}

// TestInternetEgressClassRoundTripsOperatorFields asserts the class registers
// cluster-scoped and keeps the default-class annotation, the sharing an
// operator chose, and the parameters its implementation reads.
func TestInternetEgressClassRoundTripsOperatorFields(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("shared", networkingv1alpha1.InternetEgressClassSpec{
		ControllerName: "networking.datumapis.com/cell-egress",
		Sharing:        networkingv1alpha1.InternetEgressSharingShared,
		Reach: []networkingv1alpha1.IPFamily{
			networkingv1alpha1.IPv6Protocol, networkingv1alpha1.IPv4Protocol,
		},
		ParametersRef: &networkingv1alpha1.InternetEgressClassParametersRef{
			Group: "network.datumapis.com",
			Kind:  "EgressShardParameters",
			Name:  "shared-ipv6",
		},
	})
	class.Annotations = map[string]string{
		networkingv1alpha1.InternetEgressClassDefaultAnnotation: "true",
	}
	require.NoError(t, cl.Create(ctx, class))
	t.Cleanup(func() { _ = cl.Delete(ctx, class) })

	var got networkingv1alpha1.InternetEgressClass
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: class.Name}, &got))
	assert.Equal(t, "networking.datumapis.com/cell-egress", got.Spec.ControllerName)
	assert.Equal(t, networkingv1alpha1.InternetEgressSharingShared, got.Spec.Sharing)
	assert.Equal(t, "true",
		got.Annotations[networkingv1alpha1.InternetEgressClassDefaultAnnotation])
	require.NotNil(t, got.Spec.ParametersRef)
	assert.Equal(t, "shared-ipv6", got.Spec.ParametersRef.Name)
}

// TestInternetEgressClassRejectsUnknownSharing asserts sharing carries only the
// two values a consumer's stability is projected from.
func TestInternetEgressClassRejectsUnknownSharing(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("bad-sharing", networkingv1alpha1.InternetEgressClassSpec{
		Sharing: networkingv1alpha1.InternetEgressSharing("Pooled"),
		Reach:   []networkingv1alpha1.IPFamily{networkingv1alpha1.IPv6Protocol},
	})
	err := cl.Create(ctx, class)
	require.Error(t, err, "a sharing value outside the enum must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.sharing")
}

// TestInternetEgressClassRequiresSharing asserts an operator states the sharing
// a class hands out rather than inheriting one the API picked for them.
func TestInternetEgressClassRequiresSharing(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("no-sharing", networkingv1alpha1.InternetEgressClassSpec{
		Reach: []networkingv1alpha1.IPFamily{networkingv1alpha1.IPv6Protocol},
	})
	err := cl.Create(ctx, class)
	require.Error(t, err, "sharing must be required")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.sharing")
}

// TestInternetEgressClassRejectsEmptyReach asserts a class that reaches nothing
// cannot be defined, since nothing a network declares could be served by it.
func TestInternetEgressClassRejectsEmptyReach(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("no-reach", networkingv1alpha1.InternetEgressClassSpec{
		Sharing: networkingv1alpha1.InternetEgressSharingDedicated,
	})
	err := cl.Create(ctx, class)
	require.Error(t, err, "reach must be required and non-empty")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.reach")
}

// TestNetworkContextReportsEgressAddresses asserts the status a consumer reads
// their egress address from round-trips, including the stability that decides
// whether allow-listing it is safe.
func TestNetworkContextReportsEgressAddresses(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-status", Namespace: "default"},
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: "some-network"},
			Location: networkingv1alpha.LocationReference{Name: "loc"},
		},
	}
	require.NoError(t, cl.Create(ctx, networkContext))
	t.Cleanup(func() { _ = cl.Delete(ctx, networkContext) })

	networkContext.Status.Egress = &networkingv1alpha.NetworkContextEgressStatus{
		Internet: &networkingv1alpha.NetworkContextInternetEgressStatus{
			SourceAddresses: []networkingv1alpha.InternetEgressSourceAddress{{
				Family:    networkingv1alpha.IPv6Protocol,
				Address:   "2001:db8:f00d::100",
				Stability: networkingv1alpha.InternetEgressAddressStabilityNone,
			}},
			DNS64Prefix: "64:ff9b::/96",
		},
	}
	require.NoError(t, cl.Status().Update(ctx, networkContext))

	var got networkingv1alpha.NetworkContext
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(networkContext), &got))
	require.NotNil(t, got.Status.Egress)
	require.NotNil(t, got.Status.Egress.Internet)
	require.Len(t, got.Status.Egress.Internet.SourceAddresses, 1)
	assert.Equal(t, "2001:db8:f00d::100", got.Status.Egress.Internet.SourceAddresses[0].Address)
	assert.Equal(t,
		networkingv1alpha.InternetEgressAddressStabilityNone,
		got.Status.Egress.Internet.SourceAddresses[0].Stability)
	assert.Equal(t, "64:ff9b::/96", got.Status.Egress.Internet.DNS64Prefix)
}

// TestNetworkContextRejectsUnknownStability asserts the field carrying the
// allow-listing contract cannot hold a third value a consumer has no reading
// for.
func TestNetworkContextRejectsUnknownStability(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-bad-stability", Namespace: "default"},
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: "some-network"},
			Location: networkingv1alpha.LocationReference{Name: "loc"},
		},
	}
	require.NoError(t, cl.Create(ctx, networkContext))
	t.Cleanup(func() { _ = cl.Delete(ctx, networkContext) })

	networkContext.Status.Egress = &networkingv1alpha.NetworkContextEgressStatus{
		Internet: &networkingv1alpha.NetworkContextInternetEgressStatus{
			SourceAddresses: []networkingv1alpha.InternetEgressSourceAddress{{
				Family:    networkingv1alpha.IPv4Protocol,
				Address:   "198.51.100.7",
				Stability: networkingv1alpha.InternetEgressAddressStability("Location"),
			}},
		},
	}
	err := cl.Status().Update(ctx, networkContext)
	require.Error(t, err, "a stability outside the enum must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "stability")
}
