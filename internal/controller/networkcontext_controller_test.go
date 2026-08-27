// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/finalizer"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	"go.miloapis.com/ipam/pkg/ipamerrors"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// The class name a deployment configures for a location's subnet. The e2e
// fixtures name theirs this way; the platform names its own differently, which
// is why the operator takes it from configuration.
const testSubnetClass = "datum-subnet-v6"

type networkContextScenario struct {
	t          *testing.T
	ctx        context.Context
	client     client.Client
	ipam       *fakeIPAM
	reconciler *NetworkContextReconciler
	namespace  string
	name       string
}

// newNetworkContextScenario builds one namespace naming a project, plus a
// reconciler wired to a fake IPAM. Pass a nil factory for the deployment that
// never configured one.
func newNetworkContextScenario(t *testing.T, ipam *fakeIPAM) *networkContextScenario {
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

	reconciler := &NetworkContextReconciler{}
	if ipam != nil {
		reconciler.IPAM = ipam
		reconciler.SubnetClass = testSubnetClass
	}

	return &networkContextScenario{
		t:          t,
		ctx:        ctx,
		client:     cl,
		ipam:       ipam,
		reconciler: reconciler,
		namespace:  namespaceName,
		name:       networkContextName(testNetworkName, locationsv1alpha1.LocationReference{Name: testLocationName}),
	}
}

func (s *networkContextScenario) createContext(families ...networkingv1alpha.IPFamily) *networkingv1alpha.NetworkContext {
	s.t.Helper()
	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = s.namespace
	networkContext.Name = s.name
	networkContext.Spec = networkingv1alpha.NetworkContextSpec{
		Network:    networkingv1alpha.LocalNetworkRef{Name: testNetworkName},
		Location:   locationsv1alpha1.LocationReference{Name: testLocationName},
		IPFamilies: families,
		MTU:        1460,
	}
	require.NoError(s.t, s.client.Create(s.ctx, networkContext))
	return networkContext
}

// reconcile drives the reconciler the way the manager does: every write it
// makes wakes it again, so one call here runs it until it stops writing.
func (s *networkContextScenario) reconcile() {
	s.t.Helper()

	for range 8 {
		before, present := s.find()
		if !present {
			return
		}
		beforeVersion := before.ResourceVersion
		_, err := s.reconciler.reconcileNetworkContext(s.ctx, s.client, before)
		require.NoError(s.t, err)

		after, present := s.find()
		if !present || after.ResourceVersion == beforeVersion {
			return
		}
	}
	s.t.Fatal("the network context never stopped changing")
}

func (s *networkContextScenario) find() (*networkingv1alpha.NetworkContext, bool) {
	s.t.Helper()
	var networkContext networkingv1alpha.NetworkContext
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: s.name}, &networkContext)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(s.t, err)
	return &networkContext, true
}

func (s *networkContextScenario) get() *networkingv1alpha.NetworkContext {
	s.t.Helper()
	networkContext, present := s.find()
	require.True(s.t, present)
	return networkContext
}

func (s *networkContextScenario) readyCondition() *metav1.Condition {
	s.t.Helper()
	return apimeta.FindStatusCondition(s.get().Status.Conditions,
		networkingv1alpha.NetworkContextReady)
}

func (s *networkContextScenario) ipamCondition() *metav1.Condition {
	s.t.Helper()
	return apimeta.FindStatusCondition(s.get().Status.Conditions,
		networkingv1alpha.NetworkContextIPAMAllocated)
}

// subnet reads the Subnet this context publishes its range on, which is the API
// a consumer reads a location's addressing from.
func (s *networkContextScenario) subnet() (*networkingv1alpha.Subnet, bool) {
	s.t.Helper()
	var subnet networkingv1alpha.Subnet
	err := s.client.Get(s.ctx, client.ObjectKey{
		Namespace: s.namespace,
		Name:      s.name + "-ipv6",
	}, &subnet)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(s.t, err)
	return &subnet, true
}

