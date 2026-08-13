// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const lingeringFinalizer = "test.datumapis.com/lingering"

func (s *presenceScenario) gc() *NetworkPresenceGCReconciler {
	return &NetworkPresenceGCReconciler{
		Presence: s.reconciler,
		Projects: s.reconciler.Projects,
	}
}

func (s *presenceScenario) reconcileNetwork() ctrl.Result {
	s.t.Helper()
	result, err := s.gc().Reconcile(s.ctx, mcreconcile.Request{
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: s.projectNamespace,
			Name:      s.networkName,
		}},
		ClusterName: multicluster.ClusterName(testProject),
	})
	require.NoError(s.t, err)
	return result
}

// deleteNetwork removes the network the way a user does, leaving the finalizer
// to decide when the object actually goes.
func (s *presenceScenario) deleteNetwork() {
	s.t.Helper()
	require.NoError(s.t, s.hub.Delete(s.ctx, s.currentNetwork()))
}

func (s *presenceScenario) currentNetwork() *networkingv1alpha.Network {
	s.t.Helper()
	var network networkingv1alpha.Network
	require.NoError(s.t, s.hub.Get(s.ctx,
		client.ObjectKey{Namespace: s.projectNamespace, Name: s.networkName}, &network))
	return &network
}

func (s *presenceScenario) networkExists() bool {
	s.t.Helper()
	var network networkingv1alpha.Network
	err := s.hub.Get(s.ctx,
		client.ObjectKey{Namespace: s.projectNamespace, Name: s.networkName}, &network)
	if apierrors.IsNotFound(err) {
		return false
	}
	require.NoError(s.t, err)
	return true
}

func (s *presenceScenario) bindingExists(name string) bool {
	s.t.Helper()
	var binding networkingv1alpha.NetworkBinding
	err := s.hub.Get(s.ctx, client.ObjectKey{Namespace: s.hubNamespace, Name: name}, &binding)
	if apierrors.IsNotFound(err) {
		return false
	}
	require.NoError(s.t, err)
	return true
}

// strayPresence stands in for the hub objects of some other network, carrying a
// UID this scenario's network never had.
func (s *presenceScenario) strayPresence(networkUID types.UID) (*networkingv1alpha.NetworkContext, *networkingv1alpha.NetworkBinding) {
	s.t.Helper()

	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = s.hubNamespace
	networkContext.Name = "stray-context"
	networkContext.Labels = map[string]string{networkingv1alpha.NetworkUIDLabel: string(networkUID)}
	networkContext.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "stray"}
	networkContext.Spec.Location = networkingv1alpha.LocationReference{Name: s.locationName}
	require.NoError(s.t, s.hub.Create(s.ctx, networkContext))

	binding := &networkingv1alpha.NetworkBinding{}
	binding.Namespace = s.hubNamespace
	binding.Name = "stray-binding"
	binding.Labels = map[string]string{networkingv1alpha.NetworkUIDLabel: string(networkUID)}
	binding.Spec.Network = networkingv1alpha.NetworkRef{Name: "stray"}
	binding.Spec.Location = networkingv1alpha.LocationReference{Name: s.locationName}
	require.NoError(s.t, s.hub.Create(s.ctx, binding))

	return networkContext, binding
}

func TestNetworkPresenceStampsTheNetworkUIDOnEveryBinding(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.createBinding("consumer-b")
	s.reconcile()

	for _, name := range []string{"consumer-a", "consumer-b"} {
		require.Equal(t, string(s.network.UID),
			s.binding(name).Labels[networkingv1alpha.NetworkUIDLabel],
			"binding %q carries no network UID, so garbage collection cannot find it", name)
	}
}

func TestNetworkPresenceGCHoldsALiveNetworkWithAFinalizer(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.reconcileNetwork()

	require.Contains(t, s.currentNetwork().Finalizers, networkPresenceFinalizer)
}

func TestNetworkPresenceGCDeletesEveryHubObjectCarryingTheNetworkUID(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.createBinding("consumer-b")
	s.reconcile()
	require.True(t, mustExist(s.networkContext()))

	s.reconcileNetwork()
	s.deleteNetwork()
	s.reconcileNetwork()

	_, ok := s.networkContext()
	require.False(t, ok, "a deleted network must not orphan its hub presence")
	require.False(t, s.bindingExists("consumer-a"))
	require.False(t, s.bindingExists("consumer-b"))
	require.False(t, s.networkExists(), "the finalizer is released once nothing is left")
}

