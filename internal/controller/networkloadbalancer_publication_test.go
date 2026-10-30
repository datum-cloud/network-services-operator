// SPDX-License-Identifier: AGPL-3.0-only
package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	galactic "go.datum.net/galactic/api/network/v1alpha1"
	api "go.datum.net/network-services-operator/api/v1alpha"
	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
)

type pausedIntentClient struct {
	client.Client
	beforeCreate func()
}

func (c *pausedIntentClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if intent, ok := obj.(*api.NetworkLoadBalancerCellIntent); ok && cellIntentRequestsServing(intent) && c.beforeCreate != nil {
		c.beforeCreate()
	}
	return c.Client.Create(ctx, obj, opts...)
}

func publicationScenario(t *testing.T) (*loadBalancerAllocationScenario, client.Client, *NetworkLoadBalancerCellIntentReconciler) {
	t.Helper()
	s := newLoadBalancerAllocationScenario(t)
	s.lb.Generation = 3
	s.lb.Spec.Exposure.Addresses[0].Locations.Names = []string{"iad"}
	s.lb.Spec.Exposure.Addresses[1].Locations.Names = []string{"fra"}
	s.lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	s.lb.Spec.Listeners = append(s.lb.Spec.Listeners, api.NetworkLoadBalancerListener{Name: "media", Protocol: api.NetworkLoadBalancerProtocolUDP, Port: 5001, AddressRefs: []string{"regional"}})
	require.NoError(t, s.client.Update(s.ctx, s.lb))
	var ns corev1.Namespace
	require.NoError(t, s.client.Get(s.ctx, client.ObjectKey{Name: s.lb.Namespace}, &ns))
	ns.UID = "project"
	require.NoError(t, s.client.Update(s.ctx, &ns))
	var network api.Network
	require.NoError(t, s.client.Get(s.ctx, client.ObjectKey{Namespace: s.lb.Namespace, Name: "default"}, &network))
	network.Status.Conditions = []metav1.Condition{{Type: api.NetworkReady, Status: metav1.ConditionTrue, ObservedGeneration: network.Generation, Reason: "Ready"}}
	require.NoError(t, s.client.Update(s.ctx, &network))
	route := testLoadBalancerRoute("web", time.Unix(1, 0))
	route.Namespace = s.lb.Namespace
	route.Spec.ParentRefs = []api.NetworkLoadBalancerRouteParentRef{{Name: s.lb.Name, Listener: "web"}}
	service := testRouteNetworkService()
	service.Namespace = s.lb.Namespace
	member := testRouteMember("web-0-eth0", true, "fd20:0:30::1:0:0/96")
	member.Namespace = s.lb.Namespace
	member.Labels[api.NetworkInterfaceLocationLabel] = "iad"
	member.Labels[api.NetworkInterfaceSourceNamespaceLabel] = nlbProgrammingTestNamespace
	member.Labels[api.NetworkInterfaceSourceUIDLabel] = "iface-1"
	require.NoError(t, s.client.Create(s.ctx, route))
	require.NoError(t, s.client.Create(s.ctx, service))
	require.NoError(t, s.client.Create(s.ctx, member))
	s.reconcile()
	for i, claim := range s.claims() {
		claim.UID = types.UID(fmt.Sprintf("claim-%d", i))
		require.NoError(t, s.ipam.Update(s.ctx, &claim))
		claim.Status.Address = fmt.Sprintf("2001:db8::%x", i+1)
		require.NoError(t, s.ipam.Status().Update(s.ctx, &claim))
	}
	s.reconcile()
	hub := fake.NewClientBuilder().WithScheme(s.client.Scheme()).WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nlbProgrammingTestNamespace}}).WithStatusSubresource(&api.NetworkLoadBalancerCellIntent{}).Build()
	return s, hub, &NetworkLoadBalancerCellIntentReconciler{HubCluster: &fakeCluster{cl: hub}}
}

