// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// syncReconciler reads the hub through a client carrying the namespace index the
// real one is set up with, seeded from the hub this scenario built, and reads the
// project plane through the scenario's hub. It returns that hub client too, so a
// test can read the signals written there.
func (s *presenceScenario) syncReconciler() (*NetworkPresenceSyncReconciler, client.Client) {
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

	return &NetworkPresenceSyncReconciler{
		Projects: &staticProjectResolver{clients: map[string]client.Client{testProject: s.hub}},
		hub:      hub,
	}, hub
}

// signals reads the observation each hub binding carries, by binding name.
func (s *presenceScenario) signals(hub client.Client) map[string]string {
	s.t.Helper()

	var bindings networkingv1alpha.NetworkBindingList
	require.NoError(s.t, hub.List(s.ctx, &bindings, client.InNamespace(s.hubNamespace)))

	observed := map[string]string{}
	for i := range bindings.Items {
		if value, ok := bindings.Items[i].Annotations[NetworkPresenceObservedAnnotation]; ok {
			observed[bindings.Items[i].Name] = value
		}
	}
	return observed
}

func (s *presenceScenario) networkRequest() mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: testProject,
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: s.projectNamespace,
			Name:      s.networkName,
		}},
	}
}

// A network edited after a location already carries it has to reach that
// location. The presence controller reads the network in a control plane it does
// not watch, so without this the edit is invisible to it.
//
// The context is ready first on purpose: a refused presence comes back on its
// own retry, so a test that left it refused would pass whether or not anything
// was enqueued.
func TestNetworkPresenceSyncSignalsANetworkEditAgainstAReadyContext(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{
		families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		mtu:      1440,
	})
	s.createBinding("consumer-a")
	s.reconcile()
	s.markContextReady()

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter,
		"a ready presence has nothing behind it, so only a signal can bring it back")

	s.network.Spec.MTU = 8856
	require.NoError(t, s.hub.Update(s.ctx, s.network))

	sync, hub := s.syncReconciler()

	_, err = sync.reconcileNetwork(s.ctx, s.networkRequest())
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		"consumer-a": "Network/" + s.networkName + "/" + string(s.network.UID) + "/" + s.network.ResourceVersion,
	}, s.signals(hub),
		"the edited network has to be recorded on the binding the presence controller watches")

	// The binding event maps to the presence, which carries the edit through.
	_, err = s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(8856), networkContext.Spec.MTU)
}

// Every binding declaring the network in this project is signalled, and nothing
// declaring a different network is.
func TestNetworkPresenceSyncSignalsEveryBindingOfTheNetwork(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.createBinding("consumer-b")

	other := &networkingv1alpha.NetworkBinding{}
	other.Namespace = s.hubNamespace
	other.Name = "consumer-elsewhere"
	other.Spec.Network = networkingv1alpha.NetworkRef{Name: "another-network"}
	other.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	require.NoError(t, s.hub.Create(s.ctx, other))

	sync, hub := s.syncReconciler()

	_, err := sync.reconcileNetwork(s.ctx, s.networkRequest())
	require.NoError(t, err)

	observed := s.signals(hub)
	require.Len(t, observed, 2, "another network's binding is not this network's")
	require.Contains(t, observed, "consumer-a")
	require.Contains(t, observed, "consumer-b")
	require.Equal(t, observed["consumer-a"], observed["consumer-b"])
}

// A network in a project namespace no hub namespace stands for is nothing this
// controller can map, and must not become an event for some other project's
// presence.
func TestNetworkPresenceSyncIgnoresANetworkNoHubNamespaceStandsFor(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	sync, hub := s.syncReconciler()

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

	require.Empty(t, s.signals(hub))
}

// The context finishing being deleted is the event that unwedges a presence
// whose context was terminating. There is nothing left to read by then, so the
// mapping has to come from the request alone.
func TestNetworkPresenceSyncSignalsADeletedContext(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	sync, hub := s.syncReconciler()

	_, err := sync.reconcileNetworkContext(s.ctx, mcreconcile.Request{
		ClusterName: testProject,
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: s.projectNamespace,
			Name:      s.contextName(),
		}},
	})
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		"consumer-a": "NetworkContext/" + s.contextName() + "/gone",
	}, s.signals(hub))
}

// A resync of an object that has not changed must not write the binding again,
// or every resync would wake the presence controller for every binding.
func TestNetworkPresenceSyncLeavesAnUnchangedObservationAlone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")

	sync, hub := s.syncReconciler()

	_, err := sync.reconcileNetwork(s.ctx, s.networkRequest())
	require.NoError(t, err)

	binding := &networkingv1alpha.NetworkBinding{}
	require.NoError(t, hub.Get(s.ctx, client.ObjectKey{Namespace: s.hubNamespace, Name: "consumer-a"}, binding))
	resourceVersion := binding.ResourceVersion

	_, err = sync.reconcileNetwork(s.ctx, s.networkRequest())
	require.NoError(t, err)

	require.NoError(t, hub.Get(s.ctx, client.ObjectKey{Namespace: s.hubNamespace, Name: "consumer-a"}, binding))
	require.Equal(t, resourceVersion, binding.ResourceVersion)
}
