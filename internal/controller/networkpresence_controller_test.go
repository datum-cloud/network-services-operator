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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// staticProjectResolver serves every project from one client. The hub and the
// project control plane are separate clusters in production and one apiserver
// here; what the controller does with each is the same either way.
type staticProjectResolver struct {
	clients map[string]client.Client
	readers map[string]client.Reader
}

// ClusterForProject reads through the reader a test set for the project, else
// through its client, which in these tests is not cached.
func (r *staticProjectResolver) ClusterForProject(_ context.Context, project string) (cluster.Cluster, error) {
	cl, ok := r.clients[project]
	if !ok {
		return nil, fmt.Errorf("cluster %q: %w", project, errProjectClusterNotFound)
	}
	return &planeCluster{scheme: cl.Scheme(), client: cl, reader: r.readers[project]}, nil
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
			families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
		}
		mtu := opts.mtu
		if mtu == 0 {
			mtu = 1440
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
	replicated.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	replicated.Spec.MTU = 1440
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
		families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		mtu:      1440,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(1440), networkContext.Spec.MTU)
	generationBefore := networkContext.Spec.NetworkGeneration

	s.network.Spec.MTU = 8856
	s.network.Spec.IPFamilies = []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}
	require.NoError(t, s.hub.Update(s.ctx, s.network))

	s.reconcile()

	networkContext, ok = s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(8856), networkContext.Spec.MTU)
	require.Equal(t, []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
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

// A binding written straight into the project control plane uses the same
// context a hub binding does, and holds addresses in it. The last hub consumer
// going away must not take the context from under it, however long the wait.
func TestNetworkPresenceKeepsAContextAProjectPlaneBindingStillUses(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	hubBinding := s.createBinding("consumer-a")
	s.reconcile()

	before, ok := s.networkContext()
	require.True(t, ok)

	projectBinding := &networkingv1alpha.NetworkBinding{}
	projectBinding.Namespace = s.projectNamespace
	projectBinding.Name = "project-plane-binding"
	projectBinding.Spec.Network = networkingv1alpha.NetworkRef{Name: s.networkName}
	projectBinding.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	require.NoError(t, s.hub.Create(s.ctx, projectBinding))

	// A stamp left from an earlier gap must not count down to a teardown either.
	if before.Annotations == nil {
		before.Annotations = map[string]string{}
	}
	before.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation] =
		time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	require.NoError(t, s.hub.Update(s.ctx, before))

	require.NoError(t, s.hub.Delete(s.ctx, hubBinding))
	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter, "a declared presence has no teardown to come back for")

	after, ok := s.networkContext()
	require.True(t, ok, "a presence a project-plane binding still uses must not be torn down")
	require.Equal(t, before.UID, after.UID)
	require.True(t, after.DeletionTimestamp.IsZero())
	require.Empty(t, after.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation],
		"a presence the project still declares is not waiting to be torn down")

	require.NoError(t, s.hub.Delete(s.ctx, projectBinding))
	result, err = s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter, "once nothing declares it, the usual wait applies")
}

// deleteNetwork starts the network's deletion and leaves it there, as the
// network's own finalizer does while its contexts are still live.
func (s *presenceScenario) deleteNetwork() {
	s.t.Helper()
	s.network.Finalizers = append(s.network.Finalizers, "test.datumapis.com/hold")
	require.NoError(s.t, s.hub.Update(s.ctx, s.network))
	require.NoError(s.t, s.hub.Delete(s.ctx, s.network))
}

// keepContext stands in for the location's hold, so a context can still be read
// after it is deleted.
func (s *presenceScenario) keepContext() {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok)
	networkContext.Finalizers = append(networkContext.Finalizers, "test.datumapis.com/hold")
	require.NoError(s.t, s.hub.Update(s.ctx, networkContext))
}

