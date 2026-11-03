// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// heardWithin bounds how long a test waits for a presence to be brought back.
// A watch event reaches the controller in milliseconds on envtest; only a
// presence that nothing brings back waits this out.
const heardWithin = 10 * time.Second

// runPresence runs the presence controller under a manager of its own, as one
// NSO process runs it, until the returned stop is called. Whatever it meant to
// come back to goes with it, as it goes with a process.
func (s *presenceScenario) runPresence(resync time.Duration) (stop func()) {
	s.t.Helper()
	return s.runReplica(resync, s.reconciler.UnclaimedGracePeriod, false)
}

// Lease timings for replicas that elect a leader, short so a hand-over takes
// well under heardWithin.
const (
	presenceLease      = 2 * time.Second        // [s]
	presenceLeaseRenew = time.Second            // [s]
	presenceLeaseRetry = 200 * time.Millisecond // [s]
	presenceLeaseSpace = "default"              // the namespace the lease lives in
)

// runReplica runs one presence process with its own grace. With elect, its
// controller waits for the lease the replicas share, as NSO's singleton manager
// does, and gives the lease up when stopped.
func (s *presenceScenario) runReplica(resync, grace time.Duration, elect bool) (stop func()) {
	s.t.Helper()
	options := manager.Options{
		Scheme:     s.hub.Scheme(),
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	}
	if elect {
		options.LeaderElection = true
		options.LeaderElectionID = "networkpresence-" + s.hubNamespace
		options.LeaderElectionNamespace = presenceLeaseSpace
		options.LeaderElectionReleaseOnCancel = true
		options.LeaseDuration = ptr.To(presenceLease)
		options.RenewDeadline = ptr.To(presenceLeaseRenew)
		options.RetryPeriod = ptr.To(presenceLeaseRetry)
	}
	mgr, err := manager.New(s.restConfig, options)
	require.NoError(s.t, err)
	presence := &NetworkPresenceReconciler{
		Projects:             s.reconciler.Projects,
		UnclaimedGracePeriod: grace,
		ResyncPeriod:         resync,
	}
	require.NoError(s.t, presence.SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()

	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			require.NoError(s.t, <-done)
		})
	}
	s.t.Cleanup(stop)
	return stop
}

// eventually waits for a condition the running controller brings about. The
// condition must not call require: it runs outside the test's goroutine.
func (s *presenceScenario) eventually(what string, condition func() bool) {
	s.t.Helper()
	require.Eventually(s.t, condition, heardWithin, 50*time.Millisecond, what)
}

// readContext reads the presence's context without failing the test, for a
// condition.
func (s *presenceScenario) readContext() (*networkingv1alpha.NetworkContext, error) {
	var networkContext networkingv1alpha.NetworkContext
	err := s.hub.Get(s.ctx,
		client.ObjectKey{Namespace: s.projectNamespace, Name: s.contextName()}, &networkContext)
	return &networkContext, err
}

// unclaimed builds a presence the hub no longer declares, with its context
// copied onto the hub and waiting out its grace under the running controller.
func (s *presenceScenario) unclaimed() {
	s.t.Helper()
	binding := s.createBinding("consumer-a")
	s.eventually("the presence is written", func() bool {
		networkContext, err := s.readContext()
		return err == nil && networkContext.Labels[networkingv1alpha.NetworkUIDLabel] != ""
	})
	s.replicateContext()

	require.NoError(s.t, s.hub.Delete(s.ctx, binding))
	s.eventually("the presence waits out its grace", func() bool {
		networkContext, err := s.readContext()
		return err == nil &&
			networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation] != ""
	})
}

// replicateContext stands in for the replicator, which copies a presence's
// context onto the hub under the same name, with its network, location and
// network UID labels.
func (s *presenceScenario) replicateContext() {
	s.t.Helper()
	original := s.requireContextLive("the presence is written")
	copied := &networkingv1alpha.NetworkContext{}
	copied.Namespace = s.hubNamespace
	copied.Name = original.Name
	copied.Labels = map[string]string{}
	for _, label := range []string{
		networkingv1alpha.NetworkLabel,
		networkingv1alpha.LocationLabel,
		networkingv1alpha.NetworkUIDLabel,
	} {
		copied.Labels[label] = original.Labels[label]
	}
	copied.Spec = original.Spec
	require.NoError(s.t, s.hub.Create(s.ctx, copied))
}

