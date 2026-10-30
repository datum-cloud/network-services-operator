// SPDX-License-Identifier: AGPL-3.0-only
package controller

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	galactic "go.datum.net/galactic/api/network/v1alpha1"
	api "go.datum.net/network-services-operator/api/v1alpha"
	ipam "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func retirementReceipts(parent *api.NetworkLoadBalancer) []api.NetworkLoadBalancerCellIntent {
	var intents []api.NetworkLoadBalancerCellIntent
	for i := range parent.Status.Endpoints {
		endpoint := &parent.Status.Endpoints[i]
		for j := range endpoint.Addresses {
			address := &endpoint.Addresses[j]
			if !address.Coordinated || !address.Retiring || address.ClaimRef == nil {
				continue
			}
			intent := allocationAuthorityIntent(testProject, "retirement-test", parent, endpoint, address)
			intent.Generation = 1
			reportIntent(intent, metav1.ConditionFalse, metav1.ConditionFalse)
			apimeta.SetStatusCondition(&intent.Status.Conditions, metav1.Condition{Type: api.NetworkLoadBalancerCellIntentAllocationRetired, Status: metav1.ConditionTrue, Reason: "Retired", ObservedGeneration: 1})
			intents = append(intents, *intent)
		}
	}
	return intents
}

func completeCellRetirements(t *testing.T, hub, cell client.Client, adapter *NetworkLoadBalancerCellProgrammingReconciler, project string, key client.ObjectKey) {
	t.Helper()
	ctx := context.Background()
	adapter.HubCluster = &fakeCluster{cl: hub, reader: hub}
	intents, err := parentPublishedIntents(ctx, hub, project, key)
	require.NoError(t, err)
	for i := range intents {
		source := &intents[i]
		if source.Spec.Allocation == nil || !source.Spec.Allocation.Retired {
			continue
		}
		copy := source.DeepCopy()
		copy.Namespace = nlbProgrammingTestNamespace
		copy.ResourceVersion = ""
		require.NoError(t, cell.Create(ctx, copy))
		reconcileNLBProgramming(t, adapter, copy)
		require.NoError(t, cell.Get(ctx, client.ObjectKeyFromObject(copy), copy))
		require.False(t, intentWithdrawalAcknowledged(copy), "absence of serving services is not a retirement receipt")
		var anchor galactic.NetworkServiceAddress
		anchorKey := client.ObjectKey{Namespace: copy.Namespace, Name: networkLoadBalancerAddressName(copy.Spec.Allocation.UID)}
		require.NoError(t, cell.Get(ctx, anchorKey, &anchor))
		require.True(t, anchor.Spec.Retired)
		anchor.Status.ObservedGeneration = anchor.Generation
		apimeta.SetStatusCondition(&anchor.Status.Conditions, metav1.Condition{Type: "Retired", Status: metav1.ConditionTrue, Reason: "Retired", ObservedGeneration: anchor.Generation})
		require.NoError(t, cell.Update(ctx, &anchor))
		reconcileNLBProgramming(t, adapter, copy)
		require.NoError(t, cell.Get(ctx, client.ObjectKeyFromObject(copy), copy))
		require.True(t, intentWithdrawalAcknowledged(copy))

	}
}

