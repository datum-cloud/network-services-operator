package downstreamclient

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	"go.datum.net/network-services-operator/internal/config"
)

// fakeOwner knows the clusters it has keys for and owns those mapped to true.
type fakeOwner map[multicluster.ClusterName]bool

func (f fakeOwner) Owns(name multicluster.ClusterName) bool {
	if name == "" {
		return true
	}
	return f[name]
}

func (f fakeOwner) GetCluster(_ context.Context, name multicluster.ClusterName) (cluster.Cluster, error) {
	if _, ok := f[name]; ok || name == "" {
		return nil, nil
	}
	return nil, multicluster.ErrClusterNotFound
}

var replicaOwner = fakeOwner{ownedCluster: true, notOwnedCluster: false}

var enforced = NewEventOwnership(replicaOwner, config.EventOwnershipEnforce)

func filtered(controller, reason, mode string) float64 {
	return testutil.ToFloat64(downstreamEventsFiltered.WithLabelValues(controller, reason, mode))
}

type recordingQueue[R comparable] struct {
	workqueue.TypedRateLimitingInterface[R]
	mu   sync.Mutex
	adds []R
}

func (q *recordingQueue[R]) Add(item R) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.adds = append(q.adds, item)
}

func (q *recordingQueue[R]) AddAfter(item R, _ time.Duration) { q.Add(item) }

func (q *recordingQueue[R]) AddRateLimited(item R) { q.Add(item) }

func (q *recordingQueue[R]) added() []R {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]R(nil), q.adds...)
}

type recordingPriorityQueue[R comparable] struct {
	recordingQueue[R]
	priorities []int
}

func (q *recordingPriorityQueue[R]) AddWithOpts(o priorityqueue.AddOpts, items ...R) {
	for _, item := range items {
		q.Add(item)
		p := 0
		if o.Priority != nil {
			p = *o.Priority
		}
		q.priorities = append(q.priorities, p)
	}
}

func (q *recordingPriorityQueue[R]) GetWithPriority() (R, int, bool) {
	var zero R
	return zero, 0, true
}

const (
	ownedCluster    = multicluster.ClusterName("p1")
	notOwnedCluster = multicluster.ClusterName("p2")
)

func labeledSecret(namespace string, labels map[string]string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "s",
		Namespace: namespace,
		Labels:    labels,
	}}
}

func namespaceReader(t *testing.T, namespaces ...*corev1.Namespace) client.Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objs := make([]client.Object, 0, len(namespaces))
	for _, ns := range namespaces {
		objs = append(objs, ns)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestOwnedByReplica(t *testing.T) {
	reader := namespaceReader(t,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "ns-owned",
			Labels: map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p1"},
		}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "ns-not-owned",
			Labels: map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"},
		}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-unlabeled"}},
	)
	ownership := enforced

	cases := []struct {
		name string
		obj  *corev1.Secret
		want bool
	}{
		{
			name: "owned label",
			obj:  labeledSecret("ns-unlabeled", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p1"}),
			want: true,
		},
		{
			name: "not owned label",
			obj:  labeledSecret("ns-unlabeled", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"}),
			want: false,
		},
		{
			name: "legacy encoding owned",
			obj:  labeledSecret("ns-unlabeled", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-_p1"}),
			want: true,
		},
		{
			name: "object label wins over namespace label",
			obj:  labeledSecret("ns-owned", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"}),
			want: false,
		},
		{
			name: "no label, namespace owned",
			obj:  labeledSecret("ns-owned", nil),
			want: true,
		},
		{
			name: "no label, namespace not owned",
			obj:  labeledSecret("ns-not-owned", nil),
			want: false,
		},
		{
			name: "neither labeled passes",
			obj:  labeledSecret("ns-unlabeled", nil),
			want: true,
		},
		{
			name: "namespace missing passes",
			obj:  labeledSecret("ns-gone", nil),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := OwnedByReplica[*corev1.Secret](ownership, reader, "test-predicate-table")
			if got := p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: tc.obj}); got != tc.want {
				t.Errorf("Create = %v, want %v", got, tc.want)
			}
			if got := p.Delete(event.TypedDeleteEvent[*corev1.Secret]{Object: tc.obj}); got != tc.want {
				t.Errorf("Delete = %v, want %v", got, tc.want)
			}
			if got := p.Generic(event.TypedGenericEvent[*corev1.Secret]{Object: tc.obj}); got != tc.want {
				t.Errorf("Generic = %v, want %v", got, tc.want)
			}
			if got := p.Update(event.TypedUpdateEvent[*corev1.Secret]{ObjectOld: tc.obj, ObjectNew: tc.obj}); got != tc.want {
				t.Errorf("Update = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOwnedByReplicaUpdatePassesIfEitherSideOwned(t *testing.T) {
	ownership := enforced
	owned := labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p1"})
	notOwned := labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})
	p := OwnedByReplica[*corev1.Secret](ownership, nil, "test-predicate-update")

	if !p.Update(event.TypedUpdateEvent[*corev1.Secret]{ObjectOld: notOwned, ObjectNew: owned}) {
		t.Error("relabel into owned cluster was dropped")
	}
	if !p.Update(event.TypedUpdateEvent[*corev1.Secret]{ObjectOld: owned, ObjectNew: notOwned}) {
		t.Error("relabel out of owned cluster was dropped")
	}
	if p.Update(event.TypedUpdateEvent[*corev1.Secret]{ObjectOld: notOwned, ObjectNew: notOwned}) {
		t.Error("update with both sides not owned passed")
	}
}

func TestOwnedByReplicaWithLabel(t *testing.T) {
	const label = "example.com/cluster"
	decode := func(v string) string { return v[len("x-"):] }
	p := OwnedByReplicaWithLabel[*corev1.Secret](enforced, nil, "test-predicate-custom", label, decode)

	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{label: "x-p1"})}) {
		t.Error("owned custom label dropped")
	}
	if p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{label: "x-p2"})}) {
		t.Error("not-owned custom label passed")
	}
	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})}) {
		t.Error("custom-label predicate consulted the default label")
	}
}

