// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// staticProjectResolver serves every project from one client. The hub and the
// project control plane are separate clusters in production and one apiserver
// here; what the controller does with each is the same either way.
type staticProjectResolver struct {
	clients map[string]client.Client
}

func (r *staticProjectResolver) ClientForProject(_ context.Context, project string) (client.Client, error) {
	cl, ok := r.clients[project]
	if !ok {
		return nil, fmt.Errorf("cluster %q: %w", project, errProjectClusterNotFound)
	}
	return cl, nil
}

var errProjectClusterNotFound = fmt.Errorf("cluster not found")

type presenceScenario struct {
	t   *testing.T
	ctx context.Context

	hub        client.Client
	reconciler *NetworkPresenceReconciler

	hubNamespace     string
	projectNamespace string
	locationName     string
	networkName      string
	network          *networkingv1alpha.Network
}

type presenceOptions struct {
	// unlabelledNamespace leaves the hub namespace naming no project.
	unlabelledNamespace bool
	// withoutProjectedLocation skips the Location the project needs projected
	// into it before it can use the location.
	withoutProjectedLocation bool
	// withoutNetwork skips creating the Network in the project.
	withoutNetwork bool
	// families overrides the network's address families.
	families []networkingv1alpha.IPFamily
	// mtu overrides the network's MTU.
	mtu int32
	// unclaimedGrace is how long a presence nothing declares is kept. Zero tears
	// it down on the first observation, which is what most of these assert.
	unclaimedGrace time.Duration
}

func newPresenceScenario(t *testing.T, opts presenceOptions) *presenceScenario {
	t.Helper()
	cl, _ := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	suffix := sanitizeName(strings.ToLower(t.Name()))
	if len(suffix) > 48 {
		suffix = suffix[:48]
	}
	s := &presenceScenario{
		t:                t,
		ctx:              ctx,
		hub:              cl,
		hubNamespace:     "hub-" + suffix,
		projectNamespace: "proj-" + suffix,
		locationName:     "loc-" + suffix,
		networkName:      "net-" + suffix,
	}

	hubNamespace := &corev1.Namespace{}
	hubNamespace.Name = s.hubNamespace
	hubNamespace.Labels = map[string]string{
		downstreamclient.UpstreamOwnerNamespaceLabel: s.projectNamespace,
	}
	if !opts.unlabelledNamespace {
		hubNamespace.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] = "cluster-" + testProject
	}
	require.NoError(t, cl.Create(ctx, hubNamespace))

	projectNamespace := &corev1.Namespace{}
	projectNamespace.Name = s.projectNamespace
	require.NoError(t, cl.Create(ctx, projectNamespace))

	if !opts.withoutProjectedLocation {
		location := projectedLocation(s.locationName)
		require.NoError(t, cl.Create(ctx, location))
		t.Cleanup(func() { _ = cl.Delete(ctx, location) })
	}

	if !opts.withoutNetwork {
		families := opts.families
		if families == nil {
			families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
		}
		mtu := opts.mtu
		if mtu == 0 {
			mtu = 1460
		}

		network := &networkingv1alpha.Network{}
		network.Namespace = s.projectNamespace
		network.Name = s.networkName
		network.Spec.IPAM.Mode = networkingv1alpha.NetworkIPAMModeAuto
		network.Spec.IPFamilies = families
		network.Spec.MTU = mtu
		require.NoError(t, cl.Create(ctx, network))
		s.network = network
	}

	s.reconciler = &NetworkPresenceReconciler{
		Projects:             &staticProjectResolver{clients: map[string]client.Client{testProject: cl}},
		UnclaimedGracePeriod: opts.unclaimedGrace,
		hub:                  cl,
	}

	return s
}

// contextName is the deterministic name every binding for the pair resolves to.
func (s *presenceScenario) contextName() string {
	return networkContextName(s.networkName, locationsv1alpha1.LocationReference{
		Name: s.locationName,
	})
}

func (s *presenceScenario) request() ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{
		Namespace: s.hubNamespace,
		Name:      s.contextName(),
	}}
}