func TestNLBPublicationHoldClosesFirstStatusRace(t *testing.T) {
	for _, operation := range []string{"delete", "remove-address", "remove-location"} {
		t.Run(operation, func(t *testing.T) {
			s, hub, publisher := publicationScenario(t)
			ctx := s.ctx
			key := client.ObjectKeyFromObject(s.lb)
			lb := s.reconcile()
			var old api.NetworkLoadBalancerAddressStatus
			for _, endpoint := range lb.Status.Endpoints {
				if endpoint.RequestName == "regional" {
					old = endpoint.Addresses[0]
				}
			}
			require.True(t, old.Coordinated)
			require.True(t, old.WithdrawalPending)
			require.False(t, old.Retiring)
			require.False(t, old.Announced)
			paused, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			blocking := &pausedIntentClient{Client: hub, beforeCreate: func() { close(paused); <-resume }}
			publisher.HubCluster = &fakeCluster{cl: blocking, reader: hub}
			go func() { _, err := publisher.reconcileParentIntents(ctx, testProject, s.client, key); finished <- err }()
			select {
			case <-paused:
			case err := <-finished:
				t.Fatalf("publisher never reached Create: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("publisher did not reach Create")
			}
			require.NoError(t, s.client.Get(ctx, key, &lb))
			switch operation {
			case "delete":
				require.NoError(t, s.client.Delete(ctx, &lb))
			case "remove-address":
				lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[1:]
				lb.Spec.Listeners[0].AddressRefs = []string{"edge"}
				lb.Spec.Listeners[1].AddressRefs = []string{"edge"}
				lb.Generation++
				require.NoError(t, s.client.Update(ctx, &lb))
			case "remove-location":
				lb.Spec.Exposure.Addresses[0].Locations.Names = []string{"fra"}
				lb.Generation++
				require.NoError(t, s.client.Update(ctx, &lb))
			}
			_, err := s.control.reconcile(ctx, s.client, key)
			require.NoError(t, err)
			require.NoError(t, s.ipam.Get(ctx, client.ObjectKey{Namespace: old.ClaimRef.Namespace, Name: old.ClaimRef.Name}, &ipamv1alpha1.IPClaim{}))
			require.NoError(t, s.client.Get(ctx, key, &lb))
			for _, endpoint := range lb.Status.Endpoints {
				if endpoint.RequestName == "regional" && endpoint.Location == "iad" {
					require.True(t, endpoint.Addresses[0].Retiring)
					require.True(t, endpoint.Addresses[0].WithdrawalPending)
				}
			}
			applyNetworkLoadBalancerDataPlane(&lb, nil, nil)
			for _, endpoint := range lb.Status.Endpoints {
				if endpoint.RequestName == "regional" && endpoint.Location == "iad" {
					require.True(t, endpoint.Addresses[0].WithdrawalPending)
				}
			}
			close(resume)
			require.NoError(t, <-finished)
			publisher.HubCluster = &fakeCluster{cl: hub}
			intents, err := publishedListenerIntents(ctx, hub, key)
			require.NoError(t, err)
			require.Len(t, intents, 1)
			cell, adapter, placeholder := nlbProgrammingFixture(t)
			require.NoError(t, cell.Delete(ctx, placeholder))
			adapter.SharedAddressListeners = true
			adapter.HubCluster = &fakeCluster{cl: hub, reader: hub}
			adapter.resolveLocation = func(context.Context) (string, error) { return "iad", nil }
			var iface api.NetworkInterface
			require.NoError(t, cell.Get(ctx, client.ObjectKey{Namespace: nlbProgrammingTestNamespace, Name: "web-0-eth0"}, &iface))
			iface.Labels[api.NetworkInterfaceLocationLabel] = "iad"
			iface.Spec.Network.Name = "default"
			require.NoError(t, cell.Update(ctx, &iface))
			copy := intents[0].DeepCopy()
			copy.Namespace = nlbProgrammingTestNamespace
			copy.ResourceVersion = ""
			require.NoError(t, cell.Create(ctx, copy))
			require.NoError(t, cell.Create(ctx, &galactic.NetworkGateway{ObjectMeta: metav1.ObjectMeta{Name: "gateway", Generation: 1}, Status: galactic.NetworkGatewayStatus{ObservedGeneration: 1, BackendHealthFiltering: true, Capabilities: []string{galactic.CapabilityTerminalAddressRetirement, galactic.CapabilitySharedAddressListeners}}}))
			reconcileNLBProgramming(t, adapter, copy)
			var anchor galactic.NetworkServiceAddress
			anchorKey := client.ObjectKey{Namespace: copy.Namespace, Name: networkLoadBalancerAddressName(copy.Spec.AddressGroup.UID)}
			require.NoError(t, cell.Get(ctx, anchorKey, &anchor))
			anchor.Finalizers = []string{"test.galactic/withdrawal"}
			require.NoError(t, cell.Update(ctx, &anchor))
			_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, key)
			require.NoError(t, err)
			intents, err = publishedListenerIntents(ctx, hub, key)
			require.NoError(t, err)
			var retired *api.NetworkLoadBalancerCellIntent
			for i := range intents {
				if intents[i].Spec.Allocation == nil && intents[i].Spec.Source.AddressRequest == "regional" && intents[i].Spec.Location == "iad" {
					retired = &intents[i]
				}
			}
			require.NotNil(t, retired)
			require.Equal(t, api.NetworkLoadBalancerCellIntentWithdrawn, retired.Spec.State)
			require.NoError(t, cell.Get(ctx, client.ObjectKeyFromObject(copy), copy))
			copy.Spec = retired.Spec
			require.NoError(t, cell.Update(ctx, copy))
			reconcileNLBProgramming(t, adapter, copy)
			_, err = s.control.reconcile(ctx, s.client, key)
			require.NoError(t, err)
			require.NoError(t, s.ipam.Get(ctx, client.ObjectKey{Namespace: old.ClaimRef.Namespace, Name: old.ClaimRef.Name}, &ipamv1alpha1.IPClaim{}))
			require.NoError(t, cell.Get(ctx, anchorKey, &anchor))
			require.False(t, anchor.DeletionTimestamp.IsZero())
			anchor.Finalizers = nil
			require.NoError(t, cell.Update(ctx, &anchor))
			reconcileNLBProgramming(t, adapter, copy)
			require.NoError(t, cell.Get(ctx, client.ObjectKeyFromObject(copy), copy))
			require.True(t, intentWithdrawalAcknowledged(copy))
			retired.Status = copy.Status
			require.NoError(t, hub.Status().Update(ctx, retired))
			completeCellRetirements(t, hub, cell, adapter, testProject, key)
			_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, key)
			require.NoError(t, err)
			_, err = s.control.reconcile(ctx, s.client, key)
			require.NoError(t, err)
			require.True(t, apierrors.IsNotFound(s.ipam.Get(ctx, client.ObjectKey{Namespace: old.ClaimRef.Namespace, Name: old.ClaimRef.Name}, &ipamv1alpha1.IPClaim{})))
		})
	}
}