func TestNLBPublisherHandoffRetirementFencesDelayedPublication(t *testing.T) {
	s, hub, first := publicationScenario(t)
	ctx, key := s.ctx, client.ObjectKeyFromObject(s.lb)
	lb := s.reconcile()
	var old api.NetworkLoadBalancerAddressStatus
	for _, ep := range lb.Status.Endpoints {
		if ep.RequestName == "regional" {
			old = ep.Addresses[0]
		}
	}
	paused, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	blocking := &pausedIntentClient{Client: hub, beforeCreate: func() { close(paused); <-resume }}
	first.HubCluster = &fakeCluster{cl: blocking, reader: hub}
	go func() { _, err := first.reconcileParentIntents(ctx, testProject, s.client, key); finished <- err }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not reach Create")
	}
	require.NoError(t, s.client.Get(ctx, key, &lb))
	require.NoError(t, s.client.Delete(ctx, &lb))
	_, err := s.control.reconcile(ctx, s.client, key)
	require.NoError(t, err)
	replacement := &NetworkLoadBalancerCellIntentReconciler{HubCluster: &fakeCluster{cl: hub, reader: hub}}
	_, err = replacement.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	_, err = s.control.reconcile(ctx, s.client, key)
	require.NoError(t, err)
	claimKey := client.ObjectKey{Namespace: old.ClaimRef.Namespace, Name: old.ClaimRef.Name}
	require.True(t, apierrors.IsNotFound(s.ipam.Get(ctx, claimKey, &ipam.IPClaim{})), "a terminal authority with no registered cells cannot authorize delayed materialization")
	cell, adapter, placeholder := nlbProgrammingFixture(t)
	require.NoError(t, cell.Delete(ctx, placeholder))
	adapter.resolveLocation = func(context.Context) (string, error) { return "iad", nil }
	completeCellRetirements(t, hub, cell, adapter, testProject, key)
	_, err = replacement.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	_, err = s.control.reconcile(ctx, s.client, key)
	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(s.ipam.Get(ctx, claimKey, &ipam.IPClaim{})))
	close(resume)
	require.NoError(t, <-finished)
	intents, err := parentPublishedIntents(ctx, hub, testProject, key)
	require.NoError(t, err)
	var late *api.NetworkLoadBalancerCellIntent
	for i := range intents {
		if cellIntentRequestsServing(&intents[i]) {
			late = intents[i].DeepCopy()
		}
	}
	require.NotNil(t, late, "exercise a late committed hub publication")
	late.Namespace = nlbProgrammingTestNamespace
	late.ResourceVersion = ""
	require.NoError(t, cell.Create(ctx, late))
	service := &galactic.NetworkService{ObjectMeta: metav1.ObjectMeta{Namespace: late.Namespace, Name: late.Name}, Spec: galactic.NetworkServiceSpec{VIPAddresses: []string{late.Spec.VIP}, VPCRef: "1Z", AddressGroup: galacticAddressGroup(late.Spec.AddressGroup)}}
	require.ErrorContains(t, adapter.ensureAddress(ctx, service), "permanently retired")
	done, err := adapter.cleanupAddresses(ctx, late.Namespace)
	require.NoError(t, err)
	require.True(t, done)
	anchorKey := client.ObjectKey{Namespace: late.Namespace, Name: networkLoadBalancerAddressName(late.Spec.AddressGroup.UID)}
	var anchor galactic.NetworkServiceAddress
	require.NoError(t, cell.Get(ctx, anchorKey, &anchor))
	require.True(t, anchor.Spec.Retired)
	_, err = replacement.reconcileParentIntents(ctx, testProject, s.client, key)
	require.NoError(t, err)
	intents, err = parentPublishedIntents(ctx, hub, testProject, key)
	require.NoError(t, err)
	var sentinel *api.NetworkLoadBalancerCellIntent
	for i := range intents {
		if intents[i].Spec.Allocation != nil {
			sentinel = &intents[i]
		}
	}
	require.NotNil(t, sentinel)
	require.NoError(t, deleteAcknowledgedIntent(ctx, hub, sentinel))
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(sentinel), &api.NetworkLoadBalancerCellIntent{}))
	replacementAdapter := &NetworkLoadBalancerCellProgrammingReconciler{Client: cell, APIReader: cell, Scheme: cell.Scheme(), HubCluster: &fakeCluster{cl: hub, reader: hub}}
	require.ErrorContains(t, replacementAdapter.ensureAddress(ctx, service), "permanently retired")
	fresh := late.DeepCopy()
	fresh.Name += "-fresh"
	fresh.ResourceVersion = ""
	fresh.UID = ""
	fresh.Spec.AddressGroup.UID = "replacement-allocation"
	require.NoError(t, cell.Create(ctx, fresh))
	createTestAllocationAuthority(t, hub, fresh)
	service.Name = fresh.Name
	service.Spec.AddressGroup = galacticAddressGroup(fresh.Spec.AddressGroup)
	require.NoError(t, replacementAdapter.ensureAddress(ctx, service), "a distinct allocation identity may legitimately reuse the VIP")
}

