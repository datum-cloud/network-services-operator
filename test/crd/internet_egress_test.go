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
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		Class: "shared",
	})
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	assert.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, got.Spec.Egress.Internet.Mode)
	assert.Equal(t, "shared", got.Spec.Egress.Internet.Class)
	assert.Equal(t,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		got.Spec.Egress.Internet.Reach)
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
			networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv6Protocol,
		},
	})
	err := cl.Create(ctx, network)
	require.Error(t, err, "a repeated reach family must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.reach")
}

// TestNetworkReachNeedNotMatchIPFamilies pins that reach names destinations
// rather than the families the network holds, demonstrated inside the values
// reach accepts today: a dual-stack network reaching only IPv6 is valid. No
// rule may tie reach to ipFamilies in either direction, and widening reach
// must not introduce one.
func TestNetworkReachNeedNotMatchIPFamilies(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-reach-narrower", &networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
	})
	network.Spec.IPFamilies = []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}
	require.NoError(t, cl.Create(ctx, network),
		"reach must be free to name fewer families than the network carries")
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })
}

// TestNetworkRejectsIPv4Reach asserts IPv4 reach is refused rather than
// accepted and silently not delivered. It needs a resolver and a translator
// sharing a prefix, and the platform pairs neither. A network written today
// records IPv6, so accepting IPv4 later changes no existing network.
func TestNetworkRejectsIPv4Reach(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-reach-v4", &networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
	})
	err := cl.Create(ctx, network)
	require.Error(t, err, "IPv4 reach must be withheld until NAT64 is in place")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.reach")
	assert.Contains(t, err.Error(), "Only IPv6 is accepted")
}

// TestNetworkRejectsIPv4AlongsideIPv6Reach asserts the withholding is not
// escaped by listing IPv4 next to a family the platform does deliver.
func TestNetworkRejectsIPv4AlongsideIPv6Reach(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := egressNetwork("egress-reach-dual", &networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
		},
	})
	err := cl.Create(ctx, network)
	require.Error(t, err, "IPv4 must be refused even beside IPv6")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.reach")
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

func egressClass(name string, spec networkingv1alpha.InternetEgressClassSpec) *networkingv1alpha.InternetEgressClass {
	if spec.ControllerName == "" {
		spec.ControllerName = "networking.datumapis.com/cell-egress"
	}
	return &networkingv1alpha.InternetEgressClass{
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
		"apiVersion": networkingv1alpha.GroupVersion.String(),
		"kind":       "InternetEgressClass",
		"metadata": map[string]any{
			"name": "controller-default",
		},
		"spec": map[string]any{
			"sharing": string(networkingv1alpha.InternetEgressSharingShared),
			"reach":   []any{string(networkingv1alpha.IPv6Protocol)},
		},
	}}
	require.NoError(t, cl.Create(ctx, class))
	t.Cleanup(func() { _ = cl.Delete(ctx, class) })

	var got networkingv1alpha.InternetEgressClass
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: "controller-default"}, &got))
	assert.Equal(t, "networking.datumapis.com/cell-egress", got.Spec.ControllerName)
}

// TestInternetEgressClassRoundTripsOperatorFields asserts the class registers
// cluster-scoped and keeps the default-class annotation, the sharing an
// operator chose, and the parameters its implementation reads.
func TestInternetEgressClassRoundTripsOperatorFields(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("shared", networkingv1alpha.InternetEgressClassSpec{
		ControllerName: "networking.datumapis.com/cell-egress",
		Sharing:        networkingv1alpha.InternetEgressSharingShared,
		Reach:          []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ParametersRef: &networkingv1alpha.InternetEgressClassParametersRef{
			Group: "network.datumapis.com",
			Kind:  "EgressShardParameters",
			Name:  "shared-ipv6",
		},
	})
	class.Annotations = map[string]string{
		networkingv1alpha.InternetEgressClassDefaultAnnotation: "true",
	}
	require.NoError(t, cl.Create(ctx, class))
	t.Cleanup(func() { _ = cl.Delete(ctx, class) })

	var got networkingv1alpha.InternetEgressClass
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: class.Name}, &got))
	assert.Equal(t, "networking.datumapis.com/cell-egress", got.Spec.ControllerName)
	assert.Equal(t, networkingv1alpha.InternetEgressSharingShared, got.Spec.Sharing)
	assert.Equal(t, "true",
		got.Annotations[networkingv1alpha.InternetEgressClassDefaultAnnotation])
	require.NotNil(t, got.Spec.ParametersRef)
	assert.Equal(t, "shared-ipv6", got.Spec.ParametersRef.Name)
}