func TestNetworkPresenceGCKeepsTheFinalizerWhileAHubObjectRemains(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	networkContext.Finalizers = append(networkContext.Finalizers, lingeringFinalizer)
	require.NoError(t, s.hub.Update(s.ctx, networkContext))

	s.reconcileNetwork()
	s.deleteNetwork()
	result := s.reconcileNetwork()

	require.Greater(t, result.RequeueAfter.Nanoseconds(), int64(0),
		"a presence still terminating has to be waited on")
	require.True(t, s.networkExists(), "the network may not go while a presence it made remains")
	require.Contains(t, s.currentNetwork().Finalizers, networkPresenceFinalizer)

	networkContext, ok = s.networkContext()
	require.True(t, ok)
	require.False(t, networkContext.DeletionTimestamp.IsZero(), "the presence is deleting")

	networkContext.Finalizers = nil
	require.NoError(t, s.hub.Update(s.ctx, networkContext))

	s.reconcileNetwork()

	_, ok = s.networkContext()
	require.False(t, ok)
	require.False(t, s.networkExists())
}

// A network deleted and recreated under the same name is a different network
// with a different address space.
func TestNetworkPresenceGCDoesNotAdoptItsPredecessorsPresences(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{mtu: 1460})
	s.createBinding("consumer-a")
	s.reconcile()

	predecessor, ok := s.networkContext()
	require.True(t, ok)
	predecessorUID := string(s.network.UID)
	require.Equal(t, predecessorUID, predecessor.Labels[networkingv1alpha.NetworkUIDLabel])

	require.NoError(t, s.hub.Delete(s.ctx, s.network))

	successor := &networkingv1alpha.Network{}
	successor.Namespace = s.projectNamespace
	successor.Name = s.networkName
	successor.Spec.IPAM.Mode = networkingv1alpha.NetworkIPAMModeAuto
	successor.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	successor.Spec.MTU = 8856
	require.NoError(t, s.hub.Create(s.ctx, successor))
	require.NotEqual(t, predecessorUID, string(successor.UID))

	s.reconcileNetwork()

	predecessor, ok = s.networkContext()
	require.True(t, ok)
	require.Equal(t, predecessorUID, predecessor.Labels[networkingv1alpha.NetworkUIDLabel],
		"converging a successor must not rewrite its predecessor's presence")
	require.Equal(t, int32(1460), predecessor.Spec.MTU)

	s.deleteNetwork()
	s.reconcileNetwork()

	require.False(t, s.networkExists(), "the successor owns no hub objects, so it releases at once")
	_, ok = s.networkContext()
	require.True(t, ok, "the predecessor's presence is not the successor's to delete")
	require.True(t, s.bindingExists("consumer-a"))
}

func TestNetworkPresenceGCLeavesAnotherNetworksHubObjects(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	strayContext, strayBinding := s.strayPresence("11111111-2222-3333-4444-555555555555")

	s.reconcileNetwork()
	s.deleteNetwork()
	s.reconcileNetwork()

	require.False(t, s.networkExists())
	require.NoError(t, s.hub.Get(s.ctx, client.ObjectKeyFromObject(strayContext),
		&networkingv1alpha.NetworkContext{}), "another network's presence was collected")
	require.NoError(t, s.hub.Get(s.ctx, client.ObjectKeyFromObject(strayBinding),
		&networkingv1alpha.NetworkBinding{}), "another network's binding was collected")
}

// The project-plane path is the network controller's, keyed on the owner UID
// index, and this one may not reach into it.
func TestNetworkPresenceGCLeavesProjectPlaneContextsAlone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})

	projectContext := &networkingv1alpha.NetworkContext{}
	projectContext.Namespace = s.projectNamespace
	projectContext.Name = s.contextName()
	projectContext.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: s.networkName}
	projectContext.Spec.Location = networkingv1alpha.LocationReference{Name: s.locationName}
	projectContext.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         networkingv1alpha.GroupVersion.String(),
		Kind:               "Network",
		Name:               s.networkName,
		UID:                s.network.UID,
		Controller:         ptrTo(true),
		BlockOwnerDeletion: ptrTo(true),
	}}
	require.NoError(t, s.hub.Create(s.ctx, projectContext))

	s.reconcileNetwork()
	s.deleteNetwork()
	s.reconcileNetwork()

	require.NoError(t, s.hub.Get(s.ctx, client.ObjectKeyFromObject(projectContext),
		&networkingv1alpha.NetworkContext{}),
		"a project-plane context carries no UID label and belongs to the network controller")
}

// Nothing else would ever rewrite a context once it exists.
func TestNetworkPresenceGCConvergesHubContextsOnANetworkEdit(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{
		families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		mtu:      1460,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	network := s.currentNetwork()
	network.Spec.MTU = 8856
	network.Spec.IPFamilies = []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}
	require.NoError(t, s.hub.Update(s.ctx, network))

	s.reconcileNetwork()

	networkContext, ok := s.networkContext()
	require.True(t, ok)
	require.Equal(t, int32(8856), networkContext.Spec.MTU)
	require.Equal(t, []networkingv1alpha.IPFamily{
		networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv4Protocol,
	}, networkContext.Spec.IPFamilies)
	require.Equal(t, network.Generation, networkContext.Spec.NetworkGeneration)
}

func ptrTo[T any](v T) *T { return &v }
