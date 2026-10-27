// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)

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
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)

	programmed := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, metav1.ConditionUnknown, programmed.Status)
	require.Equal(t, nlbReasonPendingDataPlane, programmed.Reason)
	require.False(t, lb.Status.Endpoints[0].Addresses[0].Announced)
}

func TestNLBDataPlaneNotReadyUntilAnnounced(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionFalse)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)

	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))
	ready := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
}

func TestNLBDataPlaneKeepsAddressUntilWithdrawalConfirmed(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)

	intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn
	intent.Generation = 3
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)
	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerProgrammed))

	reportIntent(&intent, metav1.ConditionFalse, metav1.ConditionFalse)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)
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
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)

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
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)
	programmed, ok := listenerAddressesProgrammed(lb, listener)
	require.True(t, ok)
	require.Equal(t, metav1.ConditionTrue, programmed.Status)

	intent.Generation = 3
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)
	programmed, ok = listenerAddressesProgrammed(lb, listener)
	require.True(t, ok)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, nlbRouteReasonAwaitingDataPlane, programmed.Reason)
}

func TestNLBDataPlaneClearsTerminatingLoadBalancerStatus(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(lb, []networkingv1alpha.NetworkLoadBalancerCellIntent{intent}, nil)
	lb.Finalizers = []string{"networking.test/hold"}
	now := metav1.Now()
	lb.DeletionTimestamp = &now
	project := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(lb, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: lb.Namespace, UID: "project-ns"}}).WithStatusSubresource(lb).Build()
	hub := fake.NewClientBuilder().WithScheme(project.Scheme()).WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}).Build()
	r := NetworkLoadBalancerDataPlaneReconciler{HubCluster: &fakeCluster{cl: hub}}
	require.NoError(t, r.reconcileDataPlane(context.Background(), "project", project, client.ObjectKeyFromObject(lb)))
	require.NoError(t, project.Get(context.Background(), client.ObjectKeyFromObject(lb), lb))
	require.False(t, lb.Status.Endpoints[0].Addresses[0].Announced)
	require.True(t, apimeta.IsStatusConditionFalse(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady))
}

func rejectedRoute(name, lbName, listener string, effective bool, conditions ...metav1.Condition) networkingv1alpha.NetworkLoadBalancerRoute {
	route := networkingv1alpha.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Generation: 4}}
	for i := range conditions {
		conditions[i].ObservedGeneration = route.Generation
		conditions[i].LastTransitionTime = metav1.Now()
	}
	route.Status.Parents = []networkingv1alpha.NetworkLoadBalancerRouteParentStatus{{
		Name: lbName, Listener: listener, Effective: effective, Conditions: conditions,
	}}
	return route
}

func withListener(lb *networkingv1alpha.NetworkLoadBalancer) *networkingv1alpha.NetworkLoadBalancer {
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{Name: "tcp-80", AddressRefs: []string{"public"}}}
	return lb
}

func TestNLBDataPlaneSurfacesRouteRejection(t *testing.T) {
	conflict := metav1.Condition{Type: networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs, Status: metav1.ConditionFalse,
		Reason: nlbRouteReasonBackendClaimConflict, Message: "Backend socket is already selected by older route \"old\""}
	lb, _ := dataPlaneFixture()
	applyNetworkLoadBalancerDataPlane(withListener(lb), nil, []networkingv1alpha.NetworkLoadBalancerRoute{
		rejectedRoute("web", "web", "tcp-80", true, conflict),
	})

	for _, conditionType := range []string{networkingv1alpha.NetworkLoadBalancerProgrammed, networkingv1alpha.NetworkLoadBalancerReady} {
		condition := apimeta.FindStatusCondition(lb.Status.Conditions, conditionType)
		require.NotNil(t, condition)
		require.Equal(t, metav1.ConditionFalse, condition.Status)
		require.Equal(t, nlbRouteReasonBackendClaimConflict, condition.Reason)
		require.Equal(t, `Route "web": `+conflict.Message, condition.Message)
	}
	announced := apimeta.FindStatusCondition(lb.Status.Endpoints[0].Addresses[0].Conditions, networkingv1alpha.NetworkLoadBalancerAnnounced)
	require.NotNil(t, announced)
	require.Equal(t, nlbRouteReasonBackendClaimConflict, announced.Reason)
}