// TestInternetEgressClassRejectsUnknownSharing asserts sharing carries only the
// two values a consumer's stability is projected from.
func TestInternetEgressClassRejectsUnknownSharing(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("bad-sharing", networkingv1alpha.InternetEgressClassSpec{
		Sharing: networkingv1alpha.InternetEgressSharing("Pooled"),
		Reach:   []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
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

	class := egressClass("no-sharing", networkingv1alpha.InternetEgressClassSpec{
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
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

	class := egressClass("no-reach", networkingv1alpha.InternetEgressClassSpec{
		Sharing: networkingv1alpha.InternetEgressSharingDedicated,
	})
	err := cl.Create(ctx, class)
	require.Error(t, err, "reach must be required and non-empty")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.reach")
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

// TestNetworkInterfaceReportsEgressAddresses asserts the status a consumer
// reads their egress address from round-trips, including the stability that
// decides whether allow-listing it is safe.
func TestNetworkInterfaceReportsEgressAddresses(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	iface := egressInterface("egress-status")
	require.NoError(t, cl.Create(ctx, iface))
	t.Cleanup(func() { _ = cl.Delete(ctx, iface) })

	iface.Status.Egress = &networkingv1alpha.NetworkInterfaceEgressStatus{
		Internet: &networkingv1alpha.NetworkInterfaceInternetEgressStatus{
			SourceAddresses: []networkingv1alpha.InternetEgressSourceAddress{{
				Family:    networkingv1alpha.IPv6Protocol,
				Address:   "2001:db8:f00d::100",
				Stability: networkingv1alpha.InternetEgressAddressStabilityNone,
			}},
		},
	}
	require.NoError(t, cl.Status().Update(ctx, iface))

	var got networkingv1alpha.NetworkInterface
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(iface), &got))
	require.NotNil(t, got.Status.Egress)
	require.NotNil(t, got.Status.Egress.Internet)
	require.Len(t, got.Status.Egress.Internet.SourceAddresses, 1)
	assert.Equal(t, "2001:db8:f00d::100", got.Status.Egress.Internet.SourceAddresses[0].Address)
	assert.Equal(t,
		networkingv1alpha.InternetEgressAddressStabilityNone,
		got.Status.Egress.Internet.SourceAddresses[0].Stability)
}

// TestNetworkInterfaceRejectsUnknownStability asserts the field carrying the
// allow-listing contract cannot hold a third value a consumer has no reading
// for.
func TestNetworkInterfaceRejectsUnknownStability(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	iface := egressInterface("egress-bad-stability")
	require.NoError(t, cl.Create(ctx, iface))
	t.Cleanup(func() { _ = cl.Delete(ctx, iface) })

	iface.Status.Egress = &networkingv1alpha.NetworkInterfaceEgressStatus{
		Internet: &networkingv1alpha.NetworkInterfaceInternetEgressStatus{
			SourceAddresses: []networkingv1alpha.InternetEgressSourceAddress{{
				Family:    networkingv1alpha.IPv4Protocol,
				Address:   "198.51.100.7",
				Stability: networkingv1alpha.InternetEgressAddressStability("Location"),
			}},
		},
	}
	err := cl.Status().Update(ctx, iface)
	require.Error(t, err, "a stability outside the enum must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "stability")
}

// TestNetworkInterfaceKeepsAbsentEgressAbsent asserts the schema stamps no
// egress block onto an interface nothing has reported an address for. An empty
// list would read as an answer, and no answer exists until a shard publishes
// one.
func TestNetworkInterfaceKeepsAbsentEgressAbsent(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	iface := egressInterface("egress-unreported")
	require.NoError(t, cl.Create(ctx, iface))
	t.Cleanup(func() { _ = cl.Delete(ctx, iface) })

	var got networkingv1alpha.NetworkInterface
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(iface), &got))
	assert.Nil(t, got.Status.Egress)
}

// TestNetworkInterfaceRejectsRepeatedEgressFamily asserts the apiserver keeps
// one source address per family. A consumer reads the entry for the family
// their destination uses, so two entries for one family have no reading.
func TestNetworkInterfaceRejectsRepeatedEgressFamily(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	iface := egressInterface("egress-repeated-family")
	require.NoError(t, cl.Create(ctx, iface))
	t.Cleanup(func() { _ = cl.Delete(ctx, iface) })

	iface.Status.Egress = &networkingv1alpha.NetworkInterfaceEgressStatus{
		Internet: &networkingv1alpha.NetworkInterfaceInternetEgressStatus{
			SourceAddresses: []networkingv1alpha.InternetEgressSourceAddress{
				{
					Family:    networkingv1alpha.IPv6Protocol,
					Address:   "2001:db8:f00d::100",
					Stability: networkingv1alpha.InternetEgressAddressStabilityNone,
				},
				{
					Family:    networkingv1alpha.IPv6Protocol,
					Address:   "2001:db8:f00d::101",
					Stability: networkingv1alpha.InternetEgressAddressStabilityNone,
				},
			},
		},
	}
	err := cl.Status().Update(ctx, iface)
	require.Error(t, err, "two source addresses for one family must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
}

// TestClaimMirrorsEgressAddresses asserts the claim can carry the same egress
// block the interface reports, which is where a consumer reading one object
// finds it. The schema has to accept the interface's type unchanged for the
// mirror to be a copy rather than a translation.
func TestClaimMirrorsEgressAddresses(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	claim := &networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-mirror", Namespace: "default"},
		Spec: networkingv1alpha.NetworkInterfaceClaimSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "some-network"},
		},
	}
	require.NoError(t, cl.Create(ctx, claim))
	t.Cleanup(func() { _ = cl.Delete(ctx, claim) })

	claim.Status.Egress = &networkingv1alpha.NetworkInterfaceEgressStatus{
		Internet: &networkingv1alpha.NetworkInterfaceInternetEgressStatus{
			SourceAddresses: []networkingv1alpha.InternetEgressSourceAddress{{
				Family:    networkingv1alpha.IPv6Protocol,
				Address:   "2001:db8:f00d::100",
				Stability: networkingv1alpha.InternetEgressAddressStabilityNone,
			}},
		},
	}
	require.NoError(t, cl.Status().Update(ctx, claim))

	var got networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(claim), &got))
	require.NotNil(t, got.Status.Egress)
	require.NotNil(t, got.Status.Egress.Internet)
	require.Len(t, got.Status.Egress.Internet.SourceAddresses, 1)
	assert.Equal(t, "2001:db8:f00d::100", got.Status.Egress.Internet.SourceAddresses[0].Address)
	assert.Equal(t,
		networkingv1alpha.InternetEgressAddressStabilityNone,
		got.Status.Egress.Internet.SourceAddresses[0].Stability)
}

