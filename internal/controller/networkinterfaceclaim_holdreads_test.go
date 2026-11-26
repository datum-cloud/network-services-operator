// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/recorder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// staleHoldCache answers every hold read the way a cache that has not seen the
// hold yet does.
type staleHoldCache struct {
	client.Client
}

func (c staleHoldCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*networkingv1alpha.SwitchPortHold); ok {
		return apierrors.NewNotFound(networkingv1alpha.GroupVersion.WithResource("switchportholds").GroupResource(), key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type cachedCluster struct {
	cluster.Cluster
	cached client.Client
	live   client.Reader
}

func (c *cachedCluster) GetClient() client.Client                       { return c.cached }
func (c *cachedCluster) GetAPIReader() client.Reader                    { return c.live }
func (c *cachedCluster) GetEventRecorder(string) recorder.EventRecorder { return nil }

type oneClusterManager struct {
	mcmanager.Manager
	cluster cluster.Cluster
}

func (m *oneClusterManager) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return m.cluster, nil
}

// reconcileThroughManager runs the reconciler's entry point against a cluster
// whose cache has never seen a hold, so only reads made through the API reader
// find one.
func (s *scenario) reconcileThroughManager(name string) (reconcile.Result, error) {
	s.t.Helper()
	reconciler := *s.reconciler
	reconciler.mgr = &oneClusterManager{cluster: &cachedCluster{cached: staleHoldCache{s.client}, live: s.client}}
	return reconciler.Reconcile(s.ctx, mcreconcile.Request{
		Request: reconcile.Request{NamespacedName: client.ObjectKey{Namespace: s.namespace, Name: name}},
	})
}

func TestReleaseReadsTheHoldFromTheAPIServer(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	claim, _ := boundPhysicalClaim(s)
	require.NoError(t, s.client.Delete(s.ctx, claim))

	_, err := s.reconcileThroughManager(physicalClaimName)
	require.NoError(t, err)
	s.requireAwaitingDetach(physicalClaimName)
}

func TestRecordingReadsTheHoldFromTheAPIServer(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	other := s.sibling(otherProject)
	holder := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(holder)
	other.createClaim("their-data", other.physicalSpec("Ethernet1/12", ""))

	_, err := other.reconcileThroughManager("their-data")
	require.NoError(t, err)
	other.requireWaiting("their-data")
}

type claimReadCounter struct {
	client.Reader
	claimReads atomic.Int32
}

func (c *claimReadCounter) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*networkingv1alpha.NetworkInterfaceClaim); ok {
		c.claimReads.Add(1)
	}
	return c.Reader.Get(ctx, key, obj, opts...)
}

func TestARecordedClaimReadsNoOtherClaims(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	second := s.sibling(testProject)
	first := s.createClaim("server-data", s.physicalSpec("Ethernet1/12", ""))
	s.reconcile(first)
	second.reconcile(second.createClaim("server-storage", second.physicalSpec("Ethernet1/12", "")))
	require.Len(t, s.requireHold("Ethernet1/12").Spec.NetworkClaims, 2)

	counter := &claimReadCounter{Reader: s.client}
	_, err := s.reconciler.reconcileClaim(s.ctx, s.client, counter, s.events, client.ObjectKeyFromObject(first))
	require.NoError(t, err)
	require.Zero(t, counter.claimReads.Load(), "a claim already recorded must not re-check every other entry")
}

func TestAWaitingClaimRetriesSoon(t *testing.T) {
	s := newScenario(t, true, ipv4Only)
	other := s.sibling(otherProject)
	s.reconcile(s.createClaim("server-data", s.physicalSpec("Ethernet1/12", "")))
	waiting := other.createClaim("their-data", other.physicalSpec("Ethernet1/12", ""))

	result, err := s.reconciler.reconcileClaim(s.ctx, s.client, s.client, s.events, client.ObjectKeyFromObject(waiting))
	require.NoError(t, err)
	other.requireWaiting("their-data")
	require.Equal(t, 10*time.Second, result.RequeueAfter)
}
