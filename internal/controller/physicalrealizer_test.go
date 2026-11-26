// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/physicalrealizer"
)

const realizedPort = "Ethernet1/12"

type realizedPortRun struct {
	s        *scenario
	sw       *physicalrealizer.SimulatedSwitch
	realizer *physicalrealizer.Reconciler
	projects map[string]string
	applies  int
	onApply  func()
}

func (s *scenario) realizedPort() *realizedPortRun {
	sw := physicalrealizer.NewSimulatedSwitch()
	run := &realizedPortRun{
		s:        s,
		sw:       sw,
		realizer: &physicalrealizer.Reconciler{Client: s.client, APIReader: s.client, Driver: sw},
		projects: map[string]string{s.namespace: testProject},
	}
	sw.BeforeApply(func(_ physicalrealizer.Port, desired []physicalrealizer.Membership) {
		run.applies++
		run.requireSafe("apply", desired)
		if run.onApply != nil {
			run.onApply()
		}
	})
	return run
}

func (r *realizedPortRun) sibling(project string) *scenario {
	other := r.s.sibling(project)
	r.projects[other.namespace] = project
	return other
}

func (r *realizedPortRun) port() physicalrealizer.Port {
	return physicalrealizer.Port{Switch: r.s.switchName, Name: realizedPort}
}

func (r *realizedPortRun) realize() error {
	_, err := r.realizer.Reconcile(r.s.ctx, ctrl.Request{NamespacedName: client.ObjectKey{
		Name: networkingv1alpha.SwitchPortHoldName(r.s.switchName, realizedPort),
	}})
	return err
}

func (r *realizedPortRun) mustRealize() {
	r.s.t.Helper()
	require.NoError(r.s.t, r.realize())
	r.requireConsistent()
}

func (r *realizedPortRun) carried() []physicalrealizer.Membership {
	return r.sw.Carried(r.port())
}

func (r *realizedPortRun) changes() int {
	return len(r.sw.History())
}

func networkOf(s *scenario) types.NamespacedName {
	return types.NamespacedName{Namespace: s.namespace, Name: "default"}
}

func (r *realizedPortRun) requireConsistent() {
	r.s.t.Helper()
	var claims networkingv1alpha.NetworkInterfaceClaimList
	require.NoError(r.s.t, r.s.client.List(r.s.ctx, &claims))
	carried := r.carried()
	for _, claim := range claims.Items {
		if claim.Spec.AttachmentPoint == nil || claim.Spec.AttachmentPoint.Switch != r.s.switchName {
			continue
		}
		if !apimeta.IsStatusConditionTrue(claim.Status.Conditions, networkingv1alpha.NetworkInterfaceClaimProgrammed) {
			continue
		}
		network := types.NamespacedName{Namespace: claim.Namespace, Name: claim.Spec.Network.Name}
		require.Truef(r.s.t, carries(carried, network),
			"claim %s/%s reports Programmed while the port carries %v", claim.Namespace, claim.Name, carried)
	}
}

func carries(carried []physicalrealizer.Membership, network types.NamespacedName) bool {
	for _, m := range carried {
		if m.Network == network {
			return true
		}
	}
	return false
}

func (r *realizedPortRun) requireSafe(what string, carried []physicalrealizer.Membership) {
	r.s.t.Helper()
	projects := map[string]bool{}
	for _, m := range carried {
		project, known := r.projects[m.Network.Namespace]
		require.Truef(r.s.t, known, "%s carries %s, which no scenario owns", what, m.Network)
		projects[project] = true
		if project == providerProject {
			require.Lenf(r.s.t, carried, 1, "%s carries the boot network with others: %v", what, carried)
		}
	}
	require.LessOrEqualf(r.s.t, len(projects), 1, "%s carries two projects' networks: %v", what, carried)
}

func (r *realizedPortRun) requireSafeHistory() {
	r.s.t.Helper()
	history := r.sw.History()
	require.GreaterOrEqual(r.s.t, r.applies, len(history), "every change was checked before it was applied")
	for i, change := range history {
		r.requireSafe(fmt.Sprintf("change %d", i), change.Carried)
	}
}

func requireProgrammed(t *testing.T, conditions []metav1.Condition, status metav1.ConditionStatus, reason string) {
	t.Helper()
	programmed := apimeta.FindStatusCondition(conditions, networkingv1alpha.NetworkInterfaceClaimProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, status, programmed.Status, programmed.Message)
	require.Equal(t, reason, programmed.Reason)
}

