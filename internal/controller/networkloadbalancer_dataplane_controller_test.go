// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func dataPlaneFixture() (*networkingv1alpha.NetworkLoadBalancer, networkingv1alpha.NetworkLoadBalancerCellIntent) {
	lb := &networkingv1alpha.NetworkLoadBalancer{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: "lb-1", Generation: 3},
		Status: networkingv1alpha.NetworkLoadBalancerStatus{
			Conditions: []metav1.Condition{{
				Type: networkingv1alpha.NetworkLoadBalancerAllocated, Status: metav1.ConditionTrue,
				Reason: "Allocated", ObservedGeneration: 3, LastTransitionTime: metav1.Now(),
			}},
			Endpoints: []networkingv1alpha.NetworkLoadBalancerEndpointStatus{{
				RequestName: "public", Location: "us-central-1",
				Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{{
					Family: networkingv1alpha.IPv6Protocol, Value: "2607:ed40:63::2",
				}},
			}},
		},
	}
	intent := networkingv1alpha.NetworkLoadBalancerCellIntent{
		ObjectMeta: metav1.ObjectMeta{Name: "nlb-1", Generation: 2},
		Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
			Source: networkingv1alpha.NetworkLoadBalancerCellIntentSource{
				ParentUID: "lb-1", AddressRequest: "public",
			},
			Location: "us-central-1", VIP: "2607:ed40:63::2",
			State: networkingv1alpha.NetworkLoadBalancerCellIntentValidate,
		},
	}
	return lb, intent
}

func reportIntent(intent *networkingv1alpha.NetworkLoadBalancerCellIntent, programmed, announced metav1.ConditionStatus) {
	intent.Status = networkingv1alpha.NetworkLoadBalancerCellIntentStatus{
		ObservedGeneration: intent.Generation,
		Conditions: []metav1.Condition{
			{Type: networkingv1alpha.NetworkLoadBalancerCellIntentProgrammed, Status: programmed, Reason: "Reported",
				ObservedGeneration: intent.Generation, LastTransitionTime: metav1.Now()},
			{Type: networkingv1alpha.NetworkLoadBalancerCellIntentAnnounced, Status: announced, Reason: "Reported",
				ObservedGeneration: intent.Generation, LastTransitionTime: metav1.Now()},
		},
	}
}

func TestNLBDataPlaneReportsServedAddress(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})

	address := lb.Status.Endpoints[0].Addresses[0]
	require.True(t, apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
	require.True(t, apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced))
	require.True(t, address.Announced)
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady))
}

func TestNLBDataPlaneWaitsForCurrentReport(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	intent.Generation = 3
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})

	programmed := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, metav1.ConditionUnknown, programmed.Status)
	require.Equal(t, nlbReasonPendingDataPlane, programmed.Reason)
	require.False(t, lb.Status.Endpoints[0].Addresses[0].Announced)
}

func TestNLBDataPlaneNotReadyUntilAnnounced(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionFalse)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})

	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
	ready := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
}

func TestNLBDataPlaneKeepsAddressUntilWithdrawalConfirmed(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})

	intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn
	intent.Generation = 3
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))

	reportIntent(&intent, metav1.ConditionFalse, metav1.ConditionFalse)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})
	address := lb.Status.Endpoints[0].Addresses[0]
	programmed := apimeta.FindStatusCondition(address.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, "NotServed", programmed.Reason)
	require.False(t, address.Announced)
}

func TestNLBDataPlaneIgnoresOtherAddresses(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	intent.Spec.VIP = "2607:ed40:63::3"
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})

	programmed := apimeta.FindStatusCondition(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, "NotServed", programmed.Reason)
}

func TestNLBRouteProgrammedFollowsListenerAddresses(t *testing.T) {
	lb, intent := dataPlaneFixture()
	listener := &networkingv1alpha.NetworkLoadBalancerListener{AddressRefs: []string{"public"}}

	_, ok := listenerAddressesProgrammed(&networkingv1alpha.NetworkLoadBalancer{}, listener)
	require.False(t, ok)

	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})
	programmed, ok := listenerAddressesProgrammed(lb, listener)
	require.True(t, ok)
	require.Equal(t, metav1.ConditionTrue, programmed.Status)

	intent.Generation = 3
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent})
	programmed, ok = listenerAddressesProgrammed(lb, listener)
	require.True(t, ok)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, nlbRouteReasonAwaitingDataPlane, programmed.Reason)
}