func (s *presenceScenario) createBinding(name string) *networkingv1alpha.NetworkBinding {
	s.t.Helper()

	binding := &networkingv1alpha.NetworkBinding{}
	binding.Namespace = s.hubNamespace
	binding.Name = name
	binding.Labels = map[string]string{
		networkingv1alpha.NetworkLabel:  s.networkName,
		networkingv1alpha.LocationLabel: s.locationName,
	}
	binding.Spec.Network = networkingv1alpha.NetworkRef{Name: s.networkName}
	binding.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	binding.Spec.Consumer = &networkingv1alpha.NetworkBindingConsumer{
		APIGroup: "networking.datumapis.com",
		Kind:     "LoadBalancer",
		Name:     name,
	}
	require.NoError(s.t, s.hub.Create(s.ctx, binding))
	return binding
}

func (s *presenceScenario) reconcile() {
	s.t.Helper()
	_, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(s.t, err)
}

func (s *presenceScenario) binding(name string) *networkingv1alpha.NetworkBinding {
	s.t.Helper()
	var binding networkingv1alpha.NetworkBinding
	require.NoError(s.t, s.hub.Get(s.ctx,
		client.ObjectKey{Namespace: s.hubNamespace, Name: name}, &binding))
	return &binding
}

// networkContext reads the presence where this controller writes it: the
// project control plane, beside the network it is derived from.
func (s *presenceScenario) networkContext() (*networkingv1alpha.NetworkContext, bool) {
	s.t.Helper()
	var networkContext networkingv1alpha.NetworkContext
	err := s.hub.Get(s.ctx,
		client.ObjectKey{Namespace: s.projectNamespace, Name: s.contextName()}, &networkContext)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(s.t, err)
	return &networkContext, true
}

// markContextReady stands in for whatever programs the context at its backend,
// which nothing in this repository does.
func (s *presenceScenario) markContextReady() {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok)

	apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:    networkingv1alpha.NetworkContextReady,
		Status:  metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkContextReadyReasonReady,
		Message: "test",
	})
	require.NoError(s.t, s.hub.Status().Update(s.ctx, networkContext))
}

func requireReady(t *testing.T, binding *networkingv1alpha.NetworkBinding, status metav1.ConditionStatus, reason string) {
	t.Helper()
	condition := apimeta.FindStatusCondition(binding.Status.Conditions, networkingv1alpha.NetworkBindingReady)
	require.NotNil(t, condition, "binding %q carries no Ready condition", binding.Name)
	require.Equal(t, status, condition.Status, "binding %q reason %q", binding.Name, condition.Reason)
	require.Equal(t, reason, condition.Reason)
	require.Equal(t, binding.Generation, condition.ObservedGeneration)
}

func TestNetworkPresenceCreatesContextCarryingTheNetworksRules(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{
		families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol},
		mtu:      8000,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok, "the presence controller created no network context")

	require.Equal(t, s.networkName, networkContext.Spec.Network.Name)
	require.Equal(t, s.locationName, networkContext.Spec.Location.Name)
	require.Equal(t, []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}, networkContext.Spec.IPFamilies)
	require.Equal(t, int32(8000), networkContext.Spec.MTU)
	require.Equal(t, s.network.Generation, networkContext.Spec.NetworkGeneration)

	require.Equal(t, "cluster-"+testProject,
		networkContext.Labels[downstreamclient.UpstreamOwnerClusterNameLabel])
	require.Equal(t, s.projectNamespace,
		networkContext.Labels[downstreamclient.UpstreamOwnerNamespaceLabel])
	require.Equal(t, s.networkName, networkContext.Labels[networkingv1alpha.NetworkLabel])
	require.Equal(t, s.locationName, networkContext.Labels[networkingv1alpha.LocationLabel])
	require.Equal(t, string(s.network.UID), networkContext.Labels[networkingv1alpha.NetworkUIDLabel])

	// The context lives beside the network it is derived from, owned by it, so
	// the apiserver collects it when the network goes.
	require.Equal(t, s.projectNamespace, networkContext.Namespace)
	require.Len(t, networkContext.OwnerReferences, 1)
	require.Equal(t, "Network", networkContext.OwnerReferences[0].Kind)
	require.Equal(t, s.networkName, networkContext.OwnerReferences[0].Name)
}