func (s *networkContextScenario) publishedSubnet() *networkingv1alpha.Subnet {
	s.t.Helper()
	subnet, present := s.subnet()
	require.True(s.t, present, "the context must publish this location's range on a Subnet")
	return subnet
}

func (s *networkContextScenario) storedClaims() []ipamv1alpha1.IPClaim {
	s.t.Helper()
	cl, err := s.ipam.ClientForProject(testProject)
	require.NoError(s.t, err)
	var claims ipamv1alpha1.IPClaimList
	require.NoError(s.t, cl.List(s.ctx, &claims))
	return claims.Items
}

func TestNetworkContextClaimsItsSubnetWhenCreated(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	subnet := s.publishedSubnet()
	require.Equal(t, networkingv1alpha.IPv6Protocol, subnet.Spec.IPFamily)
	require.Equal(t, int32(64), subnet.Spec.PrefixLength, "a location is addressed from a /64")
	require.Equal(t, s.name, subnet.Spec.NetworkContext.Name)
	require.Equal(t, testLocationName, subnet.Spec.Location.Name)
	require.NotEmpty(t, subnet.Spec.StartAddress)

	// The context outlives nothing here: the subnet is its child, so the
	// location's range goes when the location's context does.
	require.Len(t, subnet.OwnerReferences, 1)
	require.Equal(t, "NetworkContext", subnet.OwnerReferences[0].Kind)
	require.Equal(t, s.name, subnet.OwnerReferences[0].Name)

	// The subnet is carried to the cells serving this location by a policy that
	// selects on the location it names; unlabelled, it reaches nowhere.
	require.Equal(t, testLocationName, subnet.Labels[networkingv1alpha.LocationLabel])
	require.Equal(t, testNetworkName, subnet.Labels[networkingv1alpha.NetworkLabel])

	allocated := s.get()
	require.NotNil(t, allocated.Status.IPAM)
	require.Equal(t, subnet.Name, allocated.Status.IPAM.IPv6SubnetRef.Name)

	ref := allocated.Status.IPAM.IPv6ClaimRef
	require.NotNil(t, ref)
	require.Equal(t, testProject, ref.Project)
	require.Equal(t, testProjectNS, ref.Namespace)
	require.Equal(t, networkContextSubnetClaimName(networkContext), ref.ClaimName)
	require.Equal(t, testSubnetClass+"-"+testNetworkName+"-"+testLocationName, ref.PoolName)

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Contains(t, condition.Message, subnet.Spec.StartAddress)

	require.Equal(t, []string{networkContextSubnetClaimName(networkContext)}, s.ipam.created()[testProject],
		"the claim must be addressed to the project the namespace names")
}

// The location's range is published on the Subnet and nowhere else. Two objects
// each carrying it is the duplication this work exists to remove one level up,
// and a reader would have to decide which of them to believe.
func TestNetworkContextPublishesTheRangeOnlyOnTheSubnet(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	published, err := json.Marshal(s.get().Status.IPAM)
	require.NoError(t, err)
	require.NotContains(t, string(published), s.publishedSubnet().Spec.StartAddress,
		"the context must point at the subnet holding the range, not carry a second copy of it")
}

// The gateway an interface is handed comes from the Subnet, and the subnet
// class reserves the first /96 of the /64 for it. Populating the Subnet from
// the real allocation is what makes that address the one the tenant addressing
// plan describes rather than one read off a three-entry table of city codes.
func TestNetworkContextSubnetCarriesTheRealGateway(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	subnet := s.publishedSubnet()
	gateway, err := netip.ParseAddr(subnetGateway(subnet))
	require.NoError(t, err)
	require.Equal(t, subnet.Spec.StartAddress+"1", gateway.String(),
		"the gateway is ::1 of the location's own /64")

	held, err := netip.ParsePrefix(subnet.Spec.StartAddress + "/64")
	require.NoError(t, err)
	require.True(t, held.Contains(gateway), "the gateway must lie inside the range the context holds")
}