// networkFinishes reports whether the network's own finalizer is done with its
// contexts, which is what lets a deleting network go.
func (s *presenceScenario) networkFinishes() bool {
	_, err := finalizeNetworkContexts(s.ctx, s.hub, s.network)
	return err == nil
}

// A presence nothing declares comes back when the wait its process set runs
// out. A restart takes that wait with it, so something else must bring the
// presence back, or the network's deletion never reaches it while the network's
// finalizer waits for its context. The first case is the control: the same steps
// in one process.
func TestANetworkDeletedAfterARestartReachesAPresenceNothingDeclares(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "in one process"
		if restart {
			name = "after a restart"
		}
		t.Run(name, func(t *testing.T) {
			s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
			const resync = 2 * time.Second // [s] one process's own wait runs out well inside heardWithin
			stop := s.runPresence(resync)
			s.unclaimed()
			if restart {
				stop()
				s.runPresence(resync)
			}

			s.deleteNetwork()

			s.eventually("the network's deletion ends the presence, and the network can go", s.networkFinishes)
		})
	}
}

// declareInProject writes a binding in the project control plane for the
// presence's pair, which no hub binding mirrors.
func (s *presenceScenario) declareInProject() {
	s.t.Helper()
	binding := &networkingv1alpha.NetworkBinding{}
	binding.Namespace = s.projectNamespace
	binding.Name = "project-plane-binding"
	binding.Spec.Network = networkingv1alpha.NetworkRef{Name: s.networkName}
	binding.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	require.NoError(s.t, s.hub.Create(s.ctx, binding))
}

// writeContextAsTheBindingController writes the context that the binding
// controller writes for a project-plane binding with internal DNS on: the
// network's UID, network and location labels, and the network as controller.
func (s *presenceScenario) writeContextAsTheBindingController() {
	s.t.Helper()
	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = s.projectNamespace
	networkContext.Name = s.contextName()
	networkContext.Labels = map[string]string{
		networkingv1alpha.NetworkUIDLabel: string(s.network.UID),
		networkingv1alpha.NetworkLabel:    s.networkName,
		networkingv1alpha.LocationLabel:   s.locationName,
	}
	networkContext.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: s.networkName}
	networkContext.Spec.Location = locationsv1alpha1.LocationReference{Name: s.locationName}
	networkContext.Spec.IPFamilies = s.network.Spec.IPFamilies
	networkContext.Spec.MTU = s.network.Spec.MTU
	require.NoError(s.t, controllerutil.SetControllerReference(s.network, networkContext, s.hub.Scheme()))
	require.NoError(s.t, s.hub.Create(s.ctx, networkContext))
}

// A presence declared only in its project has no hub binding either, so it too
// waits on a wait its process set. It may also never have had a hub binding:
// the binding controller writes the context for a project-plane binding, and
// the replicator copies it to the hub like any other. Either way the network's
// deletion must reach it after a restart.
func TestANetworkDeletedAfterARestartReachesAPresenceDeclaredInItsProject(t *testing.T) {
	for _, writer := range []string{"the presence controller", "the binding controller"} {
		t.Run("written by "+writer, func(t *testing.T) {
			s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
			const resync = 2 * time.Second // [s] one process's own wait runs out well inside heardWithin
			if writer == "the binding controller" {
				s.declareInProject()
				s.writeContextAsTheBindingController()
				s.replicateContext()
			} else {
				stop := s.runPresence(resync)
				hubBinding := s.createBinding("consumer-a")
				s.eventually("the presence is written", func() bool {
					_, err := s.readContext()
					return err == nil
				})
				s.replicateContext()
				s.declareInProject()
				require.NoError(t, s.hub.Delete(s.ctx, hubBinding))
				time.Sleep(quietWindow) // the presence takes up its wait on the project's binding
				stop()
			}
			s.runPresence(resync)

			s.deleteNetwork()

			s.eventually("the network's deletion ends the presence, and the network can go", s.networkFinishes)
		})
	}
}