// TestClaimKeepsAbsentEgressAbsent asserts the schema stamps no egress block
// onto a claim nothing has mirrored one onto.
func TestClaimKeepsAbsentEgressAbsent(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	claim := &networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-mirror-absent", Namespace: "default"},
		Spec: networkingv1alpha.NetworkInterfaceClaimSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "some-network"},
		},
	}
	require.NoError(t, cl.Create(ctx, claim))
	t.Cleanup(func() { _ = cl.Delete(ctx, claim) })

	var got networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(claim), &got))
	assert.Nil(t, got.Status.Egress)
}

// TestNetworkContextReportsNoEgressAddress asserts the network reports no
// address at all. A network-level answer cannot be attributed to the interface
// whose traffic it describes, so the schema prunes one written anyway rather
// than storing a fact with no reader.
func TestNetworkContextReportsNoEgressAddress(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-no-status", Namespace: "default"},
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: "some-network"},
			Location: networkingv1alpha.LocationReference{Name: "loc"},
		},
	}
	require.NoError(t, cl.Create(ctx, networkContext))
	t.Cleanup(func() { _ = cl.Delete(ctx, networkContext) })

	written := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": networkingv1alpha.GroupVersion.String(),
		"kind":       "NetworkContext",
		"metadata": map[string]any{
			"name":            networkContext.Name,
			"namespace":       networkContext.Namespace,
			"resourceVersion": networkContext.ResourceVersion,
		},
		"status": map[string]any{
			"egress": map[string]any{
				"internet": map[string]any{
					"sourceAddresses": []any{map[string]any{
						"family":    string(networkingv1alpha.IPv6Protocol),
						"address":   "2001:db8:f00d::100",
						"stability": string(networkingv1alpha.InternetEgressAddressStabilityNone),
					}},
					"dns64Prefix": "64:ff9b::/96",
				},
			},
		},
	}}
	require.NoError(t, cl.Status().Update(ctx, written))

	var got unstructured.Unstructured
	got.SetGroupVersionKind(networkContext.GroupVersionKind())
	got.SetAPIVersion(networkingv1alpha.GroupVersion.String())
	got.SetKind("NetworkContext")
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(networkContext), &got))

	egress, found, err := unstructured.NestedMap(got.Object, "status", "egress")
	require.NoError(t, err)
	assert.Falsef(t, found, "status.egress must be pruned, got %v", egress)
}

