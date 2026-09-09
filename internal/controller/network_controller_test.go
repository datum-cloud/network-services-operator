// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/finalizer"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	"go.miloapis.com/ipam/pkg/ipamerrors"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	testNetworkName = "vpc"

	// The class name a deployment configures. The platform's own is
	// datum-vpc-ipv6; the e2e fixtures name theirs differently, which is why
	// the operator takes it from configuration.
	testNetworkPrefixClass = "datum-network-v6"
)

type networkScenario struct {
	t          *testing.T
	ctx        context.Context
	client     client.Client
	ipam       *fakeIPAM
	reconciler *NetworkReconciler
	namespace  string
}

// newNetworkScenario builds one namespace naming a project, plus a reconciler
// wired to a fake IPAM. Pass a nil factory for the deployment that never
// configured one.
func newNetworkScenario(t *testing.T, ipam *fakeIPAM) *networkScenario {
	t.Helper()
	cl, _ := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	namespaceName := "ns-" + sanitizeName(strings.ToLower(t.Name()))
	namespace := &corev1.Namespace{}
	namespace.Name = namespaceName
	namespace.Labels = map[string]string{
		downstreamclient.UpstreamOwnerNamespaceLabel:   testProjectNS,
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + testProject,
	}
	require.NoError(t, cl.Create(ctx, namespace))

	reconciler := &NetworkReconciler{}
	if ipam != nil {
		reconciler.IPAM = ipam
		reconciler.PrefixClass = testNetworkPrefixClass
	}
	reconciler.finalizers = finalizer.NewFinalizers()
	require.NoError(t, reconciler.finalizers.Register(networkControllerFinalizer, noNetworkContexts{}))

	return &networkScenario{
		t:          t,
		ctx:        ctx,
		client:     cl,
		ipam:       ipam,
		reconciler: reconciler,
		namespace:  namespaceName,
	}
}

// noNetworkContexts stands in for the half of finalization that garbage-collects
// network contexts. There are none in these scenarios, so it always finishes.
type noNetworkContexts struct{}

func (noNetworkContexts) Finalize(context.Context, client.Object) (finalizer.Result, error) {
	return finalizer.Result{}, nil
}

func (s *networkScenario) createNetwork(families ...networkingv1alpha.IPFamily) *networkingv1alpha.Network {
	s.t.Helper()
	network := &networkingv1alpha.Network{}
	network.Namespace = s.namespace
	network.Name = testNetworkName
	network.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: families,
		MTU:        1460,
	}
	require.NoError(s.t, s.client.Create(s.ctx, network))
	return network
}

// reconcile drives the reconciler the way the manager does: every write it
// makes wakes it again, so one call here runs it until it stops writing. A
// finalizer lands on the first pass and the allocation on the next.
func (s *networkScenario) reconcile() {
	s.t.Helper()

	for range 8 {
		before, present := s.find()
		if !present {
			return
		}
		beforeVersion := before.ResourceVersion
		_, err := s.reconciler.reconcileNetwork(s.ctx, s.client, before)
		require.NoError(s.t, err)

		after, present := s.find()
		if !present || after.ResourceVersion == beforeVersion {
			return
		}
	}
	s.t.Fatal("the network never stopped changing")
}

func (s *networkScenario) find() (*networkingv1alpha.Network, bool) {
	s.t.Helper()
	var network networkingv1alpha.Network
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: testNetworkName}, &network)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(s.t, err)
	return &network, true
}

func (s *networkScenario) get() *networkingv1alpha.Network {
	s.t.Helper()
	var network networkingv1alpha.Network
	require.NoError(s.t, s.client.Get(s.ctx,
		client.ObjectKey{Namespace: s.namespace, Name: testNetworkName}, &network))
	return &network
}

func (s *networkScenario) ipamCondition() *metav1.Condition {
	s.t.Helper()
	return apimeta.FindStatusCondition(s.get().Status.Conditions,
		networkingv1alpha.NetworkIPAMAllocated)
}

func (s *networkScenario) readyCondition() *metav1.Condition {
	s.t.Helper()
	return apimeta.FindStatusCondition(s.get().Status.Conditions,
		networkingv1alpha.NetworkReady)
}

func (s *networkScenario) storedClaims() []ipamv1alpha1.IPClaim {
	s.t.Helper()
	cl, err := s.ipam.ClientForProject(testProject)
	require.NoError(s.t, err)
	var claims ipamv1alpha1.IPClaimList
	require.NoError(s.t, cl.List(s.ctx, &claims))
	return claims.Items
}

