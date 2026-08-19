// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	"go.miloapis.com/ipam/pkg/ipamerrors"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const testNetworkUID = types.UID("4f2a9c1e-0000-4000-8000-00000000beef")

func vpcIdentityClass() *ipamv1alpha1.IPClass {
	class := &ipamv1alpha1.IPClass{}
	class.Name = routingIdentityClassName
	class.Spec.IPFamily = ipamv1alpha1.IPv6
	return class
}

type networkScenario struct {
	t       *testing.T
	ctx     context.Context
	client  client.Client
	ipam    *fakeIPAM
	network *networkingv1alpha.Network
	*NetworkReconciler
}

func newNetworkScenario(t *testing.T) *networkScenario {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	network := &networkingv1alpha.Network{}
	network.Namespace = testProjectNS
	network.Name = "default"
	network.UID = testNetworkUID
	network.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		MTU:        1460,
	}

	cl := fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(network).
		WithStatusSubresource(&networkingv1alpha.Network{}).
		Build()

	ipam := newFakeIPAM(t, vpcIdentityClass())

	return &networkScenario{
		t:                 t,
		ctx:               context.Background(),
		client:            cl,
		ipam:              ipam,
		network:           network,
		NetworkReconciler: &NetworkReconciler{IPAM: ipam},
	}
}

func (s *networkScenario) reconcile() {
	s.t.Helper()
	_, err := s.reconcileWithResult()
	require.NoError(s.t, err)
}

func (s *networkScenario) reconcileWithResult() (requeued bool, err error) {
	s.t.Helper()

	var network networkingv1alpha.Network
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKeyFromObject(s.network), &network))

	result, err := s.reconcileRoutingIdentity(s.ctx, s.client, testProject, &network)
	return result.RequeueAfter > 0, err
}

func (s *networkScenario) get() *networkingv1alpha.Network {
	s.t.Helper()
	var network networkingv1alpha.Network
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKeyFromObject(s.network), &network))
	return &network
}

func TestNetworkIsAllocatedARoutingIdentityOnCreate(t *testing.T) {
	s := newNetworkScenario(t)

	s.reconcile()

	network := s.get()
	require.NotNil(t, network.Status.RoutingIdentity)
	require.NotEmpty(t, network.Status.RoutingIdentity.Prefix)
	require.Equal(t, testProjectNS, network.Status.RoutingIdentity.ClaimRef.Namespace)
	require.Equal(t,
		routingIdentityClaimPrefix+string(testNetworkUID),
		network.Status.RoutingIdentity.ClaimRef.Name)

	require.True(t, apimeta.IsStatusConditionTrue(
		network.Status.Conditions, networkingv1alpha.NetworkAllocated))
	require.True(t, apimeta.IsStatusConditionTrue(
		network.Status.Conditions, networkingv1alpha.NetworkReady))

	require.Equal(t,
		map[string][]string{testProject: {routingIdentityClaimPrefix + string(testNetworkUID)}},
		s.ipam.created())
}

// The claim is named after the UID rather than the network, because a network
// deleted and recreated under one name is a different network and must not
// inherit the identity its predecessor's routes were built on.
func TestRoutingIdentityClaimIsNamedAfterTheNetworkUID(t *testing.T) {
	network := &networkingv1alpha.Network{}
	network.Namespace = testProjectNS
	network.Name = "default"
	network.UID = testNetworkUID

	successor := network.DeepCopy()
	successor.UID = "9c1d0000-0000-4000-8000-0000000000ff"

	require.NotEqual(t, routingIdentityClaimName(network), routingIdentityClaimName(successor))
	require.Contains(t, routingIdentityClaimName(network), string(testNetworkUID))
}

func TestRoutingIdentityIsAllocatedOnlyOnce(t *testing.T) {
	s := newNetworkScenario(t)

	s.reconcile()
	first := s.get().Status.RoutingIdentity.Prefix

	s.reconcile()
	s.reconcile()

	require.Equal(t, first, s.get().Status.RoutingIdentity.Prefix)
	require.Equal(t, 1, s.ipam.createdAnywhere())
}

// IPAM refuses a duplicate claim name rather than returning the claim that
// holds it, so a status lost after the allocation landed has to be recovered by
// reading, not by claiming again.
func TestRoutingIdentityIsRecoveredFromTheExistingClaim(t *testing.T) {
	s := newNetworkScenario(t)

	s.reconcile()
	allocated := s.get().Status.RoutingIdentity.Prefix

	network := s.get()
	network.Status.RoutingIdentity = nil
	require.NoError(t, s.client.Status().Update(s.ctx, network))

	s.reconcile()

	require.Equal(t, allocated, s.get().Status.RoutingIdentity.Prefix)
	require.Equal(t, 1, s.ipam.createdAnywhere())
}

