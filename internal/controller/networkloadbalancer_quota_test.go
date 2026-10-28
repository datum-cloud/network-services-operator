// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func newAddressQuotaScenario(t *testing.T, families ...networkingv1alpha.IPFamily) *loadBalancerAllocationScenario {
	t.Helper()
	s := newLoadBalancerAllocationScenario(t)
	s.control.PublicIPv4Class = "public-v4"
	s.control.Quota = fixedIPAMClient{s.ipam}
	lb := *s.lb.DeepCopy()
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[1:]
	lb.Spec.Exposure.Addresses[0].Families = families
	lb.Spec.Listeners[0].AddressRefs = []string{"edge"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	return s
}

func (s *loadBalancerAllocationScenario) quotaClaims() []quotav1alpha1.ResourceClaim {
	s.t.Helper()
	var list quotav1alpha1.ResourceClaimList
	require.NoError(s.t, s.ipam.List(s.ctx, &list, client.InNamespace(testProjectNS)))
	return list.Items
}

func (s *loadBalancerAllocationScenario) decideQuota(status metav1.ConditionStatus, reason string, at time.Time) {
	s.t.Helper()
	claims := s.quotaClaims()
	require.Len(s.t, claims, 1)
	claim := claims[0]
	claim.Status.Conditions = []metav1.Condition{{
		Type: quotav1alpha1.ResourceClaimGranted, Status: status, Reason: reason,
		Message: "decided", LastTransitionTime: metav1.NewTime(at),
	}}
	require.NoError(s.t, s.ipam.Status().Update(s.ctx, &claim))
}

func (s *loadBalancerAllocationScenario) ipv4Claims() []ipamv1alpha1.IPClaim {
	s.t.Helper()
	var claims []ipamv1alpha1.IPClaim
	for _, claim := range s.claims() {
		if claim.Spec.IPFamily == ipamv1alpha1.IPFamily(networkingv1alpha.IPv4Protocol) {
			claims = append(claims, claim)
		}
	}
	return claims
}

func requireAddressAllocated(t *testing.T, lb *networkingv1alpha.NetworkLoadBalancer,
	family networkingv1alpha.IPFamily, status metav1.ConditionStatus, reason string,
) {
	t.Helper()
	address := priorAddressStatus(lb.Status.Endpoints, "edge", "iad", family)
	condition := apimeta.FindStatusCondition(address.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
	require.NotNil(t, condition)
	require.Equal(t, status, condition.Status)
	require.Equal(t, reason, condition.Reason)
}

func TestNetworkLoadBalancerQuotaPendingWithholdsIPv4Claim(t *testing.T) {
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol)
	result, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Equal(t, networkLoadBalancerQuotaRetry, result.RequeueAfter)

	require.Empty(t, s.claims())
	claims := s.quotaClaims()
	require.Len(t, claims, 1)
	claim := claims[0]
	require.Equal(t, string(s.lb.UID), claim.Labels[networkLoadBalancerOwnerLabel])
	require.Equal(t, quotav1alpha1.ConsumerRef{
		APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: testProject,
	}, claim.Spec.ConsumerRef)
	require.Equal(t, &quotav1alpha1.UnversionedObjectReference{
		APIGroup: "networking.datumapis.com", Kind: "NetworkLoadBalancer", Name: s.lb.Name, Namespace: testProjectNS,
	}, claim.Spec.ResourceRef)
	require.Equal(t, []quotav1alpha1.ResourceRequest{{ResourceType: PublicIPv4AddressesResourceType, Amount: 1}},
		claim.Spec.Requests)

	s.decideQuota(metav1.ConditionFalse, quotav1alpha1.ResourceClaimPendingReason, time.Now())
	lb := s.reconcile()
	require.Empty(t, s.claims())
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionFalse, reasonQuotaPending)
	require.False(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
}

func TestNetworkLoadBalancerQuotaGrantedAllocatesIPv4(t *testing.T) {
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol)
	s.reconcile()
	s.decideQuota(metav1.ConditionTrue, quotav1alpha1.ResourceClaimGrantedReason, time.Now())
	s.reconcile()

	claims := s.claims()
	require.Len(t, claims, 1)
	require.Equal(t, s.quotaClaims()[0].Name, claims[0].Name)
	claims[0].Status.AllocatedCIDR = "192.0.2.10/32"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claims[0]))

	lb := s.reconcile()
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionTrue, "Allocated")
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated))
}

func TestNetworkLoadBalancerQuotaDeniedReportsExceededAndRefiles(t *testing.T) {
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol)
	s.reconcile()
	s.decideQuota(metav1.ConditionFalse, quotav1alpha1.ResourceClaimDeniedReason, time.Now())
	lb := s.reconcile()
	require.Empty(t, s.claims())
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionFalse, reasonQuotaExceeded)
	require.Len(t, s.quotaClaims(), 1, "a fresh denial is kept until the retry interval passes")

	s.decideQuota(metav1.ConditionFalse, quotav1alpha1.ResourceClaimDeniedReason, time.Now().Add(-2*networkLoadBalancerRetry))
	lb = s.reconcile()
	require.Empty(t, s.quotaClaims(), "a stale denial is withdrawn so the next pass re-evaluates")
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionFalse, reasonQuotaExceeded)

	lb = s.reconcile()
	require.Len(t, s.quotaClaims(), 1)
	require.Empty(t, s.claims())
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionFalse, reasonQuotaExceeded)
}