// touchCopy changes the presence's hub copy as the replicator does after the
// presence writes its context, and returns the context's version beforehand.
func (s *presenceScenario) touchCopy(times int) string {
	s.t.Helper()
	networkContext := s.requireContextLive("the presence is live")
	for i := 0; i < times; i++ {
		var copied networkingv1alpha.NetworkContext
		require.NoError(s.t, s.hub.Get(s.ctx,
			client.ObjectKey{Namespace: s.hubNamespace, Name: s.contextName()}, &copied))
		patch := client.MergeFrom(copied.DeepCopy())
		copied.Spec.NetworkGeneration++
		require.NoError(s.t, s.hub.Patch(s.ctx, &copied, patch))
	}
	return networkContext.ResourceVersion
}

// A change to the hub copy brings its presence back, and the presence's own
// writes change the copy. So a presence with nothing to change must write
// nothing, or its writes would feed each other.
func TestAPresenceWritesNothingWhenOnlyItsHubCopyChanges(t *testing.T) {
	for _, declared := range []bool{true, false} {
		name := "nothing declares it"
		if declared {
			name = "declared"
		}
		t.Run(name, func(t *testing.T) {
			s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
			s.runPresence(time.Hour)
			if declared {
				s.createBinding("consumer-a")
				s.eventually("the presence is written", func() bool {
					_, err := s.readContext()
					return err == nil
				})
				s.replicateContext()
			} else {
				s.unclaimed()
			}
			time.Sleep(quietWindow) // the presence settles

			before := s.touchCopy(3)
			time.Sleep(quietWindow)

			require.Equal(t, before, s.requireContextLive("the presence is live").ResourceVersion,
				"a copy that changed alone brings no write")
			s.deleteNetwork()
			s.touchCopy(1)
			s.eventually("the copy's change still reaches the presence", func() bool {
				networkContext, err := s.readContext()
				return apierrors.IsNotFound(err) || (err == nil && networkContext.Spec.NetworkDeleting)
			})
		})
	}
}

// The presence reads its project, never the copy, so a copy that goes while its
// presence waits ends nothing, and the presence still hears its network's
// deletion.
func TestAPresenceOutlivesItsHubCopyGoing(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
	const resync = 2 * time.Second // [s] the presence's own wait runs out well inside heardWithin
	s.runPresence(resync)
	s.unclaimed()
	require.NoError(t, s.hub.Delete(s.ctx, &networkingv1alpha.NetworkContext{ObjectMeta: metav1.ObjectMeta{
		Namespace: s.hubNamespace, Name: s.contextName(),
	}}))

	time.Sleep(quietWindow)
	networkContext := s.requireContextLive("the presence still waits out its grace")
	require.NotEmpty(t, networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation])

	s.deleteNetwork()
	s.eventually("the network's deletion still ends the presence", s.networkFinishes)
}

// Replicas wait for one lease, and only its holder acts. The replica that takes
// the lease over starts its controller then, with a cache that already holds the
// copies, and still brings back every presence. The standby here has no grace,
// so it ends the presence the moment it acts.
func TestANewLeaderBringsBackEveryPresence(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
	stopLeader := s.runReplica(time.Hour, time.Hour, true)
	s.unclaimed()
	s.runReplica(time.Hour, 0, true)

	time.Sleep(quietWindow)
	s.requireContextLive("the standby does nothing while another replica holds the lease")

	stopLeader()
	s.eventually("the new leader brings the presence back and ends it", func() bool {
		_, err := s.readContext()
		return apierrors.IsNotFound(err)
	})
}