type pausedServiceClient struct {
	client.Client
	beforeCreate func()
}

func (c *pausedServiceClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*galactic.NetworkService); ok && c.beforeCreate != nil {
		c.beforeCreate()
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestNLBRetirementFencesInFlightCellMaterialization(t *testing.T) {
	ctx := context.Background()
	cl, first, intent := rangeProgrammingFixture(t)
	require.NoError(t, cl.Create(ctx, &galactic.NetworkGateway{ObjectMeta: metav1.ObjectMeta{Name: "gateway", Generation: 1}, Status: galactic.NetworkGatewayStatus{ObservedGeneration: 1, BackendHealthFiltering: true, Capabilities: []string{galactic.CapabilitySharedAddressListeners, galactic.CapabilityUDPPortRanges, galactic.CapabilityTerminalAddressRetirement}}}))
	second := *first
	paused, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	first.Client = &pausedServiceClient{Client: cl, beforeCreate: func() { close(paused); <-resume }}
	first.APIReader = cl
	go func() {
		_, err := first.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(intent)})
		finished <- err
	}()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("cell publisher did not pause")
	}
	retirement := &api.NetworkLoadBalancerCellIntent{}
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}, retirement))
	retirement.Spec.Allocation.Retired = true
	require.NoError(t, cl.Update(ctx, retirement))
	reconcileNLBProgramming(t, &second, retirement)
	var anchor galactic.NetworkServiceAddress
	anchorKey := client.ObjectKey{Namespace: intent.Namespace, Name: networkLoadBalancerAddressName(intent.Spec.AddressGroup.UID)}
	require.NoError(t, cl.Get(ctx, anchorKey, &anchor))
	require.True(t, anchor.Spec.Retired)
	anchor.Status.ObservedGeneration = anchor.Generation
	apimeta.SetStatusCondition(&anchor.Status.Conditions, metav1.Condition{Type: "Retired", Status: metav1.ConditionTrue, Reason: "Retired", ObservedGeneration: anchor.Generation})
	require.NoError(t, cl.Update(ctx, &anchor))
	reconcileNLBProgramming(t, &second, retirement)
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(retirement), retirement))
	require.True(t, intentWithdrawalAcknowledged(retirement))
	close(resume)
	require.NoError(t, <-finished)
	var late galactic.NetworkService
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(intent), &late), "the serving create was already in flight")
	require.NoError(t, cl.Get(ctx, anchorKey, &anchor))
	require.True(t, anchor.Spec.Retired, "late service cannot reopen its immutable authority")
	require.ErrorContains(t, second.ensureAddress(ctx, &late), "permanently retired")
	reconcileNLBProgramming(t, &second, intent)
	require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(intent), &late)))
	require.NoError(t, cl.Get(ctx, anchorKey, &anchor))
	require.True(t, anchor.Spec.Retired)
}