func TestNetworkClaimsItsPrefixWhenCreated(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	allocated := s.get()
	require.NotNil(t, allocated.Status.IPAM, "the network must publish the space it was given")
	require.Equal(t, "fd20:1000:1::/48", allocated.Status.IPAM.IPv6Prefix)

	ref := allocated.Status.IPAM.IPv6PrefixRef
	require.NotNil(t, ref)
	require.Equal(t, testProject, ref.Project)
	require.Equal(t, testProjectNS, ref.Namespace)
	require.Equal(t, networkPrefixClaimName(network), ref.ClaimName)
	require.Equal(t, testNetworkPrefixClass+"-"+testNetworkName, ref.PoolName)

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Contains(t, condition.Message, "fd20:1000:1::/48")

	require.Equal(t, []string{networkPrefixClaimName(network)}, s.ipam.created()[testProject],
		"the claim must be addressed to the project the namespace names")
}

// The network holds the range its class names for it, and holds nothing
// inside that range. A block claimed to force the range into existence would
// be an address no interface can ever be given, held for as long as the
// network lives.
func TestNetworkHoldsTheRangeAndNothingInsideIt(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	claims := s.storedClaims()
	require.Len(t, claims, 1)
	require.Equal(t, ipamv1alpha1.TargetScopeRange, claims[0].Spec.Target)
	require.Equal(t, testNetworkPrefixClass, claims[0].Spec.ClassName,
		"the operator must name the class it was configured with")
	require.Empty(t, claims[0].Spec.PrefixLength,
		"a range is sized by the class that provisions it")
	require.Equal(t, s.get().Status.IPAM.IPv6Prefix, claims[0].Status.AllocatedCIDR,
		"the published prefix is the range the claim holds, not a pool read separately")
}

// A network's prefix is issued once. A reconcile that runs again — after a
// restart, a resync, or an edit — must find the allocation it already has
// rather than take a second one out of the platform pool.
func TestNetworkPrefixIsClaimedOnlyOnce(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()
	first := s.get().Status.IPAM.IPv6Prefix

	for range 3 {
		s.reconcile()
	}

	require.Equal(t, first, s.get().Status.IPAM.IPv6Prefix)
	require.Equal(t, []string{networkPrefixClaimName(network)}, s.ipam.created()[testProject])
	require.Len(t, s.storedClaims(), 1)
}

func TestNetworkReleasesItsPrefixOnDelete(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()
	require.Len(t, s.storedClaims(), 1)

	require.NoError(t, s.client.Delete(s.ctx, s.get()))
	s.reconcile()

	require.Equal(t, []string{networkPrefixClaimName(network)}, s.ipam.deleted()[testProject])
	require.Empty(t, s.storedClaims(), "deleting a network must give its address space back")

	var gone networkingv1alpha.Network
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: testNetworkName}, &gone)
	require.True(t, apierrors.IsNotFound(err), "every finalizer must be released")
}

// IPAM refuses to give a range back while addresses are still allocated inside
// it, so a network whose interfaces have not gone yet cannot be released. The
// wait has to be reported on the network: an error loop leaves an operator with
// a network stuck Terminating and nothing but logs to say why.
func TestNetworkReportsARangeItCannotYetRelease(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	claimName := networkPrefixClaimName(network)
	s.ipam.refuseRelease(claimName, apierrors.NewConflict(
		ipamv1alpha1.SchemeGroupVersion.WithResource("ipclaims").GroupResource(), claimName,
		errors.New("ipam: range still has allocations inside it: 1 allocation(s) inside pool-x; release everything allocated inside this range first")))

	require.NoError(t, s.client.Delete(s.ctx, s.get()))
	s.reconcile()

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkReasonRangeOccupied, condition.Reason)
	require.Contains(t, condition.Message, "still has allocations inside it")
	require.Len(t, s.storedClaims(), 1, "a refused release must leave the range held")

	s.ipam.refuseRelease(claimName, nil)
	s.reconcile()

	require.Empty(t, s.storedClaims(), "the range goes back once what was inside it does")
	var gone networkingv1alpha.Network
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: testNetworkName}, &gone)
	require.True(t, apierrors.IsNotFound(err), "every finalizer must be released")
}

// Exhaustion is the platform running out of VPCs, which is an operator's
// problem to widen a pool for. It has to read as that and not as a generic
// failure to allocate.
func TestNetworkReportsPlatformPoolExhaustion(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.ipam.refuse(networkPrefixClaimName(network), fromTheWire(t,
		ipamerrors.NewPoolExhausted("datum-network-v6-root",
			`IPPool "datum-network-v6-root" is exhausted`)))

	s.reconcile()

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, string(allocationFailureExhausted), condition.Reason)
	require.Contains(t, condition.Message, "datum-network-v6-root")
	require.Nil(t, s.get().Status.IPAM, "nothing may be published from a failed allocation")
}