// A network deleted while a presence exists takes the presence with it, which
// is what retiring the hub-side finalizer relies on.
func TestNetworkPresenceContextIsCollectedWithTheNetwork(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, s.network.UID, networkContext.OwnerReferences[0].UID,
		"garbage collection keys on the owner, so it must be this network and not its name")
	require.True(t, *networkContext.OwnerReferences[0].Controller)
}

// The reference is set when the presence is created, which is before it is
// ready, so this combination is the normal middle of the sequence.
func TestNetworkPresenceReportsNotReadyWithAReferenceAlreadySet(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	binding := s.binding("consumer-a")
	requireReady(t, binding, metav1.ConditionFalse, networkingv1alpha.NetworkBindingReasonNetworkContextNotReady)
	require.NotNil(t, binding.Status.NetworkContextRef)
	require.Equal(t, s.contextName(), binding.Status.NetworkContextRef.Name)
	require.Equal(t, s.projectNamespace, binding.Status.NetworkContextRef.Namespace,
		"a consumer is pointed at the object that exists, which is the project-plane one")
}

func TestNetworkPresenceReachesReadyOnceTheContextIs(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()
	s.markContextReady()
	s.reconcile()

	requireReady(t, s.binding("consumer-a"), metav1.ConditionTrue,
		networkingv1alpha.NetworkBindingReasonNetworkContextReady)
}

// Ready going back to false is a state consumers must expect, and the binding
// has to report the current answer rather than latch the old one.
func TestNetworkPresenceReportsAReadinessRegression(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()
	s.markContextReady()
	s.reconcile()
	requireReady(t, s.binding("consumer-a"), metav1.ConditionTrue,
		networkingv1alpha.NetworkBindingReasonNetworkContextReady)

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:    networkingv1alpha.NetworkContextReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Regressed",
		Message: "test",
	})
	require.NoError(t, s.hub.Status().Update(s.ctx, networkContext))

	s.reconcile()

	binding := s.binding("consumer-a")
	requireReady(t, binding, metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextNotReady)
	require.NotNil(t, binding.Status.NetworkContextRef,
		"a regression must not withdraw the reference to the presence serving the binding")
}

// A location the project cannot use reads as a network problem rather than as a
// consumer that never becomes ready.
func TestNetworkPresenceRefusesALocationTheProjectCannotUse(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{withoutProjectedLocation: true})
	s.createBinding("consumer-a")
	s.reconcile()

	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonLocationNotAvailable)

	_, ok := s.networkContext()
	require.False(t, ok, "no presence may be created in a location the project cannot use")
}

// A refusal has no watch behind it, so it needs its own way back. Without the
// requeue a binding refused for an unavailable location would stay refused
// after the platform enabled it.
func TestNetworkPresenceRetriesARefusal(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{withoutProjectedLocation: true})
	s.createBinding("consumer-a")

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, refusedPresenceRetryInterval, result.RequeueAfter)

	location := projectedLocation(s.locationName)
	require.NoError(t, s.hub.Create(s.ctx, location))
	t.Cleanup(func() { _ = s.hub.Delete(s.ctx, location) })

	result, err = s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)

	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextNotReady)
	_, ok := s.networkContext()
	require.True(t, ok, "the presence appears once the platform enables the location")

	s.markContextReady()
	result, err = s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
}

// The context lives in a control plane this controller does not watch, so its
// becoming ready is not an event here. Without a requeue the binding would
// report NotReady for as long as it exists.
func TestNetworkPresenceRetriesWhileTheContextIsNotReady(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, refusedPresenceRetryInterval, result.RequeueAfter,
		"nothing else will bring this binding back once the context is ready")
	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextNotReady)

	s.markContextReady()

	result, err = s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	requireReady(t, s.binding("consumer-a"), metav1.ConditionTrue,
		networkingv1alpha.NetworkBindingReasonNetworkContextReady)
}