// The context holds the range its class names for this location, and holds
// nothing inside that range. A block claimed to force the subnet into existence
// would be an address no interface can ever be given, held for as long as the
// context lives.
func TestNetworkContextHoldsTheRangeAndNothingInsideIt(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	claims := s.storedClaims()
	require.Len(t, claims, 1)
	require.Equal(t, ipamv1alpha1.TargetScopeRange, claims[0].Spec.Target)
	require.Equal(t, testSubnetClass, claims[0].Spec.ClassName,
		"the operator must name the class it was configured with")
	require.Empty(t, claims[0].Spec.PrefixLength,
		"a range is sized by the class that provisions it")

	// The scope is what ties this range to the pool endpoints cascade into. A
	// subnet scoped by location alone would hand two networks in one location
	// the same address space.
	require.Equal(t, testNetworkName, claims[0].Spec.Scope[ipamScopeRoleNetwork].Name)
	require.Equal(t, testLocationName, claims[0].Spec.Scope[ipamScopeRoleLocation].Name)
	published := s.publishedSubnet()
	require.Equal(t, fmt.Sprintf("%s/%d", published.Spec.StartAddress, published.Spec.PrefixLength),
		claims[0].Status.AllocatedCIDR, "the subnet published is the range the claim holds")
}

// The whole reason the hierarchy exists: a location's subnet is carved from the
// network's own range, not from some unrelated one that happens to come out of
// the same platform pool.
func TestNetworkContextSubnetLiesInsideTheNetworkRange(t *testing.T) {
	ipam := newFakeIPAM(t)
	s := newNetworkContextScenario(t, ipam)

	network := &networkingv1alpha.Network{}
	network.Namespace = s.namespace
	network.Name = testNetworkName
	network.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		MTU:        1460,
	}
	require.NoError(t, s.client.Create(s.ctx, network))

	networkReconciler := &NetworkReconciler{IPAM: ipam, PrefixClass: testNetworkPrefixClass}
	networkReconciler.finalizers = finalizer.NewFinalizers()
	require.NoError(t, networkReconciler.finalizers.Register(networkControllerFinalizer, noNetworkContexts{}))
	for range 4 {
		_, err := networkReconciler.reconcileNetwork(s.ctx, s.client, network)
		require.NoError(t, err)
		require.NoError(t, s.client.Get(s.ctx, client.ObjectKeyFromObject(network), network))
	}

	s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.NotNil(t, network.Status.IPAM)
	vpc, err := netip.ParsePrefix(network.Status.IPAM.IPv6Prefix)
	require.NoError(t, err)
	published := s.publishedSubnet()
	subnet, err := netip.ParsePrefix(fmt.Sprintf("%s/%d", published.Spec.StartAddress, published.Spec.PrefixLength))
	require.NoError(t, err)

	require.True(t, vpc.Contains(subnet.Addr()) && subnet.Bits() >= vpc.Bits(),
		"%s must lie inside the network's %s", subnet, vpc)
}

// A location's subnet is issued once. A reconcile that runs again — after a
// restart, a resync, or an edit — must find the range it already has rather
// than take a second one out of the network's own.
func TestNetworkContextSubnetIsClaimedOnlyOnce(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()
	first := s.publishedSubnet().Spec.StartAddress

	for range 3 {
		s.reconcile()
	}

	require.Equal(t, first, s.publishedSubnet().Spec.StartAddress)
	require.Equal(t, []string{networkContextSubnetClaimName(networkContext)}, s.ipam.created()[testProject])
	require.Len(t, s.storedClaims(), 1)
}

func TestNetworkContextReleasesItsSubnetOnDelete(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()
	require.Len(t, s.storedClaims(), 1)

	require.NoError(t, s.client.Delete(s.ctx, s.get()))
	s.reconcile()

	require.Equal(t, []string{networkContextSubnetClaimName(networkContext)}, s.ipam.deleted()[testProject])
	require.Empty(t, s.storedClaims(), "a location that goes must give its subnet back")

	_, present := s.find()
	require.False(t, present, "every finalizer must be released")
}

