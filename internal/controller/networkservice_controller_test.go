// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

type networkServiceScenario struct {
	t          *testing.T
	ctx        context.Context
	client     client.Client
	namespace  string
	reconciler *NetworkServiceReconciler
}

func newNetworkServiceScenario(t *testing.T) *networkServiceScenario {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	clients, err := planes()
	require.NoError(t, err)

	ctx := context.Background()
	cl := clients[0]

	namespaceName := "ns-" + sanitizeName(strings.ToLower(t.Name()))
	namespace := &corev1.Namespace{}
	namespace.Name = namespaceName
	require.NoError(t, cl.Create(ctx, namespace))

	return &networkServiceScenario{
		t:          t,
		ctx:        ctx,
		client:     cl,
		namespace:  namespaceName,
		reconciler: &NetworkServiceReconciler{},
	}
}

func (s *networkServiceScenario) createService(name string, matchLabels map[string]string) *networkingv1alpha.NetworkService {
	s.t.Helper()

	service := &networkingv1alpha.NetworkService{}
	service.Namespace = s.namespace
	service.Name = name
	service.Spec = networkingv1alpha.NetworkServiceSpec{
		NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
			Selector: metav1.LabelSelector{MatchLabels: matchLabels},
		},
		Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
	}
	require.NoError(s.t, s.client.Create(s.ctx, service))
	return service
}

func (s *networkServiceScenario) createMember(
	name, network string,
	memberLabels map[string]string,
	holderAvailable bool,
) *networkingv1alpha.NetworkInterface {
	s.t.Helper()
	status := metav1.ConditionFalse
	if holderAvailable {
		status = metav1.ConditionTrue
	}
	return s.createInterface(name, network, memberLabels, status,
		networkingv1alpha.NetworkInterfacePhaseBound)
}

// createRetiredInterface is a retained interface whose claim is gone. It keeps
// the labels a service selects on and holds its addresses, and nothing answers
// on them. It also keeps the holder's last word, which is the strictest case: a
// stale HolderAvailable=True must still not make released capacity a member.
func (s *networkServiceScenario) createRetiredInterface(
	name, network string,
	memberLabels map[string]string,
) {
	s.t.Helper()
	s.createInterface(name, network, memberLabels, metav1.ConditionTrue,
		networkingv1alpha.NetworkInterfacePhaseAvailable)
}

func (s *networkServiceScenario) createInterface(
	name, network string,
	memberLabels map[string]string,
	holderAvailable metav1.ConditionStatus,
	phase networkingv1alpha.NetworkInterfacePhase,
) *networkingv1alpha.NetworkInterface {
	s.t.Helper()

	iface := &networkingv1alpha.NetworkInterface{}
	iface.Namespace = s.namespace
	iface.Name = name
	iface.Labels = memberLabels
	iface.Spec = networkingv1alpha.NetworkInterfaceSpec{
		Network: networkingv1alpha.LocalNetworkRef{Name: network},
	}
	if phase == networkingv1alpha.NetworkInterfacePhaseBound {
		iface.Spec.ClaimRef = &networkingv1alpha.NetworkInterfaceClaimRef{Name: name}
	}
	require.NoError(s.t, s.client.Create(s.ctx, iface))

	iface.Status.Phase = phase
	apimeta.SetStatusCondition(&iface.Status.Conditions, metav1.Condition{
		Type:   networkingv1alpha.NetworkInterfaceHolderAvailable,
		Status: holderAvailable,
		Reason: "Test",
	})
	require.NoError(s.t, s.client.Status().Update(s.ctx, iface))
	return iface
}

