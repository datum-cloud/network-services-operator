package downstreamclient

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/instrumentation/labels"
	"go.miloapis.com/instrumentation/metrics"

	"go.datum.net/network-services-operator/internal/config"
)

type ClusterOwner interface {
	Owns(multicluster.ClusterName) bool
	GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error)
}

type EventOwnership struct {
	owner ClusterOwner
	mode  config.EventOwnershipMode
}

func NewEventOwnership(owner ClusterOwner, mode config.EventOwnershipMode) EventOwnership {
	return EventOwnership{owner: owner, mode: mode}
}

func (o EventOwnership) disabled() bool {
	return o.owner == nil || (o.mode != config.EventOwnershipShadow && o.mode != config.EventOwnershipEnforce)
}

func (o EventOwnership) enforcing() bool {
	return o.mode == config.EventOwnershipEnforce
}

func (o EventOwnership) count(controller, reason string) {
	mode := metricModeShadow
	if o.enforcing() {
		mode = metricModeEnforce
	}
	downstreamEventsFiltered.WithLabelValues(controller, reason, mode).Inc()
}

func (o EventOwnership) keep(controller string, v ownership) bool {
	switch v {
	case ownershipNotOwned:
		o.count(controller, FilterReasonNotOwned)
		return !o.enforcing()
	case ownershipUnverified:
		o.count(controller, FilterReasonUnverified)
	}
	return true
}

const (
	FilterReasonNotOwned   = "not_owned"
	FilterReasonUnverified = "unverified"

	metricModeShadow  = "shadow"
	metricModeEnforce = "enforce"
)

var ownershipLog = logf.Log.WithName("downstream-ownership")

var downstreamEventsFiltered = metrics.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nso_downstream_events_filtered_total",
		Help: "Downstream events and requests judged by upstream cluster ownership. reason=not_owned: the cluster is registered here and this replica does not currently own it; dropped when mode=enforce, only counted when mode=shadow. reason=unverified: ownership could not be established because the cluster is not registered here; always passed. Each filter counts separately, so an event can also be counted for each request it maps to. Counts include short windows where this replica has not yet started a cluster it is about to own, so mode=shadow overstates how much enforcement would drop.",
	},
	[]string{"controller", labels.Reason, "mode"},
)

type ownership int

const (
	ownershipNotOwned ownership = iota
	ownershipNoLabel
	ownershipUnverified
	ownershipOwned
)

func (o EventOwnership) classify(name multicluster.ClusterName) ownership {
	if o.owner.Owns(name) {
		return ownershipOwned
	}
	if _, err := o.owner.GetCluster(context.Background(), name); err != nil {
		return ownershipUnverified
	}
	return ownershipNotOwned
}

type ownershipCheck struct {
	ownership  EventOwnership
	namespaces client.Reader
	controller string
	labelKey   string
	decode     func(string) string
}

type judged struct {
	verdict ownership
	key     string
	value   string
}

func (c ownershipCheck) of(obj client.Object) judged {
	if v := obj.GetLabels()[c.labelKey]; v != "" {
		return judged{verdict: c.ownership.classify(multicluster.ClusterName(c.decode(v))), key: c.labelKey, value: v}
	}
	if c.namespaces == nil || obj.GetNamespace() == "" {
		return judged{verdict: ownershipNoLabel}
	}
	var ns corev1.Namespace
	if err := c.namespaces.Get(context.Background(), client.ObjectKey{Name: obj.GetNamespace()}, &ns); err != nil {
		ownershipLog.Info("treating downstream object as unlabeled: namespace read failed",
			"controller", c.controller,
			"namespace", obj.GetNamespace(),
			"error", err.Error(),
		)
		return judged{verdict: ownershipNoLabel}
	}
	if v := ns.Labels[UpstreamOwnerClusterNameLabel]; v != "" {
		return judged{verdict: c.ownership.classify(multicluster.ClusterName(UpstreamClusterNameFromLabel(v))), key: UpstreamOwnerClusterNameLabel, value: v}
	}
	return judged{verdict: ownershipNoLabel}
}

func (c ownershipCheck) allow(objs ...client.Object) bool {
	best := judged{verdict: ownershipNotOwned}
	for i, obj := range objs {
		j := c.of(obj)
		if j.verdict == ownershipOwned {
			return true
		}
		if i == 0 || j.verdict > best.verdict {
			best = j
		}
	}
	if best.verdict == ownershipUnverified {
		ownershipLog.V(1).Info("passing downstream event whose upstream cluster is not registered here",
			"controller", c.controller,
			"label", best.key,
			"value", best.value,
		)
	}
	return c.ownership.keep(c.controller, best.verdict)
}

func OwnedByReplica[T client.Object](ownership EventOwnership, namespaces client.Reader, controller string) predicate.TypedPredicate[T] {
	return OwnedByReplicaWithLabel[T](ownership, namespaces, controller, UpstreamOwnerClusterNameLabel, UpstreamClusterNameFromLabel)
}