// A binding in a project control plane belongs to the other controller. Serving
// it here overwrites that controller's answer with ProjectUnresolved, and the
// teardown path would delete a context this controller never created.
func TestNetworkPresenceLeavesProjectPlaneBindingsAlone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})

	plain := &corev1.Namespace{}
	plain.Name = "plane-" + sanitizeName(strings.ToLower(t.Name()))[:40]
	require.NoError(t, s.hub.Create(s.ctx, plain))

	binding := &networkingv1alpha.NetworkBinding{}
	binding.Namespace = plain.Name
	binding.Name = "project-plane-binding"
	binding.Spec.Network = networkingv1alpha.NetworkRef{Name: s.networkName}
	binding.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	require.NoError(t, s.hub.Create(s.ctx, binding))

	_, err := s.reconciler.Reconcile(s.ctx, ctrl.Request{NamespacedName: client.ObjectKey{
		Namespace: plain.Name,
		Name:      networkContextNameForBinding(binding),
	}})
	require.NoError(t, err)

	var seen networkingv1alpha.NetworkBinding
	require.NoError(t, s.hub.Get(s.ctx, client.ObjectKeyFromObject(binding), &seen))
	condition := apimeta.FindStatusCondition(seen.Status.Conditions, networkingv1alpha.NetworkBindingReady)
	require.NotNil(t, condition)
	require.Equal(t, networkingv1alpha.NetworkBindingReasonPending, condition.Reason,
		"the presence controller must not answer for a binding it does not serve")
}

// The hub carries a replicated copy of the presence under the same name and the
// same labels, and every cell reads the network's rules from what that copy
// becomes. Teardown must reach the project-plane object and nothing else.
func TestNetworkPresenceLeavesTheReplicatedHubCopyAlone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})

	replicated := &networkingv1alpha.NetworkContext{}
	replicated.Namespace = s.hubNamespace
	replicated.Name = s.contextName()
	replicated.Labels = map[string]string{
		networkingv1alpha.NetworkLabel:    s.networkName,
		networkingv1alpha.LocationLabel:   s.locationName,
		networkingv1alpha.NetworkUIDLabel: "9a4c-whatever",
	}
	replicated.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: s.networkName}
	replicated.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	replicated.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	replicated.Spec.MTU = 1460
	require.NoError(t, s.hub.Create(s.ctx, replicated))

	s.reconcile()

	require.NoError(t, s.hub.Get(s.ctx, client.ObjectKeyFromObject(replicated), replicated),
		"the copy every cell reads must survive having no holder on the hub")
}

func TestNetworkPresenceRefusesAMissingNetwork(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{withoutNetwork: true})
	s.createBinding("consumer-a")
	s.reconcile()

	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkNotFound)

	_, ok := s.networkContext()
	require.False(t, ok)
}

func TestNetworkPresenceRefusesANamespaceThatNamesNoProject(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unlabelledNamespace: true})
	s.createBinding("consumer-a")
	s.reconcile()

	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonProjectUnresolved)
}

func TestNetworkPresenceRefusesAProjectItCannotReach(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.reconciler.Projects = &staticProjectResolver{clients: map[string]client.Client{}}
	s.createBinding("consumer-a")

	_, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.Error(t, err, "an unreachable project is a transient fault, not a refusal")
}

// Two consumers of different kinds converge on one presence, and neither knows
// about the other.
func TestNetworkPresenceIsSharedByEveryConsumerOfThePair(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.createBinding("consumer-b")
	s.reconcile()
	s.markContextReady()
	s.reconcile()

	for _, name := range []string{"consumer-a", "consumer-b"} {
		binding := s.binding(name)
		requireReady(t, binding, metav1.ConditionTrue,
			networkingv1alpha.NetworkBindingReasonNetworkContextReady)
		require.Equal(t, s.contextName(), binding.Status.NetworkContextRef.Name,
			"every binding for the pair is served by the same presence")
	}

	var contexts networkingv1alpha.NetworkContextList
	require.NoError(t, s.hub.List(s.ctx, &contexts, client.InNamespace(s.projectNamespace)))
	require.Len(t, contexts.Items, 1, "two consumers must share one presence")
}