// setHolderAvailable is the holder reporting itself in or out of service on an
// interface a service already counts as a member.
func (s *networkServiceScenario) setHolderAvailable(
	iface *networkingv1alpha.NetworkInterface,
	status metav1.ConditionStatus,
) {
	s.t.Helper()

	var current networkingv1alpha.NetworkInterface
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKeyFromObject(iface), &current))
	apimeta.SetStatusCondition(&current.Status.Conditions, metav1.Condition{
		Type:   networkingv1alpha.NetworkInterfaceHolderAvailable,
		Status: status,
		Reason: "Test",
	})
	require.NoError(s.t, s.client.Status().Update(s.ctx, &current))
}

func (s *networkServiceScenario) reconcile(service *networkingv1alpha.NetworkService) *networkingv1alpha.NetworkService {
	s.t.Helper()

	require.NoError(s.t, s.reconciler.reconcileService(s.ctx, s.client, client.ObjectKeyFromObject(service)))

	var reconciled networkingv1alpha.NetworkService
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKeyFromObject(service), &reconciled))
	return &reconciled
}

func locationStatus(
	t *testing.T,
	service *networkingv1alpha.NetworkService,
	name string,
) networkingv1alpha.NetworkServiceLocationStatus {
	t.Helper()
	for _, location := range service.Status.Locations {
		if location.Name == name {
			return location
		}
	}
	t.Fatalf("expected a status entry for location %q, got %+v", name, service.Status.Locations)
	return networkingv1alpha.NetworkServiceLocationStatus{}
}

func serviceCondition(
	t *testing.T,
	service *networkingv1alpha.NetworkService,
	conditionType string,
) *metav1.Condition {
	t.Helper()
	condition := apimeta.FindStatusCondition(service.Status.Conditions, conditionType)
	require.NotNilf(t, condition, "expected a %s condition", conditionType)
	return condition
}

func locationLabels(workload, location string) map[string]string {
	return map[string]string{
		"compute.datumapis.com/workload-name":           workload,
		networkingv1alpha.NetworkInterfaceLocationLabel: location,
	}
}

func TestNetworkServiceResolvesMembersPerLocation(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)
	s.createMember("storefront-1", "default", locationLabels("storefront", "us-central-1"), true)
	s.createMember("storefront-2", "default", locationLabels("storefront", "us-east-1"), true)
	s.createMember("other-0", "default", locationLabels("checkout", "us-central-1"), true)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	require.Len(t, service.Status.Locations, 2)

	central := locationStatus(t, service, "us-central-1")
	require.Equal(t, int32(2), central.Members)
	require.Equal(t, int32(2), central.Healthy)
	require.True(t, central.Serving)

	east := locationStatus(t, service, "us-east-1")
	require.Equal(t, int32(1), east.Members)
	require.Equal(t, int32(1), east.Healthy)
	require.True(t, east.Serving)

	require.Equal(t, networkingv1alpha.NetworkServiceSummary{Locations: 2, Members: 3, Healthy: 3}, service.Status.Summary)

	resolved := serviceCondition(t, service, networkingv1alpha.NetworkServiceMembersResolved)
	require.Equal(t, metav1.ConditionTrue, resolved.Status)
	require.Equal(t, networkServiceReasonResolved, resolved.Reason)

	require.Equal(t, metav1.ConditionTrue,
		serviceCondition(t, service, networkingv1alpha.NetworkServiceReady).Status)
	require.Equal(t, metav1.ConditionUnknown,
		serviceCondition(t, service, networkingv1alpha.NetworkServiceEndpointsReachable).Status)
}

func TestNetworkServiceUnavailableHolderIsAMemberNotHealthy(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)
	s.createMember("storefront-1", "default", locationLabels("storefront", "us-central-1"), false)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	central := locationStatus(t, service, "us-central-1")
	require.Equal(t, int32(2), central.Members)
	require.Equal(t, int32(1), central.Healthy)
	require.True(t, central.Serving)

	require.Equal(t, networkingv1alpha.NetworkServiceSummary{Locations: 1, Members: 2, Healthy: 1}, service.Status.Summary)
	require.Equal(t, metav1.ConditionTrue,
		serviceCondition(t, service, networkingv1alpha.NetworkServiceReady).Status)
}