// A network being deleted takes its presences with it whatever still binds
// them, and each location reads that from the context it already has.
func TestNetworkPresenceMarksTheContextOfADeletingNetwork(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()
	s.keepContext()

	s.deleteNetwork()
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.True(t, networkContext.Spec.NetworkDeleting, "the location reads the deletion from its context")
	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextTerminating)
}

// A presence in its grace is declared by nothing, so only the network's
// deletion can tell its location to give up what it keeps there.
func TestNetworkPresenceMarksAContextInItsGraceWhenTheNetworkIsDeleted(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
	binding := s.createBinding("consumer-a")
	s.reconcile()
	s.keepContext()
	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconcile()

	s.deleteNetwork()
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.True(t, networkContext.Spec.NetworkDeleting,
		"the network's deletion does not wait out a grace kept for the network's own return")
}

// publishInterface stands in for a cell publishing an interface to the hub,
// where the presence reads what its location still holds.
func (s *presenceScenario) publishInterface(name, network, location string) *networkingv1alpha.NetworkInterface {
	s.t.Helper()
	copied := &networkingv1alpha.NetworkInterface{}
	copied.Namespace = s.hubNamespace
	copied.Name = name
	copied.Labels = map[string]string{
		networkingv1alpha.NetworkInterfaceProjectionLabel: "true",
		networkingv1alpha.NetworkInterfaceLocationLabel:   location,
	}
	copied.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: network}
	require.NoError(s.t, s.hub.Create(s.ctx, copied))
	return copied
}

func (s *presenceScenario) expireGrace() {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok)
	networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation] =
		time.Now().Add(-2 * s.reconciler.UnclaimedGracePeriod).UTC().Format(time.RFC3339)
	require.NoError(s.t, s.hub.Update(s.ctx, networkContext))
}

func (s *presenceScenario) requireContextLive(why string) *networkingv1alpha.NetworkContext {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok, why)
	require.True(s.t, networkContext.DeletionTimestamp.IsZero(), why)
	return networkContext
}

func (s *presenceScenario) requireContextGone(why string) {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, !ok || !networkContext.DeletionTimestamp.IsZero(), why)
}

// A deleting network's presence stays live and marked until its location gives
// up the network's interfaces. Deleting it sooner leaves the location's copy to
// arrive without the mark, and a kept interface held there for good.
func TestNetworkPresenceOfADeletingNetworkWaitsForItsLocationsInterfaces(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()
	kept := s.publishInterface("kept-eth0", s.networkName, s.locationName)

	s.deleteNetwork()
	s.reconcile()

	networkContext := s.requireContextLive("the location still holds an interface of the network")
	require.True(t, networkContext.Spec.NetworkDeleting)

	require.NoError(t, s.hub.Delete(s.ctx, kept))
	s.reconcile()
	s.requireContextGone("the location holds nothing of the network any more")
}

// Where the location holds nothing of the network, its presence ends as fast as
// it does today. Another location's interfaces and another network's are not
// this presence's.
func TestNetworkPresenceOfADeletingNetworkEndsAtOnceWhereItsLocationHoldsNothing(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
	s.createBinding("consumer-a")
	s.reconcile()
	s.publishInterface("elsewhere-eth0", s.networkName, "another-location")
	s.publishInterface("other-network-eth0", "another-network", s.locationName)

	s.deleteNetwork()
	s.reconcile()

	s.requireContextGone("nothing at this location belongs to the network")
}

// A copy is collected as soon as its interface starts going, so one being
// deleted is not an interface the location still holds.
func TestNetworkPresenceCountsACopyBeingDeletedAsGone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()
	going := s.publishInterface("going-eth0", s.networkName, s.locationName)
	going.Finalizers = []string{"test.datumapis.com/hold"}
	require.NoError(t, s.hub.Update(s.ctx, going))
	require.NoError(t, s.hub.Delete(s.ctx, going))

	s.deleteNetwork()
	s.reconcile()

	s.requireContextGone("a copy being deleted holds nothing")
}