func TestOwnedByReplicaCountsFilteredEvents(t *testing.T) {
	const controller = "test-predicate-counter"
	p := OwnedByReplica[*corev1.Secret](enforced, nil, controller)

	p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p1"})})
	p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})})
	p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})})

	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 2 {
		t.Errorf("not_owned = %v, want 2", got)
	}

	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", nil)}) {
		t.Fatal("unlabeled event dropped")
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 2 {
		t.Errorf("not_owned after passing unlabeled event = %v, want 2", got)
	}
	assertOnlyKnownReasons(t)
}

func assertOnlyKnownReasons(t *testing.T) {
	t.Helper()
	ch := make(chan prometheus.Metric, 1024)
	downstreamEventsFiltered.Collect(ch)
	close(ch)
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		for _, l := range pb.GetLabel() {
			if l.GetName() != "reason" {
				continue
			}
			switch l.GetValue() {
			case FilterReasonNotOwned, FilterReasonUnverified:
			default:
				t.Errorf("found series with reason %q", l.GetValue())
			}
		}
	}
}

func request(cluster multicluster.ClusterName, name string) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: cluster,
		Request:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: name}},
	}
}

func TestOwnedRequestsOnlyDropsNotOwned(t *testing.T) {
	const controller = "test-wrapper"
	inner := handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, _ *corev1.Secret) []mcreconcile.Request {
		return []mcreconcile.Request{
			request(ownedCluster, "a"),
			request(notOwnedCluster, "b"),
			request("", "local"),
		}
	})
	h := OwnedRequestsOnly[*corev1.Secret, mcreconcile.Request](enforced, controller, inner)

	q := &recordingQueue[mcreconcile.Request]{}
	h.Create(context.Background(), event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", nil)}, q)

	got := q.added()
	if len(got) != 2 {
		t.Fatalf("adds = %v, want 2", got)
	}
	for _, req := range got {
		if req.ClusterName == notOwnedCluster {
			t.Errorf("not-owned request %v enqueued", req)
		}
	}
	if n := filtered(controller, FilterReasonNotOwned, metricModeEnforce); n != 1 {
		t.Errorf("not_owned = %v, want 1", n)
	}
}

func TestOwnedRequestsOnlyPreservesPriorityQueue(t *testing.T) {
	inner := handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, _ *corev1.Secret) []mcreconcile.Request {
		return []mcreconcile.Request{request(ownedCluster, "a"), request(notOwnedCluster, "b")}
	})
	h := OwnedRequestsOnly[*corev1.Secret, mcreconcile.Request](enforced, "test-wrapper-priority", inner)

	q := &recordingPriorityQueue[mcreconcile.Request]{}
	h.Create(context.Background(), event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", nil), IsInInitialList: true}, q)

	got := q.added()
	if len(got) != 1 || got[0].ClusterName != ownedCluster {
		t.Fatalf("adds = %v, want only the owned request", got)
	}
	if len(q.priorities) != 1 || q.priorities[0] != handler.LowPriority {
		t.Errorf("priorities = %v, want [%d]", q.priorities, handler.LowPriority)
	}
}

type fakeCluster struct {
	cluster.Cluster
	cache *informertest.FakeInformers
}

func (c fakeCluster) GetCache() crcache.Cache { return c.cache }