// The platform provisions a project's namespace with the project, so a missing
// one says the control plane was never bootstrapped. That is a different thing
// to look at than a pool that ran out, and must not be reported as one.
func TestNetworkReportsAMissingProjectNamespace(t *testing.T) {
	ipam := newFakeIPAM(t)
	ipam.noProjectNamespace = true
	s := newNetworkScenario(t, ipam)

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkReasonProjectNamespaceNotFound, condition.Reason)
	require.Contains(t, condition.Message, testProjectNS)
	require.Nil(t, s.get().Status.IPAM)
}

// An IPAM that predates the range claim drops the field and serves a block from
// inside the range instead. That block is not the network's range, so it is
// refused rather than published as one.
func TestNetworkRefusesABlockServedForARange(t *testing.T) {
	ipam := newFakeIPAM(t)
	ipam.prunesTarget = true
	s := newNetworkScenario(t, ipam)

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkReasonRangeUnsupported, condition.Reason)
	require.Nil(t, s.get().Status.IPAM)
}

// An IPv4-only network is not addressed from the tenant ULA pool, so nothing is
// claimed for it.
func TestNetworkWithoutIPv6ClaimsNothing(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv4Protocol)
	s.reconcile()

	require.Nil(t, s.get().Status.IPAM)
	require.Nil(t, s.ipamCondition())
	require.Zero(t, s.ipam.createdAnywhere())
}

// A deployment that named no class for network ranges claims nothing, the same
// as one that configured no IPAM connection at all.
func TestNetworkWithoutAConfiguredClassClaimsNothing(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))
	s.reconciler.PrefixClass = ""

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.Nil(t, s.get().Status.IPAM)
	require.Nil(t, s.ipamCondition())
	require.Zero(t, s.ipam.createdAnywhere())
}

// A deployment that configured no IPAM connection keeps working exactly as it
// did: no address space, no condition, and nothing holding the network back
// from deletion.
func TestNetworkWithoutIPAMConfiguredIsUnchanged(t *testing.T) {
	s := newNetworkScenario(t, nil)

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	network := s.get()
	require.Nil(t, network.Status.IPAM)
	require.Nil(t, s.ipamCondition())
	require.NotContains(t, network.Finalizers, networkPrefixFinalizer)

	require.NoError(t, s.client.Delete(s.ctx, network))
	s.reconcile()

	var gone networkingv1alpha.Network
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: testNetworkName}, &gone)
	require.True(t, apierrors.IsNotFound(err))
}

// The gateway is ::1 of the /64, which is inside that subnet's FIRST /96 —
// exactly the block `datum-subnet-v6` withholds with
// `reservations: {leading: 1, unitPrefixLength: 96}`. The reservation and this
// derivation are two statements of one rule, in two repositories, and this is
// what stops them drifting apart.
func TestSubnetGatewayLiesInTheReservedBlock(t *testing.T) {
	for _, start := range []string{
		"fd20:1000:1::",
		"fd20:1000:1:7::",
		"fd20:2000:aaaa:ffff::",
	} {
		subnet := &networkingv1alpha.Subnet{}
		subnet.Spec.StartAddress = start
		subnet.Spec.PrefixLength = 64

		gateway, err := netip.ParseAddr(subnetGateway(subnet))
		require.NoError(t, err, start)
		require.Equal(t, start+"1", gateway.String(), "the gateway is ::1 of the subnet")

		reserved, err := netip.ParsePrefix(start + "/96")
		require.NoError(t, err, start)
		require.True(t, reserved.Contains(gateway),
			"%s: the gateway must sit in the /96 the subnet class reserves", start)
	}
}

// A network is usable once the range it is addressed from is held, and Ready is
// the single answer to that. Without it a consumer has to know that the
// allocation condition is the one to read and that it only carries meaning for
// a network that asked for IPv6.
func TestNetworkIsReadyOnceItsPrefixIsHeld(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	ready := s.readyCondition()
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkReadyReasonReady, ready.Reason)
	require.Contains(t, ready.Message, "fd20:1000:1::/48")
	require.Equal(t, s.get().Generation, ready.ObservedGeneration)
}

// Ready is a summary, not a second opinion. It says what stopped the network
// being usable in the words the allocation condition already uses, because the
// reason is what a listing shows and an operator acts on.
func TestNetworkReadyCarriesTheAllocationFailure(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.ipam.refuse(networkPrefixClaimName(network), fromTheWire(t,
		ipamerrors.NewPoolExhausted("datum-network-v6-root",
			`IPPool "datum-network-v6-root" is exhausted`)))

	s.reconcile()

	allocated := s.ipamCondition()
	ready := s.readyCondition()
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, string(allocationFailureExhausted), ready.Reason)
	require.Equal(t, allocated.Reason, ready.Reason)
	require.Equal(t, allocated.Message, ready.Message)
}