func TestNetworkServiceLocationWithNoHealthyMembersStopsServing(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), false)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	central := locationStatus(t, service, "us-central-1")
	require.Equal(t, int32(1), central.Members)
	require.Equal(t, int32(0), central.Healthy)
	require.False(t, central.Serving)

	ready := serviceCondition(t, service, networkingv1alpha.NetworkServiceReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonNoServingLocations, ready.Reason)
}

func TestNetworkServiceUnlocatedMemberIsCounted(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)
	s.createMember("storefront-1", "default", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}, true)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	require.Len(t, service.Status.Locations, 1)
	require.Equal(t, int32(1), locationStatus(t, service, "us-central-1").Members)
	require.Equal(t, networkingv1alpha.NetworkServiceSummary{Locations: 1, Members: 2, Healthy: 1}, service.Status.Summary)

	resolved := serviceCondition(t, service, networkingv1alpha.NetworkServiceMembersResolved)
	require.Equal(t, metav1.ConditionTrue, resolved.Status)
	require.Contains(t, resolved.Message, networkingv1alpha.NetworkInterfaceLocationLabel)
}

func TestNetworkServiceSelectorMatchingNothing(t *testing.T) {
	s := newNetworkServiceScenario(t)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	require.Empty(t, service.Status.Locations)
	require.Equal(t, networkingv1alpha.NetworkServiceSummary{}, service.Status.Summary)

	resolved := serviceCondition(t, service, networkingv1alpha.NetworkServiceMembersResolved)
	require.Equal(t, metav1.ConditionFalse, resolved.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces, resolved.Reason)

	ready := serviceCondition(t, service, networkingv1alpha.NetworkServiceReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces, ready.Reason)
}

func TestNetworkServiceInterfacesSpanningTwoNetworks(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)
	s.createMember("storefront-1", "secondary", locationLabels("storefront", "us-east-1"), true)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	require.Empty(t, service.Status.Locations)
	require.Equal(t, networkingv1alpha.NetworkServiceSummary{}, service.Status.Summary)

	resolved := serviceCondition(t, service, networkingv1alpha.NetworkServiceMembersResolved)
	require.Equal(t, metav1.ConditionFalse, resolved.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonMultipleNetworks, resolved.Reason)
	require.Contains(t, resolved.Message, "default")
	require.Contains(t, resolved.Message, "secondary")

	ready := serviceCondition(t, service, networkingv1alpha.NetworkServiceReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonMultipleNetworks, ready.Reason)
}

func TestNetworkServiceStatusIsStableAcrossReconciles(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)

	service := s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	})

	first := s.reconcile(service)
	second := s.reconcile(first)

	require.Equal(t, first.ResourceVersion, second.ResourceVersion)
}

func TestNetworkServiceInterfaceEventsReachEveryMatchingService(t *testing.T) {
	s := newNetworkServiceScenario(t)

	storefront := s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	})
	central := s.createService("storefront-central", locationLabels("storefront", "us-central-1"))
	checkout := s.createService("checkout", map[string]string{
		"compute.datumapis.com/workload-name": "checkout",
	})

	member := s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)

	requests := servicesMatchingInterface(s.ctx, s.client, member)

	require.Len(t, requests, 2)

	matched := []client.ObjectKey{requests[0].NamespacedName, requests[1].NamespacedName}
	require.ElementsMatch(t, []client.ObjectKey{
		client.ObjectKeyFromObject(storefront),
		client.ObjectKeyFromObject(central),
	}, matched)
	require.NotContains(t, matched, client.ObjectKeyFromObject(checkout))
}