func requirePrepared(t *testing.T, conditions []metav1.Condition) {
	t.Helper()
	prepared := apimeta.FindStatusCondition(conditions, networkingv1alpha.NetworkInterfaceClaimPrepared)
	require.NotNil(t, prepared)
	require.Equal(t, metav1.ConditionTrue, prepared.Status)
	require.Equal(t, networkingv1alpha.NetworkInterfaceClaimReasonAttachmentPointAccepted, prepared.Reason)
}

func (s *scenario) requireReported(name string, status metav1.ConditionStatus, reason string) *networkingv1alpha.NetworkInterfaceClaim {
	s.t.Helper()
	claim := s.getClaim(name)
	requirePrepared(s.t, claim.Status.Conditions)
	requireProgrammed(s.t, claim.Status.Conditions, status, reason)
	iface, err := s.getInterface(name)
	require.NoError(s.t, err)
	requirePrepared(s.t, iface.Status.Conditions)
	requireProgrammed(s.t, iface.Status.Conditions, status, reason)
	require.Equal(s.t, iface.Status.VLANID, claim.Status.VLANID, "the claim repeats the interface's VLAN")
	return claim
}

func (s *scenario) hasDetachFinalizer(name string) bool {
	s.t.Helper()
	return controllerutil.ContainsFinalizer(s.getClaim(name), networkingv1alpha.NetworkInterfaceClaimDetachFinalizer)
}

func (s *scenario) requireReady(name string) {
	s.t.Helper()
	ready := conditionOf(s.getClaim(name), networkingv1alpha.NetworkInterfaceClaimReady)
	require.NotNil(s.t, ready)
	require.Equal(s.t, metav1.ConditionTrue, ready.Status, ready.Message)
}

func (s *scenario) bindPhysical(name string, purpose networkingv1alpha.NetworkInterfacePurpose) {
	s.t.Helper()
	s.reconcile(s.createClaim(name, s.physicalSpec(realizedPort, purpose)))
	s.requireBound(name)
}

func (r *realizedPortRun) requireCarriedOn(s *scenario, name string) int32 {
	s.t.Helper()
	claim := s.getClaim(name)
	require.NotNil(s.t, claim.Status.VLANID, "a network claim's network arrives tagged")
	network := types.NamespacedName{Namespace: claim.Namespace, Name: claim.Spec.Network.Name}
	for _, m := range r.carried() {
		if m.Network == network {
			require.Equal(s.t, *claim.Status.VLANID, m.VLAN, "the reported VLAN is the one the switch carries")
			return m.VLAN
		}
	}
	require.Failf(s.t, "not carried", "the port does not carry %s: %v", network, r.carried())
	return 0
}

func TestARealizedNetworkClaimIsProgrammed(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	s.bindPhysical("server-data", "")

	run.mustRealize()
	require.Len(t, run.carried(), 1)
	require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationShared, run.carried()[0].Isolation)
	claim := s.requireReported("server-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
	run.requireCarriedOn(s, "server-data")

	s.reconcile(claim)
	s.requireReady("server-data")

	before := run.changes()
	run.mustRealize()
	require.Equal(t, before, run.changes(), "a port that already carries its networks is left alone")
}