func TestRoutingIdentityIsReleasedWhenTheNetworkIsDeleted(t *testing.T) {
	s := newNetworkScenario(t)

	s.reconcile()
	claimName := s.get().Status.RoutingIdentity.ClaimRef.Name

	require.NoError(t, s.releaseRoutingIdentity(s.ctx, testProject, s.get()))

	require.Equal(t, map[string][]string{testProject: {claimName}}, s.ipam.deleted())
}

// A network that never held an identity releases nothing, and does not need a
// project to be reachable to be deleted.
func TestReleaseIsANoOpWithoutAnIdentity(t *testing.T) {
	s := newNetworkScenario(t)

	require.NoError(t, s.releaseRoutingIdentity(s.ctx, testProject, s.get()))
	require.Empty(t, s.ipam.deleted())
}

func TestExhaustionIsSurfacedAsItsOwnReason(t *testing.T) {
	s := newNetworkScenario(t)
	s.ipam.refuse(routingIdentityClaimPrefix+string(testNetworkUID),
		ipamerrors.NewPoolExhausted("datum-vpc-identity-root", "no space left"))

	requeued, err := s.reconcileWithResult()
	require.NoError(t, err)
	require.True(t, requeued)

	network := s.get()
	require.Nil(t, network.Status.RoutingIdentity)

	allocated := apimeta.FindStatusCondition(
		network.Status.Conditions, networkingv1alpha.NetworkAllocated)
	require.NotNil(t, allocated)
	require.Equal(t, metav1.ConditionFalse, allocated.Status)
	require.Equal(t, string(allocationFailureExhausted), allocated.Reason)
	require.Contains(t, allocated.Message, "datum-vpc-identity-root")
	require.Contains(t, allocated.Message, "routing identity")

	ready := apimeta.FindStatusCondition(
		network.Status.Conditions, networkingv1alpha.NetworkReady)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, string(allocationFailureExhausted), ready.Reason)
}

func TestExhaustionClearsOnceSpaceIsAvailable(t *testing.T) {
	s := newNetworkScenario(t)
	claimName := routingIdentityClaimPrefix + string(testNetworkUID)
	s.ipam.refuse(claimName, ipamerrors.NewPoolExhausted("datum-vpc-identity-root", "no space left"))

	_, err := s.reconcileWithResult()
	require.NoError(t, err)

	s.ipam.refuse(claimName, nil)
	s.reconcile()

	network := s.get()
	require.NotNil(t, network.Status.RoutingIdentity)
	require.True(t, apimeta.IsStatusConditionTrue(
		network.Status.Conditions, networkingv1alpha.NetworkReady))
}

// Every network in a cell's namespace reconciles through here, and the project
// control plane holds only the namespaces the platform provisioned with it. A
// namespace it does not have has to be an answer on the network, not an error
// the controller retries forever with nothing said.
func TestAMissingProjectNamespaceIsSurfacedNotRetriedForever(t *testing.T) {
	s := newNetworkScenario(t)
	s.ipam.noProjectNamespace = true

	requeued, err := s.reconcileWithResult()
	require.NoError(t, err)
	require.True(t, requeued)

	network := s.get()
	require.Nil(t, network.Status.RoutingIdentity)

	allocated := apimeta.FindStatusCondition(
		network.Status.Conditions, networkingv1alpha.NetworkAllocated)
	require.NotNil(t, allocated)
	require.Equal(t, metav1.ConditionFalse, allocated.Status)
	require.Equal(t, networkingv1alpha.NetworkReasonProjectNamespaceNotFound, allocated.Reason)
	require.Contains(t, allocated.Message, testProjectNS)
}

func TestNoIPAMConnectionLeavesTheNetworkAlone(t *testing.T) {
	s := newNetworkScenario(t)
	s.IPAM = nil

	s.reconcile()

	network := s.get()
	require.Nil(t, network.Status.RoutingIdentity)
	require.Empty(t, network.Status.Conditions)
}

func TestRoutingIdentityProjectsIntoTheNetworkContext(t *testing.T) {
	network := &networkingv1alpha.Network{}
	networkContext := &networkingv1alpha.NetworkContext{}

	projectRoutingIdentityIfAllocated(networkContext, network)
	require.Empty(t, networkContext.Spec.RoutingIdentity)

	network.Status.RoutingIdentity = &networkingv1alpha.NetworkRoutingIdentity{
		Prefix: "fd00:a::/64",
	}
	projectRoutingIdentityIfAllocated(networkContext, network)
	require.Equal(t, "fd00:a::/64", networkContext.Spec.RoutingIdentity)

	// A network read before its allocation lands says nothing about the identity
	// a location is already forwarding on.
	network.Status.RoutingIdentity = nil
	projectRoutingIdentityIfAllocated(networkContext, network)
	require.Equal(t, "fd00:a::/64", networkContext.Spec.RoutingIdentity)
}
