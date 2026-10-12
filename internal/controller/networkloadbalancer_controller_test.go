// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type fixedIPAMClient struct{ client.Client }

func (f fixedIPAMClient) ClientForProject(project string) (client.Client, error) {
	if project != testProject {
		return nil, fmt.Errorf("unexpected project %q", project)
	}
	return f.Client, nil
}

type loadBalancerAllocationScenario struct {
	t       *testing.T
	ctx     context.Context
	client  client.Client
	ipam    client.Client
	lb      *networkingv1alpha.NetworkLoadBalancer
	control *NetworkLoadBalancerReconciler
}

func newLoadBalancerAllocationScenario(t *testing.T) *loadBalancerAllocationScenario {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, locationsv1alpha1.AddToScheme(scheme))
	require.NoError(t, ipamv1alpha1.AddToScheme(scheme))

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "tenant",
		Labels: map[string]string{
			downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + testProject,
			downstreamclient.UpstreamOwnerNamespaceLabel:   testProjectNS,
		},
	}}
	network := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns.Name}}
	network.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	lb := &networkingv1alpha.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{
		Name: "public", Namespace: ns.Name, UID: types.UID("f96c4e18-cc50-45a8-aead-b368210f13ca"),
		Finalizers: []string{networkLoadBalancerFinalizer},
	}}
	lb.Spec.NetworkRef.Name = network.Name
	lb.Spec.Exposure.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressRequest{
		{Name: "regional",
			Family: networkingv1alpha.IPv6Protocol, Locations: networkingv1alpha.NetworkLoadBalancerLocationSelection{
				From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"iad", "fra"},
			}},
		{Name: "edge",
			Family: networkingv1alpha.IPv6Protocol, Locations: networkingv1alpha.NetworkLoadBalancerLocationSelection{
				From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"iad"},
			}},
	}
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{
		Name: "web", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443,
		AddressRefs: []string{"regional", "edge"},
	}}
	locationIAD := &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "iad"}}
	locationFRA := &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "fra"}}
	cl := fakeclient.NewClientBuilder().WithScheme(scheme).
		WithObjects(ns, network, lb, locationIAD, locationFRA).
		WithStatusSubresource(lb).Build()
	ipam := fakeclient.NewClientBuilder().WithScheme(scheme).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testProjectNS}}).
		WithStatusSubresource(&ipamv1alpha1.IPClaim{}).Build()
	return &loadBalancerAllocationScenario{
		t: t, ctx: context.Background(), client: cl, ipam: ipam, lb: lb,
		control: &NetworkLoadBalancerReconciler{IPAM: fixedIPAMClient{ipam}, PublicIPv6Class: "public-v6"},
	}
}

func (s *loadBalancerAllocationScenario) reconcile() networkingv1alpha.NetworkLoadBalancer {
	s.t.Helper()
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(s.t, err)
	var lb networkingv1alpha.NetworkLoadBalancer
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKeyFromObject(s.lb), &lb))
	return lb
}

func (s *loadBalancerAllocationScenario) claims() []ipamv1alpha1.IPClaim {
	s.t.Helper()
	var list ipamv1alpha1.IPClaimList
	require.NoError(s.t, s.ipam.List(s.ctx, &list, client.InNamespace(testProjectNS)))
	return list.Items
}

func TestNetworkLoadBalancerAllocationHoldsPerLocationAddresses(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	require.Len(t, s.claims(), 3)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))

	for i, claim := range s.claims() {
		require.Equal(t, "public-v6", claim.Spec.ClassName)
		require.NotNil(t, claim.Spec.PrefixLength)
		require.Equal(t, int32(128), *claim.Spec.PrefixLength)
		require.Equal(t, ipamv1alpha1.ReclaimDelete, claim.Spec.ReclaimPolicy)
		require.NotEmpty(t, claim.Spec.Scope[ipamScopeRoleLocation].Name)
		claim.Status.AllocatedCIDR = fmt.Sprintf("2001:db8::%x/128", i+1)
		require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	}
	lb = s.reconcile()
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
	require.Len(t, lb.Status.Addresses, 3)
	for _, address := range lb.Status.Addresses {
		require.NotEmpty(t, address.Value)
		require.True(t, apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
		require.NotEmpty(t, address.Location)
	}
	require.Len(t, s.claims(), 3)
	require.Equal(t, lb.Status.Addresses, s.reconcile().Status.Addresses, "retry must reuse claims and addresses")
}