func TestASurvivingNetworkKeepsItsVLAN(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	storage := run.sibling(testProject)
	backup := run.sibling(testProject)
	s.bindPhysical("server-data", "")
	storage.bindPhysical("server-storage", "")
	backup.bindPhysical("server-backup", "")
	run.mustRealize()

	vlans := map[string]int32{}
	for name, owner := range map[string]*scenario{"server-data": s, "server-storage": storage, "server-backup": backup} {
		owner.requireReported(name, metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
		vlans[name] = run.requireCarriedOn(owner, name)
	}
	require.Len(t, map[int32]bool{vlans["server-data"]: true, vlans["server-storage"]: true, vlans["server-backup"]: true}, 3)

	require.NoError(t, s.client.Delete(s.ctx, s.getClaim("server-data")))
	s.reconcile(s.getClaim("server-data"))
	run.mustRealize()
	require.Len(t, run.carried(), 2)
	require.Equal(t, vlans["server-storage"], run.requireCarriedOn(storage, "server-storage"), "the first claim leaving re-tags nothing")
	require.Equal(t, vlans["server-backup"], run.requireCarriedOn(backup, "server-backup"))
	storage.requireReported("server-storage", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
}

func TestAHandoverMovesThePortInOneStep(t *testing.T) {
	customer := newScenario(t, true, ipv4Only)
	run := customer.realizedPort()
	provider := run.sibling(providerProject)

	provider.bindPhysical("install-1-boot", networkingv1alpha.NetworkInterfacePurposeBoot)
	customer.bindPhysical("server-data", "")
	run.mustRealize()

	require.Equal(t, []physicalrealizer.Membership{{
		Network: networkOf(provider), Isolation: networkingv1alpha.NetworkInterfaceIsolationPrivate,
	}}, run.carried(), "the boot network alone, untagged and private")
	provider.requireReported("install-1-boot", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
	data := customer.requireReported("server-data", metav1.ConditionFalse,
		networkingv1alpha.NetworkInterfaceClaimReasonBootAttachmentActive)
	require.NotNil(t, data.Status.VLANID, "the server can be configured for its network while it installs")
	installVLAN := *data.Status.VLANID
	customer.reconcile(data)
	data = customer.getClaim("server-data")
	require.Len(t, data.Status.Addresses, 1, "a network claim under a boot claim keeps its addresses")
	require.Equal(t, metav1.ConditionFalse, conditionOf(data, networkingv1alpha.NetworkInterfaceClaimReady).Status)

	require.NoError(t, provider.client.Delete(provider.ctx, provider.getClaim("install-1-boot")))
	provider.reconcile(provider.getClaim("install-1-boot"))
	provider.requireAwaitingDetach("install-1-boot")

	var detachHeldAtApply []bool
	run.onApply = func() {
		detachHeldAtApply = append(detachHeldAtApply, provider.hasDetachFinalizer("install-1-boot"))
		for _, name := range []string{"server-data"} {
			programmed := conditionOf(customer.getClaim(name), networkingv1alpha.NetworkInterfaceClaimProgrammed)
			require.NotEqual(t, metav1.ConditionTrue, programmed.Status, "a claim reports a port between states")
		}
		programmed := conditionOf(provider.getClaim("install-1-boot"), networkingv1alpha.NetworkInterfaceClaimProgrammed)
		require.Equal(t, networkingv1alpha.NetworkInterfaceClaimReasonPortChanging, programmed.Reason,
			"the boot claim is withdrawn before the port moves")
	}
	before := run.changes()
	run.mustRealize()

	require.Equal(t, before+1, run.changes(), "the port moves in one change")
	require.Equal(t, []types.NamespacedName{networkOf(customer)}, networksOf(run.carried()))
	require.Equal(t, networkingv1alpha.NetworkInterfaceIsolationShared, run.carried()[0].Isolation)
	require.Equal(t, installVLAN, run.requireCarriedOn(customer, "server-data"), "the network arrives on the VLAN reported during the install")
	require.Equal(t, []bool{true}, detachHeldAtApply)
	require.False(t, provider.hasDetachFinalizer("install-1-boot"), "the boot claim is detached once read back")
	customer.requireReported("server-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)

	provider.reconcile(provider.getClaim("install-1-boot"))
	require.True(t, provider.claimGone("install-1-boot"))
	require.NotEmpty(t, provider.ipam.deleted()[providerProject], "the boot address is released")
	customer.reconcile(customer.getClaim("server-data"))
	customer.requireReady("server-data")

	run.requireSafeHistory()
}

func TestReleasingACustomerClaimEmptiesThePort(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	s.bindPhysical("server-data", "")
	run.mustRealize()

	require.NoError(t, s.client.Delete(s.ctx, s.getClaim("server-data")))
	s.reconcile(s.getClaim("server-data"))
	s.requireAwaitingDetach("server-data")

	run.mustRealize()
	require.Empty(t, run.carried())
	require.False(t, s.hasDetachFinalizer("server-data"))
	programmed := conditionOf(s.getClaim("server-data"), networkingv1alpha.NetworkInterfaceClaimProgrammed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)

	s.reconcile(s.getClaim("server-data"))
	require.True(t, s.claimGone("server-data"))
	_, err := s.getHold(realizedPort)
	require.True(t, apierrors.IsNotFound(err))

	before := run.changes()
	run.mustRealize()
	require.Equal(t, before, run.changes())
	require.Empty(t, run.carried())
}

func TestAClaimDeletedMidChangeLeavesThePortBeforeItsFinalizer(t *testing.T) {
	customer := newScenario(t, true, ipv4Only)
	run := customer.realizedPort()
	provider := run.sibling(providerProject)
	storage := run.sibling(testProject)

	provider.bindPhysical("install-1-boot", networkingv1alpha.NetworkInterfacePurposeBoot)
	customer.bindPhysical("server-data", "")
	storage.bindPhysical("server-storage", "")
	run.mustRealize()

	require.NoError(t, provider.client.Delete(provider.ctx, provider.getClaim("install-1-boot")))
	provider.reconcile(provider.getClaim("install-1-boot"))

	applies := 0
	var storageDetachHeld []bool
	run.onApply = func() {
		applies++
		storageDetachHeld = append(storageDetachHeld, storage.hasDetachFinalizer("server-storage"))
		if applies == 1 {
			require.NoError(t, storage.client.Delete(storage.ctx, storage.getClaim("server-storage")))
		}
	}
	before := len(run.sw.History())
	require.NoError(t, run.realize(), "one pass changes the port again")

	history := run.sw.History()[before:]
	require.Len(t, history, 2)
	require.ElementsMatch(t, []types.NamespacedName{networkOf(customer), networkOf(storage)}, networksOf(history[0].Carried))
	require.Equal(t, []types.NamespacedName{networkOf(customer)}, networksOf(history[1].Carried))
	require.Equal(t, []bool{true, true}, storageDetachHeld, "the finalizer stays until the second change is read back")
	require.False(t, storage.hasDetachFinalizer("server-storage"))
	require.False(t, provider.hasDetachFinalizer("install-1-boot"))
	customer.requireReported("server-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
	run.requireConsistent()
	run.requireSafeHistory()
}

func networksOf(memberships []physicalrealizer.Membership) []types.NamespacedName {
	networks := make([]types.NamespacedName, 0, len(memberships))
	for _, m := range memberships {
		networks = append(networks, m.Network)
	}
	return networks
}

func (r *realizedPortRun) handoverPending() (customer, provider *scenario) {
	customer = r.s
	provider = r.sibling(providerProject)
	provider.bindPhysical("install-1-boot", networkingv1alpha.NetworkInterfacePurposeBoot)
	customer.bindPhysical("server-data", "")
	r.mustRealize()
	require.NoError(r.s.t, provider.client.Delete(provider.ctx, provider.getClaim("install-1-boot")))
	provider.reconcile(provider.getClaim("install-1-boot"))
	return customer, provider
}

func TestAPartialChangeHoldsFinalizersUntilTheSwitchRecovers(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	customer, provider := run.handoverPending()

	run.sw.PartlyApply(1)
	require.Error(t, run.realize())
	run.requireConsistent()
	require.Empty(t, run.carried(), "the partial change took the boot network off and added nothing")
	require.True(t, provider.hasDetachFinalizer("install-1-boot"), "an unverified change keeps every finalizer")
	requireProgrammed(t, customer.getClaim("server-data").Status.Conditions, metav1.ConditionFalse,
		networkingv1alpha.NetworkInterfaceClaimReasonPortChanging)

	run.sw.FailApplies(2)
	require.Error(t, run.realize())
	require.Error(t, run.realize())
	require.True(t, provider.hasDetachFinalizer("install-1-boot"))
	requireProgrammed(t, customer.getClaim("server-data").Status.Conditions, metav1.ConditionFalse,
		networkingv1alpha.NetworkInterfaceClaimReasonPortChanging)

	run.mustRealize()
	require.Equal(t, []types.NamespacedName{networkOf(customer)}, networksOf(run.carried()))
	require.False(t, provider.hasDetachFinalizer("install-1-boot"))
	customer.requireReported("server-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
	run.requireSafeHistory()
}

func TestASwitchThatCannotBeReadRemovesNoFinalizer(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	s.bindPhysical("server-data", "")
	run.mustRealize()
	require.NoError(t, s.client.Delete(s.ctx, s.getClaim("server-data")))
	s.reconcile(s.getClaim("server-data"))

	run.sw.FailReads(true)
	before := run.changes()
	for range 3 {
		require.ErrorIs(t, run.realize(), physicalrealizer.ErrSwitchUnreachable)
	}
	require.Equal(t, before, run.changes(), "nothing is changed on a port that cannot be read")
	require.True(t, s.hasDetachFinalizer("server-data"))
	requireProgrammed(t, s.getClaim("server-data").Status.Conditions, metav1.ConditionTrue,
		networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)

	run.sw.FailReads(false)
	run.onApply = func() { run.sw.FailReads(true) }
	require.ErrorIs(t, run.realize(), physicalrealizer.ErrSwitchUnreachable)
	require.Empty(t, run.carried(), "the change landed")
	require.True(t, s.hasDetachFinalizer("server-data"), "a change that cannot be read back proves nothing")

	run.onApply = nil
	run.sw.FailReads(false)
	run.mustRealize()
	require.False(t, s.hasDetachFinalizer("server-data"))
}

type failingStatus struct {
	client.Client
	kind client.Object
}

type failingStatusWriter struct {
	client.SubResourceWriter
	kind client.Object
}

var errStatusWrite = errors.New("status write refused")

func (c failingStatus) Status() client.SubResourceWriter {
	return failingStatusWriter{SubResourceWriter: c.Client.Status(), kind: c.kind}
}

func (w failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if sameKind(obj, w.kind) {
		return errStatusWrite
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w failingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if sameKind(obj, w.kind) {
		return errStatusWrite
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func sameKind(a, b client.Object) bool {
	switch b.(type) {
	case *networkingv1alpha.NetworkInterfaceClaim:
		_, ok := a.(*networkingv1alpha.NetworkInterfaceClaim)
		return ok
	case *networkingv1alpha.NetworkInterface:
		_, ok := a.(*networkingv1alpha.NetworkInterface)
		return ok
	}
	return false
}

func TestAFailedWithdrawalLeavesThePortAlone(t *testing.T) {
	for name, kind := range map[string]client.Object{
		"claim":     &networkingv1alpha.NetworkInterfaceClaim{},
		"interface": &networkingv1alpha.NetworkInterface{},
	} {
		t.Run(name, func(t *testing.T) {
			s := newScenario(t, true, ipv4Only)
			run := s.realizedPort()
			customer, provider := run.handoverPending()
			carried := run.carried()
			before := run.changes()

			run.realizer.Client = failingStatus{Client: s.client, kind: kind}
			require.ErrorIs(t, run.realize(), errStatusWrite)
			require.Equal(t, before, run.changes(), "the port is not touched when a withdrawal fails")
			require.Equal(t, carried, run.carried())
			require.True(t, provider.hasDetachFinalizer("install-1-boot"))

			run.realizer.Client = s.client
			run.mustRealize()
			require.False(t, provider.hasDetachFinalizer("install-1-boot"))
			customer.requireReported("server-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)
		})
	}
}

func TestAPortNeverCarriesTwoProjectsOrBootWithCustomer(t *testing.T) {
	customer := newScenario(t, true, ipv4Only)
	run := customer.realizedPort()
	provider := run.sibling(providerProject)
	other := run.sibling(otherProject)
	release := func(s *scenario, name string) {
		t.Helper()
		require.NoError(t, s.client.Delete(s.ctx, s.getClaim(name)))
		s.reconcile(s.getClaim(name))
		run.mustRealize()
		s.reconcile(s.getClaim(name))
		require.True(t, s.claimGone(name))
	}

	customer.bindPhysical("server-data", "")
	waiting := other.createClaim("their-data", other.physicalSpec(realizedPort, ""))
	other.reconcile(waiting)
	other.requireWaiting("their-data")
	run.mustRealize()
	customer.requireReported("server-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)

	provider.bindPhysical("reinstall-1-boot", networkingv1alpha.NetworkInterfacePurposeBoot)
	run.mustRealize()
	require.Equal(t, []types.NamespacedName{networkOf(provider)}, networksOf(run.carried()))
	customer.requireReported("server-data", metav1.ConditionFalse, networkingv1alpha.NetworkInterfaceClaimReasonBootAttachmentActive)

	release(provider, "reinstall-1-boot")
	require.Equal(t, []types.NamespacedName{networkOf(customer)}, networksOf(run.carried()))

	release(customer, "server-data")
	require.Empty(t, run.carried())

	other.reconcile(other.getClaim("their-data"))
	other.requireBound("their-data")
	run.mustRealize()
	require.Equal(t, []types.NamespacedName{networkOf(other)}, networksOf(run.carried()))
	other.requireReported("their-data", metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork)

	release(other, "their-data")
	provider.bindPhysical("wipe-1-boot", networkingv1alpha.NetworkInterfacePurposeBoot)
	run.mustRealize()
	require.Equal(t, []types.NamespacedName{networkOf(provider)}, networksOf(run.carried()))

	require.GreaterOrEqual(t, run.changes(), 6)
	run.requireSafeHistory()
}

// claimCacheMissingDeletion answers claim reads the way a cache that has not
// seen a deletion yet does.
type claimCacheMissingDeletion struct {
	client.Client
}

func (c claimCacheMissingDeletion) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if claim, ok := obj.(*networkingv1alpha.NetworkInterfaceClaim); ok {
		claim.DeletionTimestamp = nil
	}
	return nil
}

func TestTheRealizerReadsFromTheAPIServer(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	run.realizer.Client = staleHoldCache{claimCacheMissingDeletion{s.client}}
	s.bindPhysical("server-data", "")
	run.mustRealize()
	require.Len(t, run.carried(), 1, "a hold the cache has not seen is still realized")

	require.NoError(t, s.client.Delete(s.ctx, s.getClaim("server-data")))
	s.reconcile(s.getClaim("server-data"))
	run.mustRealize()
	require.Empty(t, run.carried(), "a deletion the cache has not seen still takes the network off")
	require.False(t, s.hasDetachFinalizer("server-data"))
}

type realizedStatus struct {
	vlan       *int32
	prepared   metav1.Condition
	programmed metav1.Condition
}

func realizedStatusOf(vlan *int32, conditions []metav1.Condition) realizedStatus {
	status := realizedStatus{vlan: vlan}
	if c := apimeta.FindStatusCondition(conditions, networkingv1alpha.NetworkInterfaceClaimPrepared); c != nil {
		status.prepared = *c
	}
	if c := apimeta.FindStatusCondition(conditions, networkingv1alpha.NetworkInterfaceClaimProgrammed); c != nil {
		status.programmed = *c
	}
	return status
}

func TestTheOperatorKeepsWhatTheRealizerReports(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	s.bindPhysical("server-data", "")
	run.mustRealize()

	claim := s.getClaim("server-data")
	iface, err := s.getInterface("server-data")
	require.NoError(t, err)
	wantClaim := realizedStatusOf(claim.Status.VLANID, claim.Status.Conditions)
	wantInterface := realizedStatusOf(iface.Status.VLANID, iface.Status.Conditions)
	require.NotNil(t, wantClaim.vlan)
	require.Equal(t, metav1.ConditionTrue, wantClaim.programmed.Status)

	s.createSubnet("default-v4", s.networkContextName("default"), networkingv1alpha.IPv4Protocol, "192.0.2.0", 24)
	for range 2 {
		s.reconcile(s.getClaim("server-data"))
		s.reconcileInterface("server-data")
	}

	claim = s.getClaim("server-data")
	iface, err = s.getInterface("server-data")
	require.NoError(t, err)
	require.NotEmpty(t, iface.Spec.Addresses[0].Gateway, "the operator wrote the interface after the realizer")
	require.Equal(t, wantClaim, realizedStatusOf(claim.Status.VLANID, claim.Status.Conditions))
	require.Equal(t, wantInterface, realizedStatusOf(iface.Status.VLANID, iface.Status.Conditions))
	s.requireReady("server-data")
}

func TestAPortWhoseHoldAnOperatorDeletesIsCleared(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	s.bindPhysical("server-data", "")
	run.mustRealize()
	require.Len(t, run.carried(), 1)

	require.NoError(t, s.client.Delete(s.ctx, s.requireHold(realizedPort)))
	run.mustRealize()
	require.Empty(t, run.carried(), "a port no hold names carries nothing")
	requireProgrammed(t, s.getClaim("server-data").Status.Conditions, metav1.ConditionFalse,
		networkingv1alpha.NetworkInterfaceClaimReasonPortChanging)

	s.reconcile(s.getClaim("server-data"))
	s.requireHold(realizedPort)
	run.mustRealize()
	run.requireCarriedOn(s, "server-data")
}

func TestAPortReleasedByBreakGlassIsCleared(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	run := s.realizedPort()
	s.bindPhysical("server-data", "")
	run.mustRealize()

	s.detach("server-data")
	require.NoError(t, s.client.Delete(s.ctx, s.getClaim("server-data")))
	s.reconcile(s.getClaim("server-data"))
	require.True(t, s.claimGone("server-data"))
	_, err := s.getHold(realizedPort)
	require.True(t, apierrors.IsNotFound(err))
	require.Len(t, run.carried(), 1, "the realizer has not seen the release yet")

	run.mustRealize()
	require.Empty(t, run.carried())

	_, err = run.realizer.Reconcile(s.ctx, physicalrealizer.SweepRequest)
	require.NoError(t, err)
	require.Empty(t, run.carried())
}