func TestNLBReselectedRetiredAllocationRequiresNewClaim(t *testing.T) {
	s, _, _ := publicationScenario(t)
	lb := s.reconcile()
	original := lb.Spec.DeepCopy()
	var old api.NetworkLoadBalancerAddressStatus
	for _, endpoint := range lb.Status.Endpoints {
		if endpoint.RequestName == "regional" {
			old = endpoint.Addresses[0]
		}
	}
	lb.Spec.Exposure.Addresses = lb.Spec.Exposure.Addresses[1:]
	for i := range lb.Spec.Listeners {
		lb.Spec.Listeners[i].AddressRefs = []string{"edge"}
	}
	lb.Generation++
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	lb.Spec = *original
	lb.Generation++
	require.NoError(t, s.client.Update(s.ctx, &lb))
	lb = s.reconcile()
	require.NoError(t, acknowledgeRetiredAllocations(s.ctx, s.client, &lb, retirementReceipts(&lb)))
	lb = s.reconcile()
	key := client.ObjectKey{Namespace: old.ClaimRef.Namespace, Name: old.ClaimRef.Name}
	require.True(t, apierrors.IsNotFound(s.ipam.Get(s.ctx, key, &ipam.IPClaim{})), "a retired claim cannot be rearmed")
	lb = s.reconcile()
	var replacement ipam.IPClaim
	require.NoError(t, s.ipam.Get(s.ctx, key, &replacement))
	replacement.UID = "replacement-claim"
	require.NoError(t, s.ipam.Update(s.ctx, &replacement))
	replacement.Status.Address = old.Value
	require.NoError(t, s.ipam.Status().Update(s.ctx, &replacement))
	lb = s.reconcile()
	found := false
	for _, endpoint := range lb.Status.Endpoints {
		if endpoint.RequestName == "regional" && endpoint.Location == "iad" {
			found = true
			address := endpoint.Addresses[0]
			require.Equal(t, "replacement-claim", address.ClaimRef.UID)
			require.False(t, address.Retiring)
			require.True(t, address.WithdrawalPending)
			require.True(t, allocationPublicationAllowed(&lb, "regional", address))
		}
	}
	require.True(t, found)
}

type unavailableNLBHubReader struct{ client.Reader }

func (r unavailableNLBHubReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return fmt.Errorf("hub is unavailable")
}

func TestNLBRejectsStaleFirstDeliveryAfterHubRetirement(t *testing.T) {
	ctx := context.Background()
	cell, adapter, intent := rangeProgrammingFixture(t)
	backends, _, err := adapter.verifiedBackends(ctx, intent)
	require.NoError(t, err)
	service, reason := desiredNetworkLoadBalancerService(intent, backends, adapter.IPv4VIPs)
	require.Empty(t, reason)
	hub := fake.NewClientBuilder().WithScheme(cell.Scheme()).Build()
	adapter.HubCluster = &fakeCluster{cl: hub, reader: unavailableNLBHubReader{Reader: hub}}
	require.ErrorContains(t, adapter.ensureAddress(ctx, service), "hub is unavailable")
	adapter.HubCluster = &fakeCluster{cl: hub, reader: hub}
	require.ErrorContains(t, adapter.ensureAddress(ctx, service), "not found")
	marker := &api.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}, Spec: api.NetworkLoadBalancerCellIntentSpec{Allocation: &api.NetworkLoadBalancerAllocationAuthority{UID: intent.Spec.AddressGroup.UID, ClaimUID: "claim", Retired: true}, VIP: intent.Spec.VIP}}
	require.NoError(t, hub.Create(ctx, marker))
	require.ErrorContains(t, adapter.ensureAddress(ctx, service), "permanently retired in the federation hub")
	var anchors galactic.NetworkServiceAddressList
	require.NoError(t, cell.List(ctx, &anchors))
	require.Empty(t, anchors.Items, "no local retirement replica was needed to deny the stale first delivery")
}

func TestNLBRetirementDoesNotDependOnLocationResolution(t *testing.T) {
	ctx := context.Background()
	cell, adapter, intent := rangeProgrammingFixture(t)
	intent.Spec.Allocation = &api.NetworkLoadBalancerAllocationAuthority{UID: "retired-elsewhere", ClaimUID: "claim", Retired: true}
	intent.Spec.AddressGroup = nil
	intent.Spec.ListenerPortRange = nil
	intent.Spec.Protocol = ""
	intent.Spec.Backends = nil
	intent.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	require.NoError(t, cell.Update(ctx, intent))
	adapter.resolveLocation = func(context.Context) (string, error) { return "", fmt.Errorf("location not discovered") }
	reconcileNLBProgramming(t, adapter, intent)
	var anchor galactic.NetworkServiceAddress
	require.NoError(t, cell.Get(ctx, client.ObjectKey{Namespace: intent.Namespace, Name: networkLoadBalancerAddressName("retired-elsewhere")}, &anchor))
	require.True(t, anchor.Spec.Retired)
}

