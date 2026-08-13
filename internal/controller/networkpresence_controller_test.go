// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	// withoutLocationBinding skips the LocationBinding the project needs to use
	// the location.
	withoutLocationBinding bool
	// withoutNetwork skips creating the Network in the project.
	withoutNetwork bool
	// families overrides the network's address families.
	families []networkingv1alpha.IPFamily
	// mtu overrides the network's MTU.
	mtu int32
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

	if !opts.withoutLocationBinding {
		locationBinding := &networkingv1alpha.LocationBinding{}
		locationBinding.Name = s.locationName
		locationBinding.Spec.LocationRef = corev1.LocalObjectReference{Name: s.locationName}
		locationBinding.Spec.LocationClassName = "datum-managed"
		require.NoError(t, cl.Create(ctx, locationBinding))
		t.Cleanup(func() { _ = cl.Delete(ctx, locationBinding) })
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
		Projects: &staticProjectResolver{clients: map[string]client.Client{testProject: cl}},
		hub:      cl,
	}

	return s
}

// contextName is the deterministic name every binding for the pair resolves to.
func (s *presenceScenario) contextName() string {
	return networkContextName(s.networkName, networkingv1alpha.LocationReference{
		Name:      s.locationName,
		Namespace: networkingv1alpha.LocationReferenceDefaultNamespace,
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
	binding.Spec.Location = networkingv1alpha.LocationReference{Name: s.locationName}
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

func (s *presenceScenario) networkContext() (*networkingv1alpha.NetworkContext, bool) {
	s.t.Helper()
	var networkContext networkingv1alpha.NetworkContext
	err := s.hub.Get(s.ctx, s.request().NamespacedName, &networkContext)
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
	require.Equal(t, s.hubNamespace, binding.Status.NetworkContextRef.Namespace)
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
	s := newPresenceScenario(t, presenceOptions{withoutLocationBinding: true})
	s.createBinding("consumer-a")
	s.reconcile()

	requireReady(t, s.binding("consumer-a"), metav1.ConditionFalse,
		networkingv1alpha.NetworkBindingReasonLocationNotAvailable)

	_, ok := s.networkContext()
	require.False(t, ok, "no presence may be created in a location the project cannot use")
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
	require.NoError(t, s.hub.List(s.ctx, &contexts, client.InNamespace(s.hubNamespace)))
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
