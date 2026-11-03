// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

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
