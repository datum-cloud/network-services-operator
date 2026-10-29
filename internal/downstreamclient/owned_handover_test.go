package downstreamclient

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/manager/coordinator/sharded"
	"sigs.k8s.io/multicluster-runtime/pkg/manager/coordinator/sharded/sharder"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	"go.datum.net/network-services-operator/internal/config"
)

type pendingEngage struct {
	ctx context.Context
	cl  cluster.Cluster
}

type handoverCoordinator struct {
	mu      sync.Mutex
	owned   map[multicluster.ClusterName]bool
	awares  []multicluster.Aware
	pending map[multicluster.ClusterName]pendingEngage
}

func (c *handoverCoordinator) AddAware(a multicluster.Aware) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.awares = append(c.awares, a)
}

func (c *handoverCoordinator) Engage(ctx context.Context, name multicluster.ClusterName, cl cluster.Cluster) error {
	c.mu.Lock()
	if !c.owned[name] {
		c.pending[name] = pendingEngage{ctx: ctx, cl: cl}
		c.mu.Unlock()
		return nil
	}
	awares := append([]multicluster.Aware(nil), c.awares...)
	c.mu.Unlock()
	for _, a := range awares {
		if err := a.Engage(ctx, name, cl); err != nil {
			return err
		}
	}
	return nil
}

func (c *handoverCoordinator) Runnable() manager.Runnable { return nil }

func (c *handoverCoordinator) Owns(name multicluster.ClusterName) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owned[name]
}

func (c *handoverCoordinator) own(name multicluster.ClusterName) error {
	c.mu.Lock()
	c.owned[name] = true
	p, ok := c.pending[name]
	delete(c.pending, name)
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("cluster %q was never offered", name)
	}
	return c.Engage(p.ctx, name, p.cl)
}

type singleClusterProvider struct {
	name multicluster.ClusterName
	cl   cluster.Cluster
}

func (p singleClusterProvider) Get(_ context.Context, name multicluster.ClusterName) (cluster.Cluster, error) {
	if name == p.name {
		return p.cl, nil
	}
	return nil, multicluster.ErrClusterNotFound
}

func (p singleClusterProvider) IndexField(context.Context, client.Object, string, client.IndexerFunc) error {
	return nil
}

type recordingReconciler struct {
	mu   sync.Mutex
	seen []mcreconcile.Request
}

func (r *recordingReconciler) Reconcile(_ context.Context, req mcreconcile.Request) (reconcile.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, req)
	return reconcile.Result{}, nil
}

func (r *recordingReconciler) reconciled(want mcreconcile.Request) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, req := range r.seen {
		if req == want {
			return true
		}
	}
	return false
}