// unreadableCopies is a hub whose interface copies cannot be listed.
type unreadableCopies struct {
	client.Client
}

func (c unreadableCopies) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*networkingv1alpha.NetworkInterfaceList); ok {
		return fmt.Errorf("the hub cannot be reached")
	}
	return c.Client.List(ctx, list, opts...)
}

// A location that cannot be read may still hold interfaces, so a failed read
// ends nothing.
func TestNetworkPresenceEndsNothingWhenItCannotReadItsLocation(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()
	s.reconciler.hub = unreadableCopies{Client: s.hub}

	s.deleteNetwork()
	_, err := s.reconciler.Reconcile(s.ctx, s.request())

	require.Error(t, err)
	s.requireContextLive("an unread location is not an empty one")
}

// Retain keeps an interface past its instance, and the location keeps its
// presence for as long as it keeps the interface: the network stays there, and
// so does the range the interface is addressed from.
func TestNetworkPresencePastItsGraceWaitsForItsLocationsInterfaces(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	binding := s.createBinding("consumer-a")
	s.reconcile()
	kept := s.publishInterface("kept-eth0", s.networkName, s.locationName)
	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconcile()
	s.expireGrace()

	s.reconcile()

	networkContext := s.requireContextLive("a location that keeps an interface keeps its presence")
	require.NotEmpty(t, networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation],
		"the stamp still says when nothing declared the presence")
	require.False(t, networkContext.Spec.NetworkDeleting, "the network stays, so its location keeps what it keeps")

	require.NoError(t, s.hub.Delete(s.ctx, kept))
	s.reconcile()
	s.requireContextGone("nothing declares the presence, and its location holds nothing of the network")
}

// A slot that comes back after the grace finds its presence where it left it.
func TestNetworkPresenceKeptPastItsGraceServesAReturningBinding(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	binding := s.createBinding("consumer-a")
	s.reconcile()
	before := s.requireContextLive("the presence is declared")
	s.publishInterface("kept-eth0", s.networkName, s.locationName)
	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconcile()
	s.expireGrace()
	s.reconcile()

	s.createBinding("consumer-b")
	s.reconcile()

	after := s.requireContextLive("the returning binding is served")
	require.Equal(t, before.UID, after.UID, "the presence the kept interface is addressed from serves it")
	require.Empty(t, after.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation])
}

func TestAnInterfaceCopyMapsToThePresenceOfItsNetworkInItsLocation(t *testing.T) {
	copied := &networkingv1alpha.NetworkInterface{}
	copied.Namespace = "hub-namespace"
	copied.Name = "kept-eth0"
	copied.Labels = map[string]string{
		networkingv1alpha.NetworkInterfaceProjectionLabel: "true",
		networkingv1alpha.NetworkInterfaceLocationLabel:   "us-east-1",
	}
	copied.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}

	require.Equal(t, []reconcile.Request{{NamespacedName: client.ObjectKey{
		Namespace: "hub-namespace",
		Name: networkContextName("default", locationsv1alpha1.LocationReference{
			Name: "us-east-1",
		}),
	}}}, presenceOfInterfaceCopy(context.Background(), copied))

	original := copied.DeepCopy()
	delete(original.Labels, networkingv1alpha.NetworkInterfaceProjectionLabel)
	require.Empty(t, presenceOfInterfaceCopy(context.Background(), original),
		"only a published copy speaks for a location")
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