func runSource(t *testing.T, ownership EventOwnership, controller string, labels map[string]string) (int, []mcreconcile.Request) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	informers := &informertest.FakeInformers{Scheme: scheme}

	var mu sync.Mutex
	calls := 0
	mapper := func(_ multicluster.ClusterName, _ cluster.Cluster) handler.TypedEventHandler[*corev1.Secret, mcreconcile.Request] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, s *corev1.Secret) []mcreconcile.Request {
			mu.Lock()
			calls++
			mu.Unlock()
			return []mcreconcile.Request{request(multicluster.ClusterName(UpstreamClusterNameFromLabel(s.Labels[UpstreamOwnerClusterNameLabel])), s.Name)}
		})
	}

	src, _, err := mcsource.TypedKind(
		&corev1.Secret{},
		OwnedRequestsOnlyFunc(ownership, controller, mapper),
		OwnedByReplica[*corev1.Secret](ownership, nil, controller),
	).ForCluster("", fakeCluster{cache: informers})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q := &recordingQueue[mcreconcile.Request]{}
	if err := src.Start(ctx, q); err != nil {
		t.Fatal(err)
	}

	inf, err := informers.FakeInformerFor(ctx, &corev1.Secret{})
	if err != nil {
		t.Fatal(err)
	}
	inf.Add(labeledSecret("ns", labels))

	mu.Lock()
	defer mu.Unlock()
	return calls, q.added()
}

func TestSourceSkipsMapperForNotOwnedCluster(t *testing.T) {
	ownership := enforced

	calls, adds := runSource(t, ownership, "test-source", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})
	if calls != 0 || len(adds) != 0 {
		t.Errorf("not owned: mapper calls = %d, adds = %v; want 0 and none", calls, adds)
	}

	calls, adds = runSource(t, ownership, "test-source", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p1"})
	if calls != 1 || len(adds) != 1 {
		t.Errorf("owned: mapper calls = %d, adds = %v; want 1 and 1", calls, adds)
	}
}

func TestOwnedByReplicaPassesUnverifiedCluster(t *testing.T) {
	const controller = "test-predicate-unknown"
	p := OwnedByReplica[*corev1.Secret](enforced, nil, controller)

	garbage := labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "garbage-value"})
	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: garbage}) {
		t.Fatal("event for a cluster not registered here was dropped")
	}
	if got := filtered(controller, FilterReasonUnverified, metricModeEnforce); got != 1 {
		t.Errorf("unverified = %v, want 1", got)
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 0 {
		t.Errorf("not_owned = %v, want 0", got)
	}

	if p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})}) {
		t.Error("known, not-owned cluster passed")
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 1 {
		t.Errorf("not_owned = %v, want 1", got)
	}
	if got := filtered(controller, FilterReasonUnverified, metricModeEnforce); got != 1 {
		t.Errorf("unverified after known drop = %v, want 1", got)
	}
}

func TestOwnedRequestsOnlyPassesUnverifiedCluster(t *testing.T) {
	const controller = "test-wrapper-unknown"
	inner := handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, _ *corev1.Secret) []mcreconcile.Request {
		return []mcreconcile.Request{request("garbage", "a"), request(notOwnedCluster, "b")}
	})
	h := OwnedRequestsOnly[*corev1.Secret, mcreconcile.Request](enforced, controller, inner)

	q := &recordingQueue[mcreconcile.Request]{}
	h.Create(context.Background(), event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", nil)}, q)

	got := q.added()
	if len(got) != 1 || got[0].ClusterName != "garbage" {
		t.Fatalf("adds = %v, want only the unverified request", got)
	}
	if n := filtered(controller, FilterReasonUnverified, metricModeEnforce); n != 1 {
		t.Errorf("unverified = %v, want 1", n)
	}
	if n := filtered(controller, FilterReasonNotOwned, metricModeEnforce); n != 1 {
		t.Errorf("not_owned = %v, want 1", n)
	}
}

func TestDisabledEventOwnershipChangesNothing(t *testing.T) {
	const controller = "test-mode-disabled"
	disabled := NewEventOwnership(replicaOwner, config.EventOwnershipDisabled)

	calls, adds := runSource(t, disabled, controller, map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})
	if calls != 1 || len(adds) != 1 {
		t.Errorf("mapper calls = %d, adds = %v; want 1 and 1", calls, adds)
	}
	calls, adds = runSource(t, disabled, controller, map[string]string{UpstreamOwnerClusterNameLabel: "garbage"})
	if calls != 1 || len(adds) != 1 {
		t.Errorf("unknown cluster: mapper calls = %d, adds = %v; want 1 and 1", calls, adds)
	}
	for _, mode := range []string{metricModeShadow, metricModeEnforce} {
		for _, reason := range []string{FilterReasonNotOwned, FilterReasonUnverified} {
			if got := filtered(controller, reason, mode); got != 0 {
				t.Errorf("%s/%s = %v, want 0", reason, mode, got)
			}
		}
	}
}