func TestNLBPublicationRetiresWithoutRoutesAndRejectsStaleAcknowledgment(t *testing.T) {
	s, hub, publisher := publicationScenario(t)
	ctx, key := s.ctx, client.ObjectKeyFromObject(s.lb)
	var route api.NetworkLoadBalancerRoute
	require.NoError(t, s.client.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: "web"}, &route))
	require.NoError(t, s.client.Delete(ctx, &route))
	_, err := publisher.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	var parent api.NetworkLoadBalancer
	require.NoError(t, s.client.Get(ctx, key, &parent))
	for _, endpoint := range parent.Status.Endpoints {
		if endpoint.RequestName == "regional" {
			require.True(t, endpoint.Addresses[0].WithdrawalPending)
		}
	}
	require.NoError(t, s.client.Delete(ctx, &parent))
	_, err = s.control.reconcile(ctx, s.client, key)
	require.NoError(t, err)
	require.NoError(t, s.client.Get(ctx, key, &parent))
	stale := parent.DeepCopy()
	parent.Generation++
	require.NoError(t, s.client.Update(ctx, &parent))
	require.True(t, apierrors.IsConflict(acknowledgeRetiredAllocations(ctx, s.client, stale, retirementReceipts(stale))))
	_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	cell, adapter, placeholder := nlbProgrammingFixture(t)
	require.NoError(t, cell.Delete(ctx, placeholder))
	adapter.resolveLocation = func(context.Context) (string, error) { return "iad", nil }
	completeCellRetirements(t, hub, cell, adapter, testProject, key)
	_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	require.NoError(t, s.client.Get(ctx, key, &parent))
	for _, endpoint := range parent.Status.Endpoints {
		for _, address := range endpoint.Addresses {
			require.False(t, address.WithdrawalPending)
		}
	}
	_, err = s.control.reconcile(ctx, s.client, key)
	require.NoError(t, err)
	require.Empty(t, s.claims())
	intents, err := publishedListenerIntents(ctx, hub, key)
	require.NoError(t, err)
	require.Empty(t, intents)
}