func TestNetworkPresenceProjectsTheInternetAccessMode(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.reconciler.DefaultInternetEgressMode = networkingv1alpha.NetworkInternetEgressDisabled
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.NotNil(t, networkContext.Spec.Egress)
	require.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, networkContext.Spec.Egress.Internet.Mode,
		"an unstamped network is projected with the platform default")

	s.network.Spec.Egress = &networkingv1alpha.NetworkEgress{
		Internet: &networkingv1alpha.NetworkInternetEgress{Mode: networkingv1alpha.NetworkInternetEgressEnabled},
	}
	require.NoError(t, s.hub.Update(s.ctx, s.network))
	s.reconcile()

	networkContext, ok = s.networkContext()
	require.True(t, ok)
	require.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, networkContext.Spec.Egress.Internet.Mode,
		"a change to the network's mode reaches every location")
}

// Under cluster sharding the event for a network edit is raised in a replica
// this controller does not run in, so a settled presence has to come back on its
// own for the edit to reach the location.
func TestNetworkPresenceResyncsASettledPresence(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.reconciler.ResyncPeriod = 30 * time.Second
	s.createBinding("consumer-a")
	s.reconcile()
	s.markContextReady()

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, result.RequeueAfter,
		"nothing else brings a settled presence back after a network edit")
	requireReady(t, s.binding("consumer-a"), metav1.ConditionTrue,
		networkingv1alpha.NetworkBindingReasonNetworkContextReady)
}

// The sync signals a network's deletion on the hub bindings declaring a
// presence. A presence in its grace has none, so the resync is what brings the
// deletion to it before the grace runs out.
func TestNetworkPresenceInItsGraceComesBackAtTheResync(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
	s.reconciler.ResyncPeriod = 30 * time.Second
	binding := s.createBinding("consumer-a")
	s.reconcile()
	require.NoError(t, s.hub.Delete(s.ctx, binding))

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, result.RequeueAfter, "a network deleted in the grace must not wait it out")
}

// A grace shorter than the resync still ends on time.
func TestNetworkPresenceInAShortGraceComesBackAtItsEnd(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: 10 * time.Second})
	s.reconciler.ResyncPeriod = 30 * time.Second
	binding := s.createBinding("consumer-a")
	s.reconcile()
	require.NoError(t, s.hub.Delete(s.ctx, binding))

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
	require.LessOrEqual(t, result.RequeueAfter, 10*time.Second, "the grace ends before the resync")
}

// Past its grace, a presence waits on its location's kept interfaces with no
// binding left to signal, and the network's deletion is what ends that wait.
func TestNetworkPresenceWaitingOnItsLocationComesBackAtTheResync(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	s.reconciler.ResyncPeriod = 30 * time.Second
	binding := s.createBinding("consumer-a")
	s.reconcile()
	s.publishInterface("kept-eth0", s.networkName, s.locationName)
	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconcile()
	s.expireGrace()

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, result.RequeueAfter, "nothing on the hub signals this presence")

	s.deleteNetwork()
	s.reconcile()
	networkContext := s.requireContextLive("the location still keeps an interface of the network")
	require.True(t, networkContext.Spec.NetworkDeleting, "the resync carries the deletion to the location")
}

// A presence declared only in the project plane has no hub binding to signal
// either, and the network waits on its context.
func TestNetworkPresenceDeclaredInItsProjectComesBackAtTheResync(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Minute})
	s.reconciler.ResyncPeriod = 30 * time.Second
	hubBinding := s.createBinding("consumer-a")
	s.reconcile()

	projectBinding := &networkingv1alpha.NetworkBinding{}
	projectBinding.Namespace = s.projectNamespace
	projectBinding.Name = "project-plane-binding"
	projectBinding.Spec.Network = networkingv1alpha.NetworkRef{Name: s.networkName}
	projectBinding.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	require.NoError(t, s.hub.Create(s.ctx, projectBinding))
	require.NoError(t, s.hub.Delete(s.ctx, hubBinding))

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, result.RequeueAfter, "nothing on the hub signals this presence")
}

// A presence that has ended has nothing to come back for.
func TestNetworkPresenceThatEndedDoesNotComeBack(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.reconciler.ResyncPeriod = 30 * time.Second
	binding := s.createBinding("consumer-a")
	s.reconcile()
	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconcile()
	s.requireContextGone("nothing declares the presence and its location holds nothing")

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
}