// Capacity a workload has released keeps every label the service selects on,
// and nothing answers on its addresses. Counting it would tell a consumer their
// service has members it cannot serve from.
func TestNetworkServiceExcludesRetiredCapacity(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)
	s.createRetiredInterface("storefront-1", "default", locationLabels("storefront", "us-central-1"))

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	central := locationStatus(t, service, "us-central-1")
	require.Equal(t, int32(1), central.Members)
	require.Equal(t, int32(1), central.Healthy)
	require.Equal(t, networkingv1alpha.NetworkServiceSummary{Locations: 1, Members: 1, Healthy: 1},
		service.Status.Summary)
}

// A selector reaching nothing but released capacity resolves to no members at
// all, rather than to members that cannot serve. The holder's last word does not
// change that: a stale HolderAvailable=True cannot put a released address back
// into a service.
func TestNetworkServiceOfOnlyRetiredCapacityHasNoMembers(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createRetiredInterface("storefront-0", "default", locationLabels("storefront", "us-central-1"))

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	require.Empty(t, service.Status.Locations)
	require.Equal(t, networkingv1alpha.NetworkServiceSummary{}, service.Status.Summary)

	resolved := serviceCondition(t, service, networkingv1alpha.NetworkServiceMembersResolved)
	require.Equal(t, metav1.ConditionFalse, resolved.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces, resolved.Reason)
}

// A network the service only reaches through released capacity is not a second
// network: refusing the whole service over it would take a working one down.
func TestNetworkServiceIgnoresRetiredCapacityOnAnotherNetwork(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), true)
	s.createRetiredInterface("storefront-1", "secondary", locationLabels("storefront", "us-east-1"))

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	resolved := serviceCondition(t, service, networkingv1alpha.NetworkServiceMembersResolved)
	require.Equal(t, metav1.ConditionTrue, resolved.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceSummary{Locations: 1, Members: 1, Healthy: 1},
		service.Status.Summary)
}

// A holder that has said nothing yet is not a holder reporting itself in
// service. The interface is capacity the service has, and no location serves
// from it until the holder speaks.
func TestNetworkServiceUnknownHolderIsAMemberNotHealthy(t *testing.T) {
	s := newNetworkServiceScenario(t)

	s.createInterface("storefront-0", "default", locationLabels("storefront", "us-central-1"),
		metav1.ConditionUnknown, networkingv1alpha.NetworkInterfacePhaseBound)

	service := s.reconcile(s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	}))

	central := locationStatus(t, service, "us-central-1")
	require.Equal(t, int32(1), central.Members)
	require.Equal(t, int32(0), central.Healthy)
	require.False(t, central.Serving)

	ready := serviceCondition(t, service, networkingv1alpha.NetworkServiceReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, networkingv1alpha.NetworkServiceReasonNoServingLocations, ready.Reason)
}

// Health follows the holder and nothing else. An interface the fabric has never
// reported programmed still serves once its holder says it is available.
func TestNetworkServiceHealthFollowsTheHolderNotProgrammed(t *testing.T) {
	s := newNetworkServiceScenario(t)

	member := s.createMember("storefront-0", "default", locationLabels("storefront", "us-central-1"), false)
	service := s.createService("storefront", map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
	})

	require.False(t, locationStatus(t, s.reconcile(service), "us-central-1").Serving)

	s.setHolderAvailable(member, metav1.ConditionTrue)

	reconciled := s.reconcile(service)
	require.Equal(t, int32(1), locationStatus(t, reconciled, "us-central-1").Healthy)
	require.True(t, locationStatus(t, reconciled, "us-central-1").Serving)
	require.Equal(t, metav1.ConditionTrue,
		serviceCondition(t, reconciled, networkingv1alpha.NetworkServiceReady).Status)

	s.setHolderAvailable(member, metav1.ConditionFalse)

	reconciled = s.reconcile(service)
	require.Equal(t, int32(1), locationStatus(t, reconciled, "us-central-1").Members)
	require.Equal(t, int32(0), locationStatus(t, reconciled, "us-central-1").Healthy)
	require.Equal(t, metav1.ConditionFalse,
		serviceCondition(t, reconciled, networkingv1alpha.NetworkServiceReady).Status)
}