// IPAM refuses to give a subnet back while addresses are still allocated inside
// it, so a location whose interfaces have not gone yet cannot be released. The
// wait has to be reported on the context: an error loop leaves an operator with
// a context stuck Terminating and nothing but logs to say why.
func TestNetworkContextReportsASubnetItCannotYetRelease(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	claimName := networkContextSubnetClaimName(networkContext)
	s.ipam.refuseRelease(claimName, apierrors.NewConflict(
		ipamv1alpha1.SchemeGroupVersion.WithResource("ipclaims").GroupResource(), claimName,
		errors.New("ipam: range still has allocations inside it: 1 allocation(s) inside pool-x; release everything allocated inside this range first")))

	require.NoError(t, s.client.Delete(s.ctx, s.get()))
	s.reconcile()

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkContextReasonRangeOccupied, condition.Reason)
	require.Contains(t, condition.Message, "still has allocations inside it")
	require.Len(t, s.storedClaims(), 1, "a refused release must leave the subnet held")

	s.ipam.refuseRelease(claimName, nil)
	s.reconcile()

	require.Empty(t, s.storedClaims(), "the subnet goes back once what was inside it does")
	_, present := s.find()
	require.False(t, present, "every finalizer must be released")
}

// Exhaustion is a network running out of locations it can be addressed in,
// which is an operator's problem to widen a pool for. It has to read as that
// and not as a generic failure to allocate.
func TestNetworkContextReportsPoolExhaustion(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.ipam.refuse(networkContextSubnetClaimName(networkContext), fromTheWire(t,
		ipamerrors.NewPoolExhausted("datum-subnet-v6-vpc",
			`IPPool "datum-subnet-v6-vpc" is exhausted`)))

	s.reconcile()

	condition := s.ipamCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, string(allocationFailureExhausted), condition.Reason)
	require.Contains(t, condition.Message, "datum-subnet-v6-vpc")
	require.Nil(t, s.get().Status.IPAM, "nothing may be published from a failed allocation")
	_, present := s.subnet()
	require.False(t, present, "no subnet may be published from a failed allocation")
}

// A context carrying no IPv6 family is not addressed from the tenant ULA pool,
// so nothing is claimed for it. A context written before the field existed
// carries nothing either, and is treated the same rather than assumed into a
// family.
func TestNetworkContextWithoutIPv6ClaimsNothing(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	s.createContext(networkingv1alpha.IPv4Protocol)
	s.reconcile()

	require.Nil(t, s.get().Status.IPAM)
	require.Nil(t, s.ipamCondition())
	require.Zero(t, s.ipam.createdAnywhere())

	_, present := s.subnet()
	require.False(t, present, "an unclaimed location publishes no subnet of its own")
}

// Readiness is what every consumer of a presence waits on. A location that
// needs no address space has nothing outstanding, so it is ready as soon as it
// exists — anything else leaves an IPv4-only network unusable everywhere.
func TestNetworkContextWithoutIPv6IsReady(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	s.createContext(networkingv1alpha.IPv4Protocol)
	s.reconcile()

	condition := s.readyCondition()
	require.NotNil(t, condition, "a context nothing is allocated for must still say whether it is ready")
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkContextReadyReasonReady, condition.Reason)
}

// A location that is addressed from IPAM is ready once its subnet is held, and
// carries the allocation's own reason while it is not.
func TestNetworkContextReadinessFollowsItsSubnet(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.ipam.refuse(networkContextSubnetClaimName(networkContext), fromTheWire(t,
		ipamerrors.NewPoolExhausted("datum-subnet-v6-vpc",
			`IPPool "datum-subnet-v6-vpc" is exhausted`)))
	s.reconcile()

	condition := s.readyCondition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, string(allocationFailureExhausted), condition.Reason,
		"readiness carries the allocation's own reason rather than a second vocabulary for it")

	s.ipam.refuse(networkContextSubnetClaimName(networkContext), nil)
	s.reconcile()

	condition = s.readyCondition()
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, networkingv1alpha.NetworkContextReadyReasonReady, condition.Reason)
}