// A presence nothing declares ends at the end of its grace, whichever process
// is running then.
func TestAPresenceNothingDeclaresEndsAtItsGraceAfterARestart(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: 3 * time.Second})
	stop := s.runPresence(time.Hour)
	s.unclaimed()
	stop()

	s.runPresence(time.Hour)

	s.eventually("the presence ends once its grace is over", func() bool {
		_, err := s.readContext()
		return apierrors.IsNotFound(err)
	})
}

// mirrorNetwork stands in for the replicator, which mirrors every network onto
// the hub and deletes the mirror as soon as the network is deleted.
func (s *presenceScenario) mirrorNetwork() *networkingv1alpha.Network {
	s.t.Helper()
	mirror := &networkingv1alpha.Network{}
	mirror.Namespace = s.hubNamespace
	mirror.Name = s.networkName
	mirror.Spec = s.network.Spec
	require.NoError(s.t, s.hub.Create(s.ctx, mirror))
	return mirror
}

// The hub's mirror of a network goes as soon as the network is deleted, and a
// presence nothing declares hears the deletion from it, not at its next resync.
func TestANetworkDeletionReachesAPresenceNothingDeclaresAtOnce(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{unclaimedGrace: time.Hour})
	mirror := s.mirrorNetwork()
	s.runPresence(time.Hour)
	s.unclaimed()

	s.deleteNetwork()
	require.NoError(t, s.hub.Delete(s.ctx, mirror))

	s.eventually("the network's deletion ends the presence well before its resync", s.networkFinishes)
}

// hubCopy writes the hub copy of a context of network at location into a hub
// namespace.
func (s *presenceScenario) hubCopy(namespace, network, location string) reconcile.Request {
	s.t.Helper()
	copied := &networkingv1alpha.NetworkContext{}
	copied.Namespace = namespace
	copied.Name = networkContextName(network, locationsv1alpha1.LocationReference{Name: location})
	copied.Labels = map[string]string{
		networkingv1alpha.NetworkLabel:  network,
		networkingv1alpha.LocationLabel: location,
	}
	copied.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: network}
	copied.Spec.Location = locationsv1alpha1.LocationReference{Name: location}
	copied.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	copied.Spec.MTU = 1440
	require.NoError(s.t, s.hub.Create(s.ctx, copied))
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(copied)}
}

// A network's mirror maps to its presences in every location, and to no other
// network's, nor to a network of the same name in another project.
func TestANetworkMirrorMapsToItsPresencesInItsNamespace(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	other := &corev1.Namespace{}
	other.Name = "other-" + s.hubNamespace[:40]
	require.NoError(t, s.hub.Create(s.ctx, other))

	want := []reconcile.Request{
		s.hubCopy(s.hubNamespace, s.networkName, "us-east-1"),
		s.hubCopy(s.hubNamespace, s.networkName, "us-central-1"),
	}
	s.hubCopy(s.hubNamespace, "another-network", "us-east-1")
	s.hubCopy(other.Name, s.networkName, "us-east-1")

	presence := &NetworkPresenceReconciler{hub: s.hub}
	got := presence.presencesOfNetworkMirror(s.ctx, s.mirrorNetwork())

	require.ElementsMatch(t, want, got)
}

// Only a mirror's deletion brings presences back. Its creation, in every list a
// start makes, and its edits say nothing a waiting presence reads.
func TestOnlyANetworkMirrorGoingBringsItsPresencesBack(t *testing.T) {
	live := &networkingv1alpha.Network{}
	going := live.DeepCopy()
	going.DeletionTimestamp = ptr.To(metav1.Now())
	going.Finalizers = []string{"test.datumapis.com/hold"}
	passes := networkMirrorGoing()

	require.False(t, passes.Create(event.CreateEvent{Object: live}), "a creation")
	require.False(t, passes.Update(event.UpdateEvent{ObjectOld: live, ObjectNew: live.DeepCopy()}), "an edit")
	require.True(t, passes.Update(event.UpdateEvent{ObjectOld: live, ObjectNew: going}),
		"a deletion that a finalizer holds")
	require.True(t, passes.Delete(event.DeleteEvent{Object: live}), "a deletion")
	require.False(t, passes.Generic(event.GenericEvent{Object: live}), "a generic event")
}