func TestNetworkLoadBalancerQuotaValidationFailureIsRejected(t *testing.T) {
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol)
	s.reconcile()
	s.decideQuota(metav1.ConditionFalse, quotav1alpha1.ResourceClaimValidationFailedReason, time.Now())
	lb := s.reconcile()
	require.Empty(t, s.claims())
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionFalse, reasonQuotaRejected)
}

func TestNetworkLoadBalancerQuotaLeavesIPv6Unclaimed(t *testing.T) {
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol)
	lb := s.reconcile()
	claims := s.claims()
	require.Len(t, claims, 1)
	require.Equal(t, ipamv1alpha1.IPFamily(networkingv1alpha.IPv6Protocol), claims[0].Spec.IPFamily)
	require.Len(t, s.quotaClaims(), 1)
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionFalse, reasonQuotaPending)
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv6Protocol, metav1.ConditionFalse, "AllocationPending")
}

func TestNetworkLoadBalancerQuotaKeepsHeldAddressWhenDenied(t *testing.T) {
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol)
	s.control.Quota = nil
	s.reconcile()
	claims := s.claims()
	require.Len(t, claims, 1)
	claims[0].Status.AllocatedCIDR = "192.0.2.10/32"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claims[0]))

	s.control.Quota = fixedIPAMClient{s.ipam}
	s.reconcile()
	s.decideQuota(metav1.ConditionFalse, quotav1alpha1.ResourceClaimDeniedReason, time.Now())
	lb := s.reconcile()
	require.Len(t, s.ipv4Claims(), 1)
	requireAddressAllocated(t, &lb, networkingv1alpha.IPv4Protocol, metav1.ConditionTrue, "Allocated")
}

func grantedQuotaScenario(t *testing.T) (*loadBalancerAllocationScenario, networkingv1alpha.NetworkLoadBalancer) {
	t.Helper()
	s := newAddressQuotaScenario(t, networkingv1alpha.IPv4Protocol)
	s.reconcile()
	s.decideQuota(metav1.ConditionTrue, quotav1alpha1.ResourceClaimGrantedReason, time.Now())
	s.reconcile()
	claims := s.claims()
	require.Len(t, claims, 1)
	claims[0].Status.AllocatedCIDR = "192.0.2.10/32"
	require.NoError(t, s.ipam.Status().Update(s.ctx, &claims[0]))
	return s, s.reconcile()
}

func TestNetworkLoadBalancerQuotaReleasedAfterWithdrawalAndIPClaim(t *testing.T) {
	s, lb := grantedQuotaScenario(t)
	setEdgeAnnouncement(&lb, metav1.ConditionTrue)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	require.NoError(t, s.client.Delete(s.ctx, &lb))
	s.reconcile()
	require.Len(t, s.claims(), 1)
	require.Len(t, s.quotaClaims(), 1, "quota stays held while the address is announced")

	lb = s.reconcile()
	setEdgeAnnouncement(&lb, metav1.ConditionFalse)
	require.NoError(t, s.client.Status().Update(s.ctx, &lb))
	_, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Empty(t, s.claims())
	require.Empty(t, s.quotaClaims())
}

func TestNetworkLoadBalancerQuotaHeldUntilIPClaimIsGone(t *testing.T) {
	s, lb := grantedQuotaScenario(t)
	claim := s.claims()[0]
	claim.Finalizers = []string{"ipam.miloapis.com/release"}
	require.NoError(t, s.ipam.Update(s.ctx, &claim))

	require.NoError(t, s.client.Delete(s.ctx, &lb))
	result, err := s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Equal(t, networkLoadBalancerQuotaRetry, result.RequeueAfter)
	require.Len(t, s.quotaClaims(), 1, "quota stays held while IPAM still holds the address")
	require.NoError(t, s.client.Get(s.ctx, client.ObjectKeyFromObject(s.lb), &lb))
	require.Contains(t, lb.Finalizers, networkLoadBalancerFinalizer)

	claim = s.claims()[0]
	claim.Finalizers = nil
	require.NoError(t, s.ipam.Update(s.ctx, &claim))
	_, err = s.control.reconcile(s.ctx, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Empty(t, s.quotaClaims())
}

func TestNetworkLoadBalancerQuotaReleasedWhenAddressIsDropped(t *testing.T) {
	s, lb := grantedQuotaScenario(t)
	lb.Spec.Exposure.Addresses[0].Locations.Names = []string{"fra"}
	require.NoError(t, s.client.Update(s.ctx, &lb))
	s.reconcile()
	for _, claim := range s.quotaClaims() {
		require.NotEqual(t, loadBalancerClaimName(s.lb, loadBalancerAddressRequest{
			requestName: "edge", location: "iad", family: networkingv1alpha.IPv4Protocol,
		}), claim.Name)
	}
	for _, claim := range s.ipv4Claims() {
		require.Equal(t, "fra", claim.Spec.Scope[ipamScopeRoleLocation].Name)
	}
}