func publishedListenerIntents(ctx context.Context, hub client.Client, key client.ObjectKey) ([]api.NetworkLoadBalancerCellIntent, error) {
	intents, err := parentPublishedIntents(ctx, hub, testProject, key)
	return slices.DeleteFunc(intents, func(intent api.NetworkLoadBalancerCellIntent) bool { return intent.Spec.Allocation != nil }), err
}

func createTestAllocationAuthority(t *testing.T, hub client.Client, intent *api.NetworkLoadBalancerCellIntent) {
	t.Helper()
	authority := &api.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}, Spec: api.NetworkLoadBalancerCellIntentSpec{
		Allocation: &api.NetworkLoadBalancerAllocationAuthority{UID: intent.Spec.AddressGroup.UID, ClaimUID: "claim"}, Source: intent.Spec.Source, Location: intent.Spec.Location, NetworkName: intent.Spec.NetworkName, VIP: intent.Spec.VIP, VIPFamily: intent.Spec.VIPFamily, State: api.NetworkLoadBalancerCellIntentWithdrawn,
	}}
	authority.Spec.Source.RouteName = ""
	authority.Spec.Source.RouteUID = ""
	authority.Spec.Source.ListenerName = ""
	require.NoError(t, hub.Create(context.Background(), authority))
}

type pausedAuthorityPatchClient struct {
	client.Client
	beforePatch func()
}