func TestShadowEventOwnershipCountsButDropsNothing(t *testing.T) {
	const controller = "test-mode-shadow"
	shadow := NewEventOwnership(replicaOwner, config.EventOwnershipShadow)

	p := OwnedByReplica[*corev1.Secret](shadow, nil, controller)
	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})}) {
		t.Error("shadow predicate dropped a not-owned event")
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeShadow); got != 1 {
		t.Errorf("predicate not_owned/shadow = %v, want 1", got)
	}

	inner := handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, _ *corev1.Secret) []mcreconcile.Request {
		return []mcreconcile.Request{request(ownedCluster, "a"), request(notOwnedCluster, "b")}
	})
	q := &recordingQueue[mcreconcile.Request]{}
	OwnedRequestsOnly[*corev1.Secret, mcreconcile.Request](shadow, controller, inner).
		Create(context.Background(), event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", nil)}, q)
	if got := q.added(); len(got) != 2 {
		t.Errorf("shadow wrapper adds = %v, want both requests", got)
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeShadow); got != 2 {
		t.Errorf("not_owned/shadow after wrapper = %v, want 2", got)
	}

	calls, adds := runSource(t, shadow, controller, map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})
	if calls != 1 || len(adds) != 1 {
		t.Errorf("source: mapper calls = %d, adds = %v; want 1 and 1", calls, adds)
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 0 {
		t.Errorf("not_owned/enforce = %v, want 0 in shadow mode", got)
	}
}

func TestEnforceEventOwnershipCountsWithEnforceMode(t *testing.T) {
	const controller = "test-mode-enforce"
	calls, adds := runSource(t, enforced, controller, map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})
	if calls != 0 || len(adds) != 0 {
		t.Errorf("mapper calls = %d, adds = %v; want 0 and none", calls, adds)
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 1 {
		t.Errorf("not_owned/enforce = %v, want 1", got)
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeShadow); got != 0 {
		t.Errorf("not_owned/shadow = %v, want 0", got)
	}
}

func TestOwnedByReplicaDropsLegacyLabelOfProjectOwnedElsewhere(t *testing.T) {
	const controller = "test-predicate-legacy"
	p := OwnedByReplica[*corev1.Secret](enforced, nil, controller)
	if p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-_p2"})}) {
		t.Error("legacy label of a project owned elsewhere passed")
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 1 {
		t.Errorf("not_owned = %v, want 1", got)
	}
}

type erroringOwner struct{}

func (erroringOwner) Owns(multicluster.ClusterName) bool { return false }

func (erroringOwner) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return nil, errors.New("provider unavailable")
}

func TestOwnedByReplicaPassesWhenRegistrationCannotBeRead(t *testing.T) {
	const controller = "test-predicate-provider-error"
	p := OwnedByReplica[*corev1.Secret](NewEventOwnership(erroringOwner{}, config.EventOwnershipEnforce), nil, controller)
	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns", map[string]string{UpstreamOwnerClusterNameLabel: "cluster-p2"})}) {
		t.Error("event dropped without proof another replica owns its project")
	}
	if got := filtered(controller, FilterReasonUnverified, metricModeEnforce); got != 1 {
		t.Errorf("unverified = %v, want 1", got)
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 0 {
		t.Errorf("not_owned = %v, want 0", got)
	}
}

type failingNamespaceReader struct{ client.Reader }

func (failingNamespaceReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errors.New("apiserver unavailable")
}

func TestOwnedByReplicaPassesAndLogsWhenNamespaceReadFails(t *testing.T) {
	var lines []string
	prev := ownershipLog
	ownershipLog = funcr.New(func(_, args string) { lines = append(lines, args) }, funcr.Options{Verbosity: 0})
	t.Cleanup(func() { ownershipLog = prev })

	const controller = "test-predicate-namespace-error"
	p := OwnedByReplica[*corev1.Secret](enforced, failingNamespaceReader{}, controller)
	if !p.Create(event.TypedCreateEvent[*corev1.Secret]{Object: labeledSecret("ns-unreadable", nil)}) {
		t.Fatal("event dropped when its namespace could not be read")
	}
	if got := filtered(controller, FilterReasonNotOwned, metricModeEnforce); got != 0 {
		t.Errorf("not_owned = %v, want 0", got)
	}

	want := []string{
		`"msg"="treating downstream object as unlabeled: namespace read failed"`,
		`"controller"="` + controller + `"`,
		`"namespace"="ns-unreadable"`,
		`"error"="apiserver unavailable"`,
	}
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %v", len(lines), lines)
	}
	for _, w := range want {
		if !strings.Contains(lines[0], w) {
			t.Errorf("log line %q missing %s", lines[0], w)
		}
	}
}