func TestNLBDataPlaneSurfacesRouteWithNoEligibleEndpoints(t *testing.T) {
	lb, _ := dataPlaneFixture()
	applyNetworkLoadBalancerDataPlane(withListener(lb), nil, []networkingv1alpha.NetworkLoadBalancerRoute{
		rejectedRoute("web", "web", "tcp-80", true,
			metav1.Condition{Type: networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs, Status: metav1.ConditionTrue, Reason: "Resolved"},
			metav1.Condition{Type: networkingv1alpha.NetworkLoadBalancerRouteProgrammed, Status: metav1.ConditionFalse,
				Reason: nlbRouteReasonNoEligibleEndpoints, Message: "No enabled backend"}),
	})

	ready := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady)
	require.NotNil(t, ready)
	require.Equal(t, nlbRouteReasonNoEligibleEndpoints, ready.Reason)
}

func TestNLBDataPlaneIgnoresRouteStatusItCannotExplain(t *testing.T) {
	awaiting := metav1.Condition{Type: networkingv1alpha.NetworkLoadBalancerRouteProgrammed, Status: metav1.ConditionFalse,
		Reason: nlbRouteReasonAwaitingDataPlane, Message: "waiting"}
	conflict := metav1.Condition{Type: networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs, Status: metav1.ConditionFalse,
		Reason: nlbRouteReasonBackendClaimConflict, Message: "conflict"}
	staleRoute := rejectedRoute("stale", "web", "tcp-80", true, conflict)
	staleRoute.Generation = 5

	for name, route := range map[string]networkingv1alpha.NetworkLoadBalancerRoute{
		"follows load balancer status": rejectedRoute("web", "web", "tcp-80", true, awaiting),
		"other load balancer":          rejectedRoute("web", "api", "tcp-80", true, conflict),
		"other listener":               rejectedRoute("web", "web", "udp-53", true, conflict),
		"superseded route":             rejectedRoute("web", "web", "tcp-80", false, conflict),
		"stale report":                 staleRoute,
	} {
		t.Run(name, func(t *testing.T) {
			lb, _ := dataPlaneFixture()
			applyNetworkLoadBalancerDataPlane(withListener(lb), nil, []networkingv1alpha.NetworkLoadBalancerRoute{route})
			ready := apimeta.FindStatusCondition(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady)
			require.NotNil(t, ready)
			require.Equal(t, nlbReasonNotServed, ready.Reason)
		})
	}
}

func TestNLBDataPlaneKeepsServedAddressDespiteRejectedRoute(t *testing.T) {
	lb, intent := dataPlaneFixture()
	reportIntent(&intent, metav1.ConditionTrue, metav1.ConditionTrue)
	applyNetworkLoadBalancerDataPlane(withListener(lb), []networkingv1alpha.NetworkLoadBalancerCellIntent{intent},
		[]networkingv1alpha.NetworkLoadBalancerRoute{rejectedRoute("web", "web", "tcp-80", true,
			metav1.Condition{Type: networkingv1alpha.NetworkLoadBalancerRouteResolvedRefs, Status: metav1.ConditionFalse,
				Reason: nlbRouteReasonBackendClaimConflict, Message: "conflict"})})

	require.True(t, apimeta.IsStatusConditionTrue(lb.Status.Conditions, networkingv1alpha.NetworkLoadBalancerReady))
}

func TestNLBDataPlaneEnqueuesEachRouteParentOnce(t *testing.T) {
	route := &networkingv1alpha.NetworkLoadBalancerRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: networkingv1alpha.NetworkLoadBalancerRouteSpec{ParentRefs: []networkingv1alpha.NetworkLoadBalancerRouteParentRef{
			{Name: "web", Listener: "tcp-80"}, {Name: "web", Listener: "udp-53"}, {Name: "api"},
		}},
	}
	requests := loadBalancersForRoute(context.Background(), route)
	require.Len(t, requests, 2)
	require.Equal(t, "web", requests[0].Name)
	require.Equal(t, "api", requests[1].Name)
	require.Equal(t, "default", requests[0].Namespace)
}