func TestHandoverReplaysDroppedDownstreamEvent(t *testing.T) {
	runHandover(t, func(_ *rest.Config) (mcmanager.Option, func(*testing.T)) {
		coord := &handoverCoordinator{
			owned:   map[multicluster.ClusterName]bool{},
			pending: map[multicluster.ClusterName]pendingEngage{},
		}
		return mcmanager.WithCoordinator(coord), func(t *testing.T) {
			if err := coord.own(handoverUpstream); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestHandoverReplaysDroppedDownstreamEventWithShardedCoordinator(t *testing.T) {
	runHandover(t, func(cfg *rest.Config) (mcmanager.Option, func(*testing.T)) {
		kube, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
		if err != nil {
			t.Fatal(err)
		}
		self := sharder.PeerInfo{ID: "self", Weight: 1}
		other := sharder.PeerInfo{Weight: 1}
		for i := 0; ; i++ {
			other.ID = fmt.Sprintf("other-%d", i)
			if !sharder.NewHRW().ShouldOwn(string(handoverUpstream), []sharder.PeerInfo{self, other}, self) {
				break
			}
		}
		reg := &mutablePeers{self: self, peers: []sharder.PeerInfo{self, other}}
		coord := sharded.New(kube, ctrl.Log.WithName("sharded-test"),
			sharded.WithPeerRegistry(reg),
			sharded.WithSynchronizationIntervals(200*time.Millisecond, time.Second),
			sharded.WithLeaseTimings(5*time.Second, time.Second, 100*time.Millisecond),
		)
		return mcmanager.WithCoordinator(coord), func(t *testing.T) {
			if coord.Owns(handoverUpstream) {
				t.Fatal("coordinator reports owning a cluster the other peer wins")
			}
			reg.set([]sharder.PeerInfo{self})
			waitFor(t, "coordinator to start the cluster", func() bool {
				return coord.Owns(handoverUpstream)
			})
			var lease coordinationv1.Lease
			if err := kube.Get(context.Background(), client.ObjectKey{Namespace: "kube-system", Name: "mcr-shard-p1"}, &lease); err != nil {
				t.Fatal(err)
			}
			if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != self.ID {
				t.Fatalf("fence lease holder = %v, want %q", lease.Spec.HolderIdentity, self.ID)
			}
		}
	})
}

type mutablePeers struct {
	mu    sync.Mutex
	self  sharder.PeerInfo
	peers []sharder.PeerInfo
}

func (m *mutablePeers) Self() sharder.PeerInfo { return m.self }

func (m *mutablePeers) Snapshot() []sharder.PeerInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sharder.PeerInfo(nil), m.peers...)
}

func (m *mutablePeers) set(p []sharder.PeerInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers = p
}

func (m *mutablePeers) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

const handoverUpstream = multicluster.ClusterName("p1")

func runHandover(t *testing.T, newCoordinator func(*rest.Config) (mcmanager.Option, func(*testing.T))) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	controllerName := "handover-" + t.Name()
	const upstream = handoverUpstream

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	coordOpt, handover := newCoordinator(cfg)
	provider := &singleClusterProvider{name: upstream}
	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:  clientgoscheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	}, coordOpt)
	if err != nil {
		t.Fatal(err)
	}
	local := mgr.GetLocalManager()
	ownership := NewEventOwnership(mgr, config.EventOwnershipEnforce)
	provider.cl = local

	direct, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"upstream", "downstream"} {
		if err := direct.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatal(err)
		}
	}

	mapperCalls := 0
	var mapperMu sync.Mutex
	downstreamSrc, _, err := mcsource.TypedKind(
		&corev1.Secret{},
		OwnedRequestsOnlyFunc(ownership, controllerName, func(_ multicluster.ClusterName, _ cluster.Cluster) handler.TypedEventHandler[*corev1.Secret, mcreconcile.Request] {
			return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, s *corev1.Secret) []mcreconcile.Request {
				mapperMu.Lock()
				mapperCalls++
				mapperMu.Unlock()
				return []mcreconcile.Request{{
					ClusterName: multicluster.ClusterName(UpstreamClusterNameFromLabel(s.Labels[UpstreamOwnerClusterNameLabel])),
					Request: reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: s.Labels[UpstreamOwnerNamespaceLabel],
						Name:      s.Labels[UpstreamOwnerNameLabel],
					}},
				}}
			})
		}),
		predicate.NewTypedPredicateFuncs(func(s *corev1.Secret) bool { return s.Namespace == "downstream" }),
		OwnedByReplica[*corev1.Secret](ownership, nil, controllerName),
	).ForCluster("", local)
	if err != nil {
		t.Fatal(err)
	}

	rec := &recordingReconciler{}
	if err := mcbuilder.ControllerManagedBy(mgr).
		Named(controllerName).
		For(&corev1.ConfigMap{}, mcbuilder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return o.GetNamespace() == "upstream"
		}))).
		WatchesRawSource(downstreamSrc).
		Complete(rec); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Engage(ctx, upstream, local); err != nil {
		t.Fatal(err)
	}

	go func() { _ = mgr.Start(ctx) }()

	if err := direct.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "upstream", Name: "gw"}}); err != nil {
		t.Fatal(err)
	}
	if err := direct.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "downstream",
		Name:      "gw",
		Labels: map[string]string{
			UpstreamOwnerClusterNameLabel: "cluster-p1",
			UpstreamOwnerNamespaceLabel:   "upstream",
			UpstreamOwnerNameLabel:        "gw",
		},
	}}); err != nil {
		t.Fatal(err)
	}

	want := mcreconcile.Request{
		ClusterName: upstream,
		Request:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "upstream", Name: "gw"}},
	}

	waitFor(t, "downstream event dropped as not owned", func() bool {
		return filtered(controllerName, FilterReasonNotOwned, metricModeEnforce) >= 1
	})
	time.Sleep(time.Second)
	if rec.reconciled(want) {
		t.Fatal("upstream object reconciled while another replica owned its cluster")
	}
	mapperMu.Lock()
	if mapperCalls != 0 {
		t.Errorf("mapper ran %d times for a not-owned cluster, want 0", mapperCalls)
	}
	mapperMu.Unlock()

	handover(t)

	waitFor(t, "upstream object reconciled after handover", func() bool {
		return rec.reconciled(want)
	})

	if err := direct.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "downstream",
		Name:      "gw-after",
		Labels: map[string]string{
			UpstreamOwnerClusterNameLabel: "cluster-p1",
			UpstreamOwnerNamespaceLabel:   "upstream",
			UpstreamOwnerNameLabel:        "gw-after",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	wantAfter := mcreconcile.Request{
		ClusterName: upstream,
		Request:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "upstream", Name: "gw-after"}},
	}
	waitFor(t, "event for the now-owned cluster reconciled", func() bool {
		return rec.reconciled(wantAfter)
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