func TestNetworkLoadBalancerClaimNamesKeepAddressIdentityDistinct(t *testing.T) {
	lb := &networkingv1alpha.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{UID: types.UID("fixture")}}
	a := loadBalancerClaimName(lb, loadBalancerAddressRequest{
		requestName: "a-b", location: "c",
	})
	b := loadBalancerClaimName(lb, loadBalancerAddressRequest{
		requestName: "a", location: "b-c",
	})
	c := loadBalancerClaimName(lb, loadBalancerAddressRequest{
		requestName: "a-b", location: "d",
	})
	require.NotEqual(t, a, b)
	require.NotEqual(t, a, c)
}

func TestNetworkLoadBalancerWithoutIPAMDoesNotLeaveAnUnreleasableFinalizer(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := *s.lb.DeepCopy()
	lb.Finalizers = nil
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.control.IPAM = nil
	lb = s.reconcile()
	require.Empty(t, lb.Finalizers)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
}

func TestNetworkLoadBalancerExplicitLocationRetainsClaimWhenProjectionDisappears(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	s.reconcile()
	before := s.claims()
	require.NoError(t, s.client.Delete(s.ctx, &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "fra"}}))
	s.reconcile()
	require.Equal(t, before, s.claims())
}

func TestNetworkLoadBalancerAllLocationsAddsRegionalClaimAsLocationsAppear(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	for i := range lb.Spec.Exposure.Addresses {
		lb.Spec.Exposure.Addresses[i].Locations = networkingv1alpha.NetworkLoadBalancerLocationSelection{
			From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll,
		}
	}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.reconcile()
	require.Len(t, s.claims(), 4)
	require.NoError(t, s.client.Create(s.ctx, &locationsv1alpha1.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "sea"},
	}))
	lb = s.reconcile()
	require.Len(t, s.claims(), 6)
	require.Len(t, lb.Status.Addresses, 6)
}

func TestNetworkLoadBalancerRetainsAnnouncedAddressUntilWithdrawn(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	for i := range lb.Status.Addresses {
		if lb.Status.Addresses[i].RequestName == "edge" {
			apimeta.SetStatusCondition(&lb.Status.Addresses[i].Conditions, metav1.Condition{
				Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: metav1.ConditionTrue,
				Reason: "Published", Message: "BGP announcement active", ObservedGeneration: lb.Generation,
			})
		}
	}
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[:1]
	lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, s.claims(), 3, "announced IP must remain held")
	require.Len(t, lb.Status.Addresses, 3, "withdrawal must see the old address")

	for i := range lb.Status.Addresses {
		if lb.Status.Addresses[i].RequestName == "edge" {
			apimeta.SetStatusCondition(&lb.Status.Addresses[i].Conditions, metav1.Condition{
				Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: metav1.ConditionFalse,
				Reason: "Withdrawn", Message: "BGP announcement stopped", ObservedGeneration: lb.Generation,
			})
		}
	}
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	lb = s.reconcile()
	require.Len(t, lb.Status.Addresses, 2)
	require.Len(t, s.claims(), 2)
}

func TestNetworkLoadBalancerDeletionWaitsForWithdrawal(t *testing.T) {
	s := newLoadBalancerAllocationScenario(t)
	lb := s.reconcile()
	for i := range lb.Status.Addresses {
		if lb.Status.Addresses[i].RequestName == "edge" {
			apimeta.SetStatusCondition(&lb.Status.Addresses[i].Conditions, metav1.Condition{
				Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: metav1.ConditionTrue,
				Reason: "Published", Message: "BGP announcement active", ObservedGeneration: lb.Generation,
			})
		}
	}
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	require.NoError(t, s.client.Delete(s.ctx, &lb))
	lb = s.reconcile()
	require.NotNil(t, lb.DeletionTimestamp)
	require.Len(t, s.claims(), 3)

	for i := range lb.Status.Addresses {
		if lb.Status.Addresses[i].RequestName == "edge" {
			apimeta.SetStatusCondition(&lb.Status.Addresses[i].Conditions, metav1.Condition{
				Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: metav1.ConditionFalse,
				Reason: "Withdrawn", Message: "BGP announcement stopped", ObservedGeneration: lb.Generation,
			})
		}
	}
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Empty(t, s.claims())
}