// A retry is not a pending step: nothing is holding the range, so the network
// is not usable now and says so. It goes back to true on its own when the next
// attempt succeeds.
func TestNetworkReadyRecoversWhenAllocationSucceeds(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.ipam.refuse(networkPrefixClaimName(network), fromTheWire(t,
		ipamerrors.NewPoolExhausted("datum-network-v6-root",
			`IPPool "datum-network-v6-root" is exhausted`)))
	s.reconcile()
	require.Equal(t, metav1.ConditionFalse, s.readyCondition().Status)

	s.ipam.refuse(networkPrefixClaimName(network), nil)
	s.reconcile()

	ready := s.readyCondition()
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkReadyReasonReady, ready.Reason)
}

// A range that cannot be given back leaves a network stuck Terminating. Ready
// has to fall with the allocation condition rather than stay true underneath a
// deletion that is not progressing.
func TestNetworkReadyFallsWhenARangeCannotBeReleased(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	claimName := networkPrefixClaimName(network)
	s.ipam.refuseRelease(claimName, apierrors.NewConflict(
		ipamv1alpha1.SchemeGroupVersion.WithResource("ipclaims").GroupResource(), claimName,
		errors.New("ipam: range still has allocations inside it: 1 allocation(s) inside pool-x; release everything allocated inside this range first")))

	require.NoError(t, s.client.Delete(s.ctx, s.get()))
	s.reconcile()

	ready := s.readyCondition()
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkReasonRangeOccupied, ready.Reason)
}

// A network carrying no IPv6 cannot run anything, and admission only stops new
// ones. The networks that predate it are found by reading Ready, so Ready has
// to say so rather than call the network fine.
func TestNetworkWithoutIPv6IsNotReady(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv4Protocol)
	s.reconcile()

	require.Nil(t, s.ipamCondition(), "nothing was claimed, so nothing is reported about a claim")

	ready := s.readyCondition()
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkReadyReasonIPv6Required, ready.Reason)
	require.Contains(t, ready.Message, "spec.ipFamilies")
}

// A dual-stack network carries IPv6, so it is addressed and ready exactly as a
// single-stack IPv6 one is.
func TestDualStackNetworkIsAddressed(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol)
	s.reconcile()

	require.NotNil(t, s.get().Status.IPAM)

	ready := s.readyCondition()
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkReadyReasonReady, ready.Reason)
}

// A policy-mode network is addressed by what an operator creates in it, not by
// the tenant pool, so the operator claims nothing for it either.
func TestPolicyModeNetworkIsReady(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	network.Spec.IPAM.Mode = networkingv1alpha.NetworkIPAMModePolicy
	require.NoError(t, s.client.Update(s.ctx, network))
	s.reconcile()

	require.Nil(t, s.ipamCondition())
	require.Zero(t, s.ipam.createdAnywhere())
	require.Equal(t, metav1.ConditionTrue, s.readyCondition().Status)
}

// A deployment that configured no address service claims nothing on purpose and
// its networks are used exactly as they were before the operator reached IPAM.
// Unknown would leave every network in such a deployment permanently unanswered.
func TestNetworkWithoutIPAMConfiguredIsReady(t *testing.T) {
	s := newNetworkScenario(t, nil)

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.Nil(t, s.ipamCondition())

	ready := s.readyCondition()
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkReadyReasonReady, ready.Reason)
	require.Contains(t, ready.Message, "No address service is configured")
}

// The same holds for a deployment reaching IPAM that named no class for network
// ranges.
func TestNetworkWithoutAConfiguredClassIsReady(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))
	s.reconciler.PrefixClass = ""

	s.createNetwork(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.Nil(t, s.ipamCondition())
	require.Equal(t, metav1.ConditionTrue, s.readyCondition().Status)
}

// Before the controller has reached a network there is no answer to give, and
// an unknown one is what distinguishes that from a network that is not usable.
func TestNetworkReadyIsUnknownBeforeTheControllerRuns(t *testing.T) {
	s := newNetworkScenario(t, newFakeIPAM(t))

	s.createNetwork(networkingv1alpha.IPv6Protocol)

	ready := s.readyCondition()
	require.NotNil(t, ready, "the API must seed a readiness answer with the network")
	require.Equal(t, metav1.ConditionUnknown, ready.Status)
	require.Equal(t, "Pending", ready.Reason)
}