// A context marked for an earlier network of the same name is that network's,
// ending. Adopting it would carry the mark to the new network, and its location
// would give up the new network's kept interfaces. The owner reference alone
// does not tell the two apart: it is matched by name.
func TestNetworkPresenceRefusesAContextMarkedForAnEarlierNetwork(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	const earlier = "an-earlier-network-uid"
	left, ok := s.networkContext()
	require.True(t, ok)
	left.Labels[networkingv1alpha.NetworkUIDLabel] = earlier
	left.OwnerReferences[0].UID = earlier
	left.Spec.NetworkDeleting = true
	require.NoError(t, s.hub.Update(s.ctx, left))

	s.reconcile()

	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonNetworkContextTerminating)
	after, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, earlier, after.Labels[networkingv1alpha.NetworkUIDLabel], "the earlier network's context is not adopted")
	require.Equal(t, types.UID(earlier), after.OwnerReferences[0].UID)
}

// replacedBeforeDelete is a project control plane where another writer, such as
// the binding controller, replaces the context between the presence's read and
// its delete.
type replacedBeforeDelete struct {
	client.Client
	t *testing.T
}

func (c replacedBeforeDelete) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if read, ok := obj.(*networkingv1alpha.NetworkContext); ok {
		replacement := &networkingv1alpha.NetworkContext{}
		replacement.Namespace = read.Namespace
		replacement.Name = read.Name
		replacement.Labels = read.Labels
		replacement.Spec = read.Spec
		require.NoError(c.t, c.Client.Delete(ctx, read.DeepCopy()))
		require.NoError(c.t, c.Create(ctx, replacement))
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// A delete names the context it read. One that took whatever holds the name
// would remove a replacement nothing has decided to end.
func TestNetworkPresenceDeletesOnlyTheContextItRead(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	binding := s.createBinding("consumer-a")
	s.reconcile()
	require.NoError(t, s.hub.Delete(s.ctx, binding))
	s.reconciler.Projects = &staticProjectResolver{clients: map[string]client.Client{
		testProject: replacedBeforeDelete{Client: s.hub, t: t},
	}}

	_, err := s.reconciler.Reconcile(s.ctx, s.request())

	require.Error(t, err, "the delete's UID precondition refuses the replacement")
	s.requireContextLive("the replacement is not the context that ended")
}

// The presence reads what a location holds from the copies that location's
// writeback publishes to the hub. This runs the writeback's own publish and the
// presence's own read against one hub, so a change to either side's namespace
// or labels that parts them fails here and not on a live hub. A cell's
// namespace carries the hub namespace's name, as federation delivers it.
func TestThePresenceReadsTheInterfacesALocationPublishes(t *testing.T) {
	v := newVisibility(t)
	kept := v.interfaceOnCell()
	kept.Spec.ClaimRef = nil
	require.NoError(t, v.cell.Update(v.ctx, kept))
	v.publish()

	presence := &NetworkPresenceReconciler{hub: v.hub}
	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = v.projectNamespace
	networkContext.Name = "default-" + testLocationName
	networkContext.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	networkContext.Spec.Location = locationsv1alpha1.LocationReference{Name: testLocationName}

	held, err := presence.heldAtLocation(v.ctx, v.cellNamespace, networkContext)
	require.NoError(t, err)
	require.Equal(t, []string{boundInterfaceName}, held, "the kept interface's copy holds the presence")

	require.NoError(t, v.cell.Get(v.ctx, client.ObjectKeyFromObject(kept), kept))
	require.NoError(t, v.cell.Delete(v.ctx, kept))
	v.publish()

	held, err = presence.heldAtLocation(v.ctx, v.cellNamespace, networkContext)
	require.NoError(t, err)
	require.Empty(t, held, "the copy goes with its interface, and holds nothing then")
}