func OwnedByReplicaWithLabel[T client.Object](
	ownership EventOwnership,
	namespaces client.Reader,
	controller string,
	labelKey string,
	decode func(string) string,
) predicate.TypedPredicate[T] {
	if ownership.disabled() {
		return predicate.TypedFuncs[T]{}
	}
	c := ownershipCheck{
		ownership:  ownership,
		namespaces: namespaces,
		controller: controller,
		labelKey:   labelKey,
		decode:     decode,
	}
	return predicate.TypedFuncs[T]{
		CreateFunc: func(e event.TypedCreateEvent[T]) bool { return c.allow(e.Object) },
		UpdateFunc: func(e event.TypedUpdateEvent[T]) bool { return c.allow(e.ObjectNew, e.ObjectOld) },
		DeleteFunc: func(e event.TypedDeleteEvent[T]) bool { return c.allow(e.Object) },
		GenericFunc: func(e event.TypedGenericEvent[T]) bool {
			return c.allow(e.Object)
		},
	}
}

func OwnedRequestsOnly[O any, R mcreconcile.ClusterAware[R]](
	ownership EventOwnership,
	controller string,
	h handler.TypedEventHandler[O, R],
) handler.TypedEventHandler[O, R] {
	if ownership.disabled() {
		return h
	}
	return &ownedRequestsHandler[O, R]{ownership: ownership, controller: controller, inner: h}
}

func OwnedRequestsOnlyFunc[O client.Object, R mcreconcile.ClusterAware[R]](
	ownership EventOwnership,
	controller string,
	hf mchandler.TypedEventHandlerFunc[O, R],
) mchandler.TypedEventHandlerFunc[O, R] {
	if ownership.disabled() {
		return hf
	}
	return func(name multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[O, R] {
		return OwnedRequestsOnly(ownership, controller, hf(name, cl))
	}
}

type ownedRequestsHandler[O any, R mcreconcile.ClusterAware[R]] struct {
	ownership  EventOwnership
	controller string
	inner      handler.TypedEventHandler[O, R]
}

func (h *ownedRequestsHandler[O, R]) Create(ctx context.Context, e event.TypedCreateEvent[O], q workqueue.TypedRateLimitingInterface[R]) {
	h.inner.Create(ctx, e, h.wrap(q))
}

func (h *ownedRequestsHandler[O, R]) Update(ctx context.Context, e event.TypedUpdateEvent[O], q workqueue.TypedRateLimitingInterface[R]) {
	h.inner.Update(ctx, e, h.wrap(q))
}

func (h *ownedRequestsHandler[O, R]) Delete(ctx context.Context, e event.TypedDeleteEvent[O], q workqueue.TypedRateLimitingInterface[R]) {
	h.inner.Delete(ctx, e, h.wrap(q))
}

func (h *ownedRequestsHandler[O, R]) Generic(ctx context.Context, e event.TypedGenericEvent[O], q workqueue.TypedRateLimitingInterface[R]) {
	h.inner.Generic(ctx, e, h.wrap(q))
}

func (h *ownedRequestsHandler[O, R]) wrap(q workqueue.TypedRateLimitingInterface[R]) workqueue.TypedRateLimitingInterface[R] {
	oq := ownedQueue[R]{TypedRateLimitingInterface: q, ownership: h.ownership, controller: h.controller}
	if pq, ok := q.(priorityqueue.PriorityQueue[R]); ok {
		return ownedPriorityQueue[R]{ownedQueue: oq, pq: pq}
	}
	return oq
}

type ownedQueue[R mcreconcile.ClusterAware[R]] struct {
	workqueue.TypedRateLimitingInterface[R]
	ownership  EventOwnership
	controller string
}

func (q ownedQueue[R]) owned(item R) bool {
	name := item.Cluster()
	if name == "" {
		return true
	}
	verdict := q.ownership.classify(name)
	if verdict == ownershipUnverified {
		ownershipLog.V(1).Info("passing request whose upstream cluster is not registered here",
			"controller", q.controller,
			"cluster", name,
		)
	}
	return q.ownership.keep(q.controller, verdict)
}

func (q ownedQueue[R]) Add(item R) {
	if q.owned(item) {
		q.TypedRateLimitingInterface.Add(item)
	}
}

func (q ownedQueue[R]) AddAfter(item R, d time.Duration) {
	if q.owned(item) {
		q.TypedRateLimitingInterface.AddAfter(item, d)
	}
}

func (q ownedQueue[R]) AddRateLimited(item R) {
	if q.owned(item) {
		q.TypedRateLimitingInterface.AddRateLimited(item)
	}
}

type ownedPriorityQueue[R mcreconcile.ClusterAware[R]] struct {
	ownedQueue[R]
	pq priorityqueue.PriorityQueue[R]
}

func (q ownedPriorityQueue[R]) AddWithOpts(o priorityqueue.AddOpts, items ...R) {
	kept := make([]R, 0, len(items))
	for _, item := range items {
		if q.owned(item) {
			kept = append(kept, item)
		}
	}
	if len(kept) > 0 {
		q.pq.AddWithOpts(o, kept...)
	}
}

func (q ownedPriorityQueue[R]) GetWithPriority() (R, int, bool) {
	return q.pq.GetWithPriority()
}