func (c *pausedAuthorityPatchClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if authority, ok := obj.(*api.NetworkLoadBalancerCellIntent); ok && authority.Spec.Allocation != nil && !authority.Spec.Allocation.Retired && c.beforePatch != nil {
		c.beforePatch()
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestNLBRetirementWinsDelayedCellRegistration(t *testing.T) {
	ctx := context.Background()
	cl, adapter, intent := rangeProgrammingFixture(t)
	backends, _, err := adapter.verifiedBackends(ctx, intent)
	require.NoError(t, err)
	service, reason := desiredNetworkLoadBalancerService(intent, backends, adapter.IPv4VIPs)
	require.Empty(t, reason)
	paused, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	writer := &pausedAuthorityPatchClient{Client: cl, beforePatch: func() { close(paused); <-resume }}
	adapter.HubCluster = &fakeCluster{cl: writer, reader: cl}
	go func() { finished <- adapter.ensureAddress(ctx, service) }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not pause")
	}
	var authority api.NetworkLoadBalancerCellIntent
	key := client.ObjectKey{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}
	require.NoError(t, cl.Get(ctx, key, &authority))
	require.Empty(t, authority.Spec.Allocation.Participants)
	authority.Spec.Allocation.Retired = true
	require.NoError(t, cl.Update(ctx, &authority))
	close(resume)
	require.True(t, apierrors.IsConflict(<-finished))
	require.NoError(t, cl.Get(ctx, key, &authority))
	require.Empty(t, authority.Spec.Allocation.Participants)
	require.True(t, allocationAuthorityRetired(&authority))
	var anchors galactic.NetworkServiceAddressList
	require.NoError(t, cl.List(ctx, &anchors))
	require.Empty(t, anchors.Items)
}

func TestNLBRegisteredCellsRequireExactTerminalReceipts(t *testing.T) {
	ctx := context.Background()
	cl, adapter, intent := rangeProgrammingFixture(t)
	require.NoError(t, adapter.registerAllocationParticipant(ctx, intent.Namespace, intent.Spec.AddressGroup.UID, intent.Spec.VIP))
	var authority api.NetworkLoadBalancerCellIntent
	key := client.ObjectKey{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}
	require.NoError(t, cl.Get(ctx, key, &authority))
	require.Equal(t, []string{"cell-uid"}, authority.Spec.Allocation.Participants)
	stale := authority.DeepCopy()
	authority.Spec.Allocation.Retired = true
	authority.Generation = 4
	require.NoError(t, cl.Update(ctx, &authority))
	require.False(t, allocationAuthorityRetired(&authority))
	authority.Status.AllocationRetirements = []api.NetworkLoadBalancerAllocationRetirementReceipt{{CellUID: "cell-uid", ObservedGeneration: 3}, {CellUID: "other-cell", ObservedGeneration: 4}}
	require.False(t, allocationAuthorityRetired(&authority), "an old or foreign receipt cannot discharge the registered cell")
	require.ErrorContains(t, adapter.acknowledgeHubRetirement(ctx, cl, stale), "changed during retirement")
	require.NoError(t, adapter.acknowledgeHubRetirement(ctx, cl, &authority))
	require.NoError(t, cl.Get(ctx, key, &authority))
	require.True(t, allocationAuthorityRetired(&authority))
}

type pausedAuthorityStatusClient struct {
	client.Client
	beforePatch func()
}

func (c *pausedAuthorityStatusClient) Status() client.SubResourceWriter {
	return &pausedAuthorityStatusWriter{SubResourceWriter: c.Client.Status(), beforePatch: c.beforePatch}
}

type pausedAuthorityStatusWriter struct {
	client.SubResourceWriter
	beforePatch func()
}

func (w *pausedAuthorityStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	w.beforePatch()
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func TestNLBConcurrentRetirementReceiptsPreserveEveryCell(t *testing.T) {
	ctx := context.Background()
	cl, adapter, intent := rangeProgrammingFixture(t)
	require.NoError(t, adapter.registerAllocationParticipant(ctx, intent.Namespace, intent.Spec.AddressGroup.UID, intent.Spec.VIP))
	var authority api.NetworkLoadBalancerCellIntent
	key := client.ObjectKey{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}
	require.NoError(t, cl.Get(ctx, key, &authority))
	authority.Spec.Allocation.Participants = append(authority.Spec.Allocation.Participants, "peer-cell")
	authority.Spec.Allocation.Retired = true
	authority.Generation = 4
	require.NoError(t, cl.Update(ctx, &authority))
	paused, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	writer := &pausedAuthorityStatusClient{Client: cl, beforePatch: func() { close(paused); <-resume }}
	go func() { finished <- adapter.acknowledgeHubRetirement(ctx, writer, &authority) }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("receipt did not pause")
	}
	var peer api.NetworkLoadBalancerCellIntent
	require.NoError(t, cl.Get(ctx, key, &peer))
	peer.Status.AllocationRetirements = []api.NetworkLoadBalancerAllocationRetirementReceipt{{CellUID: "peer-cell", ObservedGeneration: 4}}
	require.NoError(t, cl.Status().Update(ctx, &peer))
	close(resume)
	require.True(t, apierrors.IsConflict(<-finished))
	require.NoError(t, cl.Get(ctx, key, &peer))
	require.Len(t, peer.Status.AllocationRetirements, 1)
	require.False(t, allocationAuthorityRetired(&peer))
	require.NoError(t, adapter.acknowledgeHubRetirement(ctx, cl, &authority))
	require.NoError(t, cl.Get(ctx, key, &peer))
	require.Len(t, peer.Status.AllocationRetirements, 2)
	require.True(t, allocationAuthorityRetired(&peer))
}

func TestNLBStaleActiveCellCopyUsesTerminalHubAuthority(t *testing.T) {
	ctx := context.Background()
	cell, adapter, intent := rangeProgrammingFixture(t)
	key := client.ObjectKey{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}
	var local api.NetworkLoadBalancerCellIntent
	require.NoError(t, cell.Get(ctx, key, &local))
	require.False(t, local.Spec.Allocation.Retired)
	hubAuthority := local.DeepCopy()
	hubAuthority.ResourceVersion = ""
	hubAuthority.Spec.Allocation.Participants = []string{"cell-uid"}
	hubAuthority.Spec.Allocation.Retired = true
	hubAuthority.Generation = 4
	hub := fake.NewClientBuilder().WithScheme(cell.Scheme()).WithStatusSubresource(&api.NetworkLoadBalancerCellIntent{}).WithObjects(hubAuthority).Build()
	adapter.HubCluster = &fakeCluster{cl: hub, reader: hub}
	reconcileNLBProgramming(t, adapter, &local)
	var anchor galactic.NetworkServiceAddress
	anchorKey := client.ObjectKey{Namespace: intent.Namespace, Name: networkLoadBalancerAddressName(intent.Spec.AddressGroup.UID)}
	require.NoError(t, cell.Get(ctx, anchorKey, &anchor))
	require.True(t, anchor.Spec.Retired)
	require.NoError(t, hub.Get(ctx, key, hubAuthority))
	require.False(t, allocationAuthorityRetired(hubAuthority))
	anchor.Status.ObservedGeneration = anchor.Generation
	apimeta.SetStatusCondition(&anchor.Status.Conditions, metav1.Condition{Type: galactic.ConditionTypeAddressRetired, Status: metav1.ConditionTrue, Reason: "Retired", ObservedGeneration: anchor.Generation})
	require.NoError(t, cell.Update(ctx, &anchor))
	reconcileNLBProgramming(t, adapter, &local)
	require.NoError(t, hub.Get(ctx, key, hubAuthority))
	require.True(t, allocationAuthorityRetired(hubAuthority))
	require.EqualValues(t, 4, hubAuthority.Status.AllocationRetirements[0].ObservedGeneration)
	require.NoError(t, cell.Get(ctx, key, &local))
	require.False(t, local.Spec.Allocation.Retired, "the stale member spec never overrode terminal hub authority")
}

func TestNLBAllocationAuthorityDoesNotReportListenerReadiness(t *testing.T) {
	lb, intent := dataPlaneFixture()
	authority := intent.DeepCopy()
	authority.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	authority.Spec.Allocation = &api.NetworkLoadBalancerAllocationAuthority{UID: "allocation", ClaimUID: "claim"}
	authority.Status = api.NetworkLoadBalancerCellIntentStatus{}
	intent.Spec.State = api.NetworkLoadBalancerCellIntentWithdrawn
	reportIntent(&intent, metav1.ConditionFalse, metav1.ConditionFalse)
	_, announced, known := addressDataPlane([]api.NetworkLoadBalancerCellIntent{*authority, intent}, lb.Status.Endpoints[0].RequestName, lb.Status.Endpoints[0].Location, lb.Status.Endpoints[0].Addresses[0].Value)
	require.True(t, known)
	require.Equal(t, metav1.ConditionFalse, announced.Status)
}

func TestNLBAllocationValidationWireBarrier(t *testing.T) {
	ctx := context.Background()
	cell, adapter, intent := rangeProgrammingFixture(t)
	require.Equal(t, api.NetworkLoadBalancerCellIntentValidateAllocation, intent.Spec.State)
	require.True(t, cellIntentRequestsServing(intent))
	require.NotEqual(t, api.NetworkLoadBalancerCellIntentValidate, intent.Spec.State, "older adapters withdraw every state other than Validate")
	intent.Spec.State = api.NetworkLoadBalancerCellIntentValidate
	require.NoError(t, cell.Update(ctx, intent))
	reconcileNLBProgramming(t, adapter, intent)
	require.False(t, cellIntentRequestsServing(intent))
	var service galactic.NetworkService
	require.True(t, apierrors.IsNotFound(cell.Get(ctx, client.ObjectKeyFromObject(intent), &service)))
	var authority api.NetworkLoadBalancerCellIntent
	require.NoError(t, cell.Get(ctx, client.ObjectKey{Namespace: intent.Namespace, Name: "nlb-allocation-" + intent.Spec.AddressGroup.UID}, &authority))
	require.Empty(t, authority.Spec.Allocation.Participants)
	intent.Spec.State = api.NetworkLoadBalancerCellIntentValidateAllocation
	intent.Spec.AddressGroup = nil
	require.False(t, cellIntentRequestsServing(intent))
}