func TestNLBPublicationParentFinalizerProtectsPausedSibling(t *testing.T) {
	s, hub, publisher := publicationScenario(t)
	ctx, key := s.ctx, client.ObjectKeyFromObject(s.lb)
	_, err := publisher.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	intents, err := publishedListenerIntents(ctx, hub, key)
	require.NoError(t, err)
	require.Len(t, intents, 1)
	sibling := intents[0].DeepCopy()
	sibling.Name += "-sibling"
	sibling.ResourceVersion = ""
	sibling.Spec.Source.ParentName = "sibling"
	sibling.Spec.Source.ParentUID = "sibling-uid"
	var route api.NetworkLoadBalancerRoute
	routeKey := client.ObjectKey{Namespace: key.Namespace, Name: "web"}
	require.NoError(t, s.client.Get(ctx, routeKey, &route))
	siblingFinalizer := parentRouteIntentFinalizer("sibling")
	route.Finalizers = append(route.Finalizers, siblingFinalizer, nlbIntentFinalizer)
	route.Annotations[siblingFinalizer] = "sibling"
	require.NoError(t, s.client.Update(ctx, &route))
	paused, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	blocking := &pausedIntentClient{Client: hub, beforeCreate: func() { close(paused); <-resume }}
	siblingKey := client.ObjectKey{Namespace: key.Namespace, Name: "sibling"}
	go func() {
		_, err := reconcileParentHubIntents(ctx, blocking, testProject, siblingKey, []api.NetworkLoadBalancerCellIntent{*sibling})
		finished <- err
	}()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("sibling publisher did not pause")
	}
	require.NoError(t, s.client.Delete(ctx, &route))
	_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	intents, err = publishedListenerIntents(ctx, hub, key)
	require.NoError(t, err)
	require.Len(t, intents, 1)
	reportIntent(&intents[0], metav1.ConditionFalse, metav1.ConditionFalse)
	require.NoError(t, hub.Status().Update(ctx, &intents[0]))
	_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	require.NoError(t, s.client.Get(ctx, routeKey, &route))
	require.Contains(t, route.Finalizers, siblingFinalizer)
	require.Contains(t, route.Finalizers, nlbIntentFinalizer)
	require.NotContains(t, route.Finalizers, parentRouteIntentFinalizer(key.Name))
	close(resume)
	require.NoError(t, <-finished)
	requests := loadBalancersForIntentEvent(ctx, s.client, &route)
	found := false
	for _, request := range requests {
		found = found || request.NamespacedName == siblingKey
	}
	require.True(t, found, "durable annotation must recover the absent sibling after restart")
	_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, siblingKey)
	require.NoError(t, err)
	intents, err = publishedListenerIntents(ctx, hub, siblingKey)
	require.NoError(t, err)
	require.Len(t, intents, 1)
	require.Equal(t, api.NetworkLoadBalancerCellIntentWithdrawn, intents[0].Spec.State)
	require.NoError(t, s.client.Get(ctx, routeKey, &route))
	reportIntent(&intents[0], metav1.ConditionFalse, metav1.ConditionFalse)
	require.NoError(t, hub.Status().Update(ctx, &intents[0]))
	_, err = publisher.reconcileParentIntents(ctx, testProject, s.client, siblingKey)
	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(s.client.Get(ctx, routeKey, &route)))
}

func TestNLBPublicationRetriesMissingNamespaceMapping(t *testing.T) {
	s, _, publisher := publicationScenario(t)
	require.NoError(t, s.client.Delete(s.ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: s.lb.Namespace}}))
	result, err := publisher.reconcileParentIntents(s.ctx, testProject, s.client, client.ObjectKeyFromObject(s.lb))
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
}
