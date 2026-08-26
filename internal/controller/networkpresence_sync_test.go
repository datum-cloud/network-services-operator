// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// syncReconciler reads the hub through a client carrying the namespace index the
// real one is set up with, seeded from the hub this scenario built.
func (s *presenceScenario) syncReconciler(events chan event.GenericEvent) *NetworkPresenceSyncReconciler {
	s.t.Helper()

	var namespaces corev1.NamespaceList
	require.NoError(s.t, s.hub.List(s.ctx, &namespaces))
	var bindings networkingv1alpha.NetworkBindingList
	require.NoError(s.t, s.hub.List(s.ctx, &bindings))

	objects := make([]client.Object, 0, len(namespaces.Items)+len(bindings.Items))
	for i := range namespaces.Items {
		objects = append(objects, &namespaces.Items[i])
	}
	for i := range bindings.Items {
		objects = append(objects, &bindings.Items[i])
	}

	hub := fake.NewClientBuilder().
		WithScheme(s.hub.Scheme()).
		WithIndex(&corev1.Namespace{}, hubNamespaceProjectIndex, hubNamespaceProjectIndexFunc).
		WithObjects(objects...).
		Build()

	return &NetworkPresenceSyncReconciler{Events: events, hub: hub}
}

func drainPresenceEvents(t *testing.T, events chan event.GenericEvent) []client.ObjectKey {
	t.Helper()

	keys := make([]client.ObjectKey, 0, len(events))
	for {
		select {
		case ev := <-events:
			keys = append(keys, client.ObjectKeyFromObject(ev.Object))
		default:
			return keys
		}
	}
}

// A network edited after a location already carries it has to reach that
// location. The presence controller reads the network in a control plane it does
// not watch, so without this the edit is invisible to it.
//
// The context is ready first on purpose: a refused presence comes back on its
// own retry, so a test that left it refused would pass whether or not anything
// was enqueued.
func TestNetworkPresenceSyncEnqueuesANetworkEditAgainstAReadyContext(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{
		families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		mtu:      1460,
	})
	s.createBinding("consumer-a")
	s.reconcile()
	s.markContextReady()

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter,
		"a ready presence has nothing behind it, so only an event can bring it back")

	s.network.Spec.MTU = 8856
	require.NoError(t, s.hub.Update(s.ctx, s.network))

	events := make(chan event.GenericEvent, 8)
	sync := s.syncReconciler(events)

	_, err = sync.reconcileNetwork(s.ctx, mcreconcile.Request{
		ClusterName: testProject,
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: s.projectNamespace,
			Name:      s.networkName,
		}},
	})
	require.NoError(t, err)

	require.Equal(t, []client.ObjectKey{{
		Namespace: s.hubNamespace,
		Name:      s.contextName(),
	}}, drainPresenceEvents(t, events),
		"the edited network's presence has to be enqueued on the hub, where the presence is keyed")

	// The key is enough to carry the edit through.
	_, err = s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(8856), networkContext.Spec.MTU)
}

// Every consumer of the network in this project is enqueued, once each, and
// nothing declaring a different network is.
func TestNetworkPresenceSyncEnqueuesEveryPresenceOfTheNetworkOnce(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.createBinding("consumer-b")

	other := &networkingv1alpha.NetworkBinding{}
	other.Namespace = s.hubNamespace
	other.Name = "consumer-elsewhere"
	other.Spec.Network = networkingv1alpha.NetworkRef{Name: "another-network"}
	other.Spec.Location = networkingv1alpha.LocationReference{Name: s.locationName}
	require.NoError(t, s.hub.Create(s.ctx, other))

	events := make(chan event.GenericEvent, 8)
	sync := s.syncReconciler(events)

	_, err := sync.reconcileNetwork(s.ctx, mcreconcile.Request{
		ClusterName: testProject,
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: s.projectNamespace,
			Name:      s.networkName,
		}},
	})
	require.NoError(t, err)

	require.Equal(t, []client.ObjectKey{{
		Namespace: s.hubNamespace,
		Name:      s.contextName(),
	}}, drainPresenceEvents(t, events),
		"two consumers of one pair are one presence, and another network's is not this network's")
}

// A network in a project namespace no hub namespace stands for is nothing this
// controller can map, and must not become an event for some other project's
// presence.
func TestNetworkPresenceSyncIgnoresANetworkNoHubNamespaceStandsFor(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	events := make(chan event.GenericEvent, 8)
	sync := s.syncReconciler(events)

	for _, req := range []mcreconcile.Request{
		{
			ClusterName: "some-other-project",
			Request: ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: s.projectNamespace,
				Name:      s.networkName,
			}},
		},
		{
			ClusterName: testProject,
			Request: ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: "some-other-namespace",
				Name:      s.networkName,
			}},
		},
	} {
		_, err := sync.reconcileNetwork(s.ctx, req)
		require.NoError(t, err)
	}

	require.Empty(t, drainPresenceEvents(t, events))
}

// The context finishing being deleted is the event that unwedges a presence
// whose context was terminating. There is nothing left to read by then, so the
// mapping has to come from the request alone.
func TestNetworkPresenceSyncEnqueuesADeletedContext(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	events := make(chan event.GenericEvent, 8)
	sync := s.syncReconciler(events)

	_, err := sync.reconcileNetworkContext(s.ctx, mcreconcile.Request{
		ClusterName: testProject,
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: s.projectNamespace,
			Name:      s.contextName(),
		}},
	})
	require.NoError(t, err)

	require.Equal(t, []client.ObjectKey{{
		Namespace: s.hubNamespace,
		Name:      s.contextName(),
	}}, drainPresenceEvents(t, events))
}

// Nothing drains the channel unless the presence controller is running in this
// process. Blocking there would stall this controller behind a reader that may
// never arrive, so a full channel drops.
func TestNetworkPresenceSyncDropsRatherThanBlockingOnAFullChannel(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	// Unbuffered and unread: every send finds no reader.
	events := make(chan event.GenericEvent)
	sync := s.syncReconciler(events)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := sync.reconcileNetworkContext(s.ctx, mcreconcile.Request{
			ClusterName: testProject,
			Request: ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: s.projectNamespace,
				Name:      s.contextName(),
			}},
		})
		require.NoError(t, err)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the sync controller blocked on a channel nothing is reading")
	}
}