// A deployment that named no class for subnets claims nothing, the same as one
// that configured no IPAM connection at all. The subnet still comes into being
// under the first endpoint in the location, exactly as it did before.
func TestNetworkContextWithoutAConfiguredClassClaimsNothing(t *testing.T) {
	s := newNetworkContextScenario(t, newFakeIPAM(t))
	s.reconciler.SubnetClass = ""

	s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	require.Nil(t, s.get().Status.IPAM)
	require.Nil(t, s.ipamCondition())
	require.Zero(t, s.ipam.createdAnywhere())

	_, present := s.subnet()
	require.False(t, present, "an unclaimed location publishes no subnet of its own")
}

// A deployment that configured no IPAM connection keeps working exactly as it
// did: no subnet, no condition, and nothing holding the context back from
// deletion.
func TestNetworkContextWithoutIPAMConfiguredIsUnchanged(t *testing.T) {
	s := newNetworkContextScenario(t, nil)

	networkContext := s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()

	stored := s.get()
	require.Nil(t, stored.Status.IPAM)
	require.Nil(t, s.ipamCondition())
	require.NotContains(t, stored.Finalizers, networkContextSubnetFinalizer)

	require.NoError(t, s.client.Delete(s.ctx, networkContext))
	s.reconcile()

	_, present := s.find()
	require.False(t, present)
}

// deleteNetworkTeardown drives the network and its locations the way the
// manager does when a tenant deletes a network: each controller reconciles what
// it owns, and each other's writes wake the other. It stops when nothing
// changes any more, which is either both objects gone or a deadlock.
func (s *networkContextScenario) deleteNetworkTeardown(
	networkReconciler *NetworkReconciler,
	network *networkingv1alpha.Network,
) {
	s.t.Helper()

	for range 16 {
		before := s.teardownState(network)

		if stored, present := s.findNetwork(network); present {
			_, err := networkReconciler.reconcileNetwork(s.ctx, s.client, stored)
			require.NoError(s.t, err)
		}
		if stored, present := s.find(); present {
			_, err := s.reconciler.reconcileNetworkContext(s.ctx, s.client, stored)
			require.NoError(s.t, err)
		}

		if s.teardownState(network) == before {
			return
		}
	}
	s.t.Fatal("the teardown never settled")
}

func (s *networkContextScenario) teardownState(network *networkingv1alpha.Network) string {
	s.t.Helper()
	state := ""
	if stored, present := s.findNetwork(network); present {
		state += "network=" + stored.ResourceVersion
	}
	if stored, present := s.find(); present {
		state += " context=" + stored.ResourceVersion
	}
	return state
}

func (s *networkContextScenario) findNetwork(network *networkingv1alpha.Network) (*networkingv1alpha.Network, bool) {
	s.t.Helper()
	var stored networkingv1alpha.Network
	err := s.client.Get(s.ctx, client.ObjectKeyFromObject(network), &stored)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(s.t, err)
	return &stored, true
}