func TestNetworkPresenceSurvivesTheFirstConsumerGoingAway(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	first := s.createBinding("consumer-a")
	s.createBinding("consumer-b")
	s.reconcile()

	require.NoError(t, s.hub.Delete(s.ctx, first))
	s.reconcile()

	_, ok := s.networkContext()
	require.True(t, ok, "a presence another consumer still declares must not be torn down")
}

func TestNetworkPresenceIsTornDownByTheLastConsumerGoingAway(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	first := s.createBinding("consumer-a")
	second := s.createBinding("consumer-b")
	s.reconcile()
	require.True(t, mustExist(s.networkContext()))

	require.NoError(t, s.hub.Delete(s.ctx, first))
	s.reconcile()
	require.NoError(t, s.hub.Delete(s.ctx, second))
	s.reconcile()

	_, ok := s.networkContext()
	require.False(t, ok, "the last declaration going away tears the presence down")
}

// A network edited after its context exists has to reach the locations carrying
// it; nothing else would ever rewrite that context.
func TestNetworkPresenceConvergesOnANetworkEdit(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{
		families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		mtu:      1460,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(1460), networkContext.Spec.MTU)
	generationBefore := networkContext.Spec.NetworkGeneration

	s.network.Spec.MTU = 8856
	s.network.Spec.IPFamilies = []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol,
	}
	require.NoError(t, s.hub.Update(s.ctx, s.network))

	s.reconcile()

	networkContext, ok = s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(8856), networkContext.Spec.MTU)
	require.Equal(t, []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol,
	}, networkContext.Spec.IPFamilies)
	require.Greater(t, networkContext.Spec.NetworkGeneration, generationBefore,
		"networkGeneration is what makes staleness visible")
}

// observedGeneration is the binding's own, so a consumer can tell an answer
// about the current spec from an answer about the previous one.
func TestNetworkPresenceReportsTheBindingsOwnGeneration(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	binding := s.createBinding("consumer-a")
	s.reconcile()

	condition := apimeta.FindStatusCondition(
		s.binding("consumer-a").Status.Conditions, networkingv1alpha.NetworkBindingReady)
	require.NotNil(t, condition)
	require.Equal(t, binding.Generation, condition.ObservedGeneration)
}

// A context deleted out from under the controller is rebuilt.
func TestNetworkPresenceRebuildsADeletedContext(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.NoError(t, s.hub.Delete(s.ctx, networkContext))

	s.reconcile()

	_, ok = s.networkContext()
	require.True(t, ok, "a presence a binding still declares must come back")
}

// A binding whose network or location changed is a declaration about a
// different presence, and the API refuses the crossing.
func TestNetworkBindingPairIsImmutable(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	binding := s.createBinding("consumer-a")

	binding.Spec.Location.Name = "somewhere-else"
	require.Error(t, s.hub.Update(s.ctx, binding))

	binding = s.binding("consumer-a")
	binding.Spec.Network.Name = "another-network"
	require.Error(t, s.hub.Update(s.ctx, binding))
}

func mustExist(_ *networkingv1alpha.NetworkContext, ok bool) bool { return ok }

// holdContext stands in for the finalizers a real context carries while its
// address space is being given back, so it stays Terminating for as long as the
// test needs it to.
func (s *presenceScenario) holdContext() *networkingv1alpha.NetworkContext {
	s.t.Helper()

	networkContext, ok := s.networkContext()
	require.True(s.t, ok)

	networkContext.Finalizers = append(networkContext.Finalizers, "test.datumapis.com/hold")
	require.NoError(s.t, s.hub.Update(s.ctx, networkContext))
	require.NoError(s.t, s.hub.Delete(s.ctx, networkContext))

	networkContext, ok = s.networkContext()
	require.True(s.t, ok)
	require.False(s.t, networkContext.DeletionTimestamp.IsZero())
	return networkContext
}