func egressContext(
	name string,
	internet *networkingv1alpha.NetworkContextInternetEgress,
) *networkingv1alpha.NetworkContext {
	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: "some-network"},
			Location: networkingv1alpha.LocationReference{Name: "loc"},
		},
	}
	if internet != nil {
		networkContext.Spec.Egress = &networkingv1alpha.NetworkContextEgress{Internet: internet}
	}
	return networkContext
}

// TestNetworkContextEgressIntentAbsentByDefault pins the asymmetry with the
// network's own field: the context spec carries no default mode. A defaulted
// Disabled here could not be told apart from a context written before egress
// was projected, and a reader that cannot tell those apart would withdraw
// egress a consumer asked for.
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

// TestNetworkContextEgressIntentRoundTrips asserts every field a location acts
// on survives a write, including the resolved class name that keeps class
// selection with a single writer.
func TestNetworkContextEgressIntentRoundTrips(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent", &networkingv1alpha.NetworkContextInternetEgress{
		Mode:      networkingv1alpha.NetworkInternetEgressEnabled,
		Reach:     []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ClassName: "shared",
		Sharing:   networkingv1alpha.InternetEgressSharingShared,
		ParametersRef: &networkingv1alpha.InternetEgressClassParametersRef{
			Group: "network.datumapis.com",
			Kind:  "EgressShardParameters",
			Name:  "shared-ipv6",
		},
	})
	require.NoError(t, cl.Create(ctx, networkContext))
	t.Cleanup(func() { _ = cl.Delete(ctx, networkContext) })

	var got networkingv1alpha.NetworkContext
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(networkContext), &got))
	require.NotNil(t, got.Spec.Egress)
	internet := got.Spec.Egress.Internet
	require.NotNil(t, internet)
	assert.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, internet.Mode)
	assert.Equal(t, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, internet.Reach)
	assert.Equal(t, "shared", internet.ClassName)
	assert.Equal(t, networkingv1alpha.InternetEgressSharingShared, internet.Sharing)
	require.NotNil(t, internet.ParametersRef)
	assert.Equal(t, "network.datumapis.com", internet.ParametersRef.Group)
	assert.Equal(t, "EgressShardParameters", internet.ParametersRef.Kind)
	assert.Equal(t, "shared-ipv6", internet.ParametersRef.Name)
}

// TestNetworkContextRejectsUnknownEgressIntentMode asserts the projected mode
// carries the same two values the network's does.
func TestNetworkContextRejectsUnknownEgressIntentMode(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent-bad-mode", &networkingv1alpha.NetworkContextInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressMode("Inherit"),
	})
	err := cl.Create(ctx, networkContext)
	require.Error(t, err, "a mode outside the enum must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.mode")
}

// TestNetworkContextRejectsUnknownEgressIntentSharing asserts the sharing a
// location reports stability from holds only the two values it is projected
// from.
func TestNetworkContextRejectsUnknownEgressIntentSharing(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent-bad-sharing", &networkingv1alpha.NetworkContextInternetEgress{
		Mode:    networkingv1alpha.NetworkInternetEgressEnabled,
		Sharing: networkingv1alpha.InternetEgressSharing("Pooled"),
	})
	err := cl.Create(ctx, networkContext)
	require.Error(t, err, "a sharing value outside the enum must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.sharing")
}

// TestNetworkContextRejectsRepeatedEgressIntentReach asserts the projected
// reach is subject to the same uniqueness the network's own field is.
func TestNetworkContextRejectsRepeatedEgressIntentReach(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent-repeated-reach", &networkingv1alpha.NetworkContextInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv6Protocol,
		},
	})
	err := cl.Create(ctx, networkContext)
	require.Error(t, err, "a repeated reach family must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.reach")
}

// TestInternetEgressClassRejectsIPv4Reach asserts an operator cannot define a
// class advertising IPv4, which would promise what no component delivers.
func TestInternetEgressClassRejectsIPv4Reach(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	class := egressClass("reach-v4", networkingv1alpha.InternetEgressClassSpec{
		Sharing: networkingv1alpha.InternetEgressSharingShared,
		Reach:   []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
	})
	err := cl.Create(ctx, class)
	require.Error(t, err, "a class may not advertise IPv4 reach")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.reach")
}

// TestNetworkContextRejectsIPv4EgressIntentReach asserts the projection cannot
// carry what its source cannot declare.
func TestNetworkContextRejectsIPv4EgressIntentReach(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	networkContext := egressContext("egress-intent-reach-v4", &networkingv1alpha.NetworkContextInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
	})
	err := cl.Create(ctx, networkContext)
	require.Error(t, err, "a projected IPv4 reach must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	assert.Contains(t, err.Error(), "spec.egress.internet.reach")
}
