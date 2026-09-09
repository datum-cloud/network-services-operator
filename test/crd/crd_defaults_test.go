// SPDX-License-Identifier: AGPL-3.0-only

// Package crd runs the generated CRDs against a real apiserver (envtest) to
// guard invariants the fake client cannot enforce. The apiserver validates a
// schema node's structural default against that node's own CEL rules at CRD
// registration, so an invalid default is rejected here — the fake client used
// by the rest of the suite never sees schema, defaults, or CEL.
//
// Regression guard for #257: a paranoiaLevels default of {} violated the
// detection>=blocking rule added in #251, so the apiserver rejected the whole
// CRD ("no such key: detection") and every deploy that applied it failed.
package crd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// testClient is nil when KUBEBUILDER_ASSETS is unset (plain `go test` without
// envtest binaries); tests then skip rather than fail. `make test` provides the
// assets, so CI exercises them.
var testClient client.Client

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		os.Exit(m.Run())
	}

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest start (installs generated CRDs): %v\n", err)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "add scheme: %v\n", err)
		os.Exit(1)
	}
	testClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "build client: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

func requireEnv(t *testing.T) client.Client {
	t.Helper()
	if testClient == nil {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	return testClient
}

func gatewayTargetRef(name string) gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName {
	return gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.GroupName,
			Kind:  gatewayv1.Kind("Gateway"),
			Name:  gatewayv1.ObjectName(name),
		},
	}
}

// TestTPPCRDInstallsAndDefaults asserts the generated CRD registers against a
// real apiserver — an invalid structural default (the #257 regression) would
// reject it during envtest start — and that a TPP created with only its
// required targetRef defaults paranoiaLevels to a pair satisfying
// detection>=blocking.
func TestTPPCRDInstallsAndDefaults(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: "default"},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTargetRef("gw")},
		},
	}
	require.NoError(t, cl.Create(ctx, tpp))
	t.Cleanup(func() { _ = cl.Delete(ctx, tpp) })

	var got networkingv1alpha.TrafficProtectionPolicy
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(tpp), &got))
	require.Len(t, got.Spec.RuleSets, 1)

	pl := got.Spec.RuleSets[0].OWASPCoreRuleSet.ParanoiaLevels
	assert.Equal(t, 1, pl.Blocking, "paranoiaLevels.blocking must default")
	assert.Equal(t, 1, pl.Detection, "paranoiaLevels.detection must default")
	assert.GreaterOrEqual(t, pl.Detection, pl.Blocking,
		"the defaulted pair must satisfy the detection>=blocking rule")
}

// TestTPPRejectsInvertedParanoia asserts the detection>=blocking CEL rule from
// #251 still rejects a user-supplied inverted pair, so the #257 default fix did
// not loosen the validation it depends on.
func TestTPPRejectsInvertedParanoia(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "inverted", Namespace: "default"},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTargetRef("gw")},
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
					ParanoiaLevels: networkingv1alpha.ParanoiaLevels{Blocking: 3, Detection: 1},
				},
			}},
		},
	}
	err := cl.Create(ctx, tpp)
	require.Error(t, err, "detection<blocking must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
}

// TestNetworkDefaultsToIPv6 asserts a Network created without ipFamilies
// carries IPv6. A NetworkInterfaceClaim defaults to IPv6 too, and the claim
// reconciler rejects a family its network does not carry, so an IPv4 default
// here makes the default workload on the default network unsatisfiable in
// every location.
func TestNetworkDefaultsToIPv6(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "family-defaults", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		},
	}
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	assert.Equal(t,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		got.Spec.IPFamilies)
}

// TestNetworkKeepsExplicitIPFamilies asserts the default does not overwrite an
// author's own choice of families.
func TestNetworkKeepsExplicitIPFamilies(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "family-explicit", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM: networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: []networkingv1alpha.IPFamily{
				networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
			},
		},
	}
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	require.Equal(t, []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}, got.Spec.IPFamilies)
}

// TestNetworkSchemaLeavesIPv4NetworksWritable pins the reason a network without
// IPv6 is turned away by an admission webhook rather than by a rule on this
// schema. A rule here would run on every write, and validation ratcheting —
// which lets an unchanged field carry a stale value through a spec update —
// does not cover the status subresource. The operator's whole answer to the
// networks that predate the rule is a condition, which is a status write, so a
// schema rule would gag the report and leave the operator retrying a rejected
// update forever.
//
// Everything below therefore has to keep working against the generated CRD: an
// IPv4-only network takes a spec patch, takes a status write, and can be
// repaired in place.
func TestNetworkSchemaLeavesIPv4NetworksWritable(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	network := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "family-legacy", Namespace: "default"},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		},
	}
	require.NoError(t, cl.Create(ctx, network))
	t.Cleanup(func() { _ = cl.Delete(ctx, network) })

	var got networkingv1alpha.Network
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))

	got.Spec.MTU = 1500
	require.NoError(t, cl.Update(ctx, &got),
		"a controller patching an unrelated field must not be turned away")

	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	apimeta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkReady,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.NetworkReadyReasonIPv6Required,
		ObservedGeneration: got.Generation,
		Message:            "reported unhealthy",
	})
	require.NoError(t, cl.Status().Update(ctx, &got),
		"reporting the network unhealthy is a status write and must reach the apiserver")

	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(network), &got))
	got.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	require.NoError(t, cl.Update(ctx, &got),
		"a user repairing the network in place must not be turned away")
}