func (s *presenceScenario) releaseContext() {
	s.t.Helper()

	networkContext, ok := s.networkContext()
	require.True(s.t, ok)
	networkContext.Finalizers = nil
	require.NoError(s.t, s.hub.Update(s.ctx, networkContext))

	_, ok = s.networkContext()
	require.False(s.t, ok)
}

// A workload deleted and recreated inside the window its context takes to finish
// being deleted must not be bound to the dying object. Adopting it wedges the
// binding permanently: the context never comes back, and nothing brings the
// presence round again.
func TestNetworkPresenceRefusesATerminatingContext(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	held := s.holdContext()

	s.reconcile()

	binding := s.binding("consumer-a")
	requireReady(t, binding, metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextTerminating)
	require.Nil(t, binding.Status.NetworkContextRef,
		"a context that is going away is not the presence serving this binding")

	after, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, held.UID, after.UID)
	require.Equal(t, held.Generation, after.Generation,
		"a terminating context must not be adopted and rewritten")
}

// Recovery keys on the context going, which is an event the sync controller
// carries. A context that still says Ready while terminating must not be
// believed either.
func TestNetworkPresenceRebuildsOnceTheTerminatingContextGoes(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	held := s.holdContext()
	s.markContextReady()

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, refusedPresenceRetryInterval, result.RequeueAfter)
	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextTerminating,
	)

	s.releaseContext()
	s.reconcile()

	fresh, ok := s.networkContext()
	require.True(t, ok, "the presence a binding still declares has to be built again")
	require.NotEqual(t, held.UID, fresh.UID, "the binding must be served by a new context, not the old one")
	require.True(t, fresh.DeletionTimestamp.IsZero())

	binding := s.binding("consumer-a")
	require.NotNil(t, binding.Status.NetworkContextRef)
	require.Equal(t, s.contextName(), binding.Status.NetworkContextRef.Name)
}

// Replacing a workload deletes its binding and creates the replacement's a few
// seconds later. Tearing the presence down in that gap gives this location's
// address space back and takes every address in it.
func TestNetworkPresenceSurvivesABindingBeingReplaced(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	binding := s.createBinding("consumer-a")
	s.reconcile()

	before, ok := s.networkContext()
	require.True(t, ok)

	require.NoError(t, s.hub.Delete(s.ctx, binding))
	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter, "the wait has to come back and finish the teardown")

	unclaimed, ok := s.networkContext()
	require.True(t, ok, "a presence must not be torn down on the first observation that nothing declares it")
	require.NotEmpty(t, unclaimed.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation],
		"the wait is recorded on the object so a restart neither restarts nor skips it")

	s.createBinding("consumer-b")
	s.reconcile()

	after, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, before.UID, after.UID, "the replacement is served by the presence that was already here")
	require.Equal(t, before.CreationTimestamp, after.CreationTimestamp)
	require.Empty(t, after.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation],
		"a presence declared again is no longer waiting to be torn down")
}

// The wait is a delay, not a reprieve: a workload that really has gone gives its
// address space back.
func TestNetworkPresenceIsTornDownOnceTheGraceExpires(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	binding := s.createBinding("consumer-a")
	s.reconcile()

	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation] =
		time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	require.NoError(t, s.hub.Update(s.ctx, networkContext))

	s.reconcile()

	_, ok = s.networkContext()
	require.False(t, ok, "nothing has declared this presence for longer than the wait")
}

func projectedLocation(name string) *locationsv1alpha1.Location {
	location := &locationsv1alpha1.Location{}
	location.Name = name
	location.Spec = locationsv1alpha1.LocationSpec{
		LocationClassRef: locationsv1alpha1.LocationClassReference{Name: "datum-managed"},
		Topology:         map[string]string{locationsv1alpha1.TopologyCityCodeKey: "IAD"},
	}
	return location
}