// A tenant deleting a network is not told to take its locations down first, and
// nothing in the API says so. The network holds the range every one of those
// locations carved a subnet out of, and IPAM refuses to give a range back while
// anything is allocated inside it, so a network that releases its own range
// before taking its locations down can never finish.
//
// The location here arrives the way propagation delivers one, carrying no owner
// reference, because that is what the platform actually puts in front of this
// controller.
func TestDeletingANetworkTakesDownTheLocationsHoldingItsSubnets(t *testing.T) {
	ipam := newFakeIPAM(t)
	s := newNetworkContextScenario(t, ipam)

	network := &networkingv1alpha.Network{}
	network.Namespace = s.namespace
	network.Name = testNetworkName
	network.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		MTU:        1460,
	}
	require.NoError(t, s.client.Create(s.ctx, network))

	networkReconciler := &NetworkReconciler{IPAM: ipam, PrefixClass: testNetworkPrefixClass}
	networkReconciler.finalizers = finalizer.NewFinalizers()
	require.NoError(t, networkReconciler.finalizers.Register(networkControllerFinalizer,
		networkContextFinalization{cl: s.client}))

	for range 4 {
		stored, present := s.findNetwork(network)
		require.True(t, present)
		_, err := networkReconciler.reconcileNetwork(s.ctx, s.client, stored)
		require.NoError(t, err)
	}
	s.createContext(networkingv1alpha.IPv6Protocol)
	s.reconcile()
	require.Len(t, s.storedClaims(), 2, "the network holds its range and the location holds a subnet in it")

	require.NoError(t, s.client.Delete(s.ctx, network))
	s.deleteNetworkTeardown(networkReconciler, network)

	_, present := s.find()
	require.False(t, present, "the location must be taken down with the network")
	_, present = s.findNetwork(network)
	require.False(t, present, "the network must not hang on a range its own locations hold")
	require.Empty(t, s.storedClaims(), "every range must go back to IPAM")
}

// networkContextFinalization runs the reconciler's own context finalization
// against the test client, rather than standing in for it.
type networkContextFinalization struct {
	cl client.Client
}

func (f networkContextFinalization) Finalize(ctx context.Context, obj client.Object) (finalizer.Result, error) {
	return finalizeNetworkContexts(ctx, f.cl, obj)
}

// Widening what a network waits on must not widen what it takes down. A
// location created the ordinary way carries its network as controller, and that
// owner is what decides: another network deleting must not reach it, even
// though both are in one namespace. Only a location carrying no owner at all —
// the shape propagation delivers — is matched by the network it names.
func TestANetworkTakesDownOnlyItsOwnLocations(t *testing.T) {
	cl, _ := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	namespace := &corev1.Namespace{}
	namespace.Name = "ns-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, cl.Create(ctx, namespace))

	newNetwork := func(name string) *networkingv1alpha.Network {
		network := &networkingv1alpha.Network{}
		network.Namespace = namespace.Name
		network.Name = name
		network.Spec = networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
			MTU:        1460,
		}
		require.NoError(t, cl.Create(ctx, network))
		return network
	}

	newContext := func(name, network string, owner *networkingv1alpha.Network) {
		networkContext := &networkingv1alpha.NetworkContext{}
		networkContext.Namespace = namespace.Name
		networkContext.Name = name
		networkContext.Spec = networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: network},
			Location: locationsv1alpha1.LocationReference{Name: testLocationName},
		}
		if owner != nil {
			require.NoError(t, controllerutil.SetControllerReference(owner, networkContext, cl.Scheme()))
		}
		require.NoError(t, cl.Create(ctx, networkContext))
	}

	alpha := newNetwork("alpha")
	beta := newNetwork("beta")

	newContext("alpha-owned", alpha.Name, alpha)
	newContext("alpha-propagated", alpha.Name, nil)
	newContext("beta-owned", beta.Name, beta)
	newContext("beta-propagated", beta.Name, nil)

	// A context the presence controller made for beta, which happens to name
	// alpha. The owner decides, so alpha must not reach it.
	newContext("beta-owned-naming-alpha", alpha.Name, beta)

	names := func(network *networkingv1alpha.Network) []string {
		found, err := networkContextsOfNetwork(ctx, cl, network)
		require.NoError(t, err)
		out := make([]string, 0, len(found))
		for i := range found {
			out = append(out, found[i].Name)
		}
		slices.Sort(out)
		return out
	}

	require.Equal(t, []string{"alpha-owned", "alpha-propagated"}, names(alpha))
	require.Equal(t, []string{"beta-owned", "beta-owned-naming-alpha", "beta-propagated"}, names(beta))
}
