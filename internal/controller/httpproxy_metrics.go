// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

type httpProxyStep string

const (
	httpProxyStepValidate        httpProxyStep = "validate"
	httpProxyStepGateway         httpProxyStep = "gateway"
	httpProxyStepHTTPRouteFilter httpProxyStep = "httproutefilter"
	httpProxyStepHTTPRoute       httpProxyStep = "httproute"
	httpProxyStepEndpointSlice   httpProxyStep = "endpointslice"
	httpProxyStepStatus          httpProxyStep = "status"
)

const (
	httpProxyErrorReasonInvalid   = "invalid"
	httpProxyErrorReasonConflict  = "conflict"
	httpProxyErrorReasonForbidden = "forbidden"
	httpProxyErrorReasonNotFound  = "notfound"
	httpProxyErrorReasonTimeout   = "timeout"
	httpProxyErrorReasonOther     = "other"

	httpProxyChangeCreate = "create"
	httpProxyChangeUpdate = "update"

	httpProxyReasonNotObserved = "NotObserved"
	httpProxyReasonOther       = "Other"

	httpProxyDefaultProgrammedMessage = "The HTTPProxy has not been programmed"
	httpProxyFleetListTimeout         = 5 * time.Second
)

var (
	httpProxyReconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_httpproxy_reconcile_errors_total",
			Help: "HTTPProxy reconciles that failed, by the step that failed and the API error class.",
		},
		[]string{"step", metricLabelReason},
	)

	httpProxyProgrammingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nso_httpproxy_programming_duration_seconds",
			Help:    "Time from an HTTPProxy generation first being observed (creation time for generation 1) to Programmed=True at that generation.",
			Buckets: []float64{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200},
		},
		[]string{"change"},
	)

	httpProxyFleet = newHTTPProxyFleetCollector(time.Now)

	httpProxyProgramming = newHTTPProxyProgrammingTracker(time.Now)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		httpProxyReconcileErrorsTotal,
		httpProxyProgrammingDuration,
		httpProxyFleet,
	)
}

func httpProxyErrorReason(err error) string {
	switch {
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return httpProxyErrorReasonInvalid
	case apierrors.IsConflict(err), apierrors.IsAlreadyExists(err):
		return httpProxyErrorReasonConflict
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return httpProxyErrorReasonForbidden
	case apierrors.IsNotFound(err):
		return httpProxyErrorReasonNotFound
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err),
		errors.Is(err, context.DeadlineExceeded):
		return httpProxyErrorReasonTimeout
	default:
		return httpProxyErrorReasonOther
	}
}

type httpProxyKey struct {
	cluster multicluster.ClusterName
	types.NamespacedName
}

type httpProxyReconcileObservation struct {
	step              httpProxyStep
	key               httpProxyKey
	generation        int64
	created           time.Time
	wasProgrammed     bool
	reconcileStarted  time.Time
	programmedTracker *httpProxyProgrammingTracker
}

func beginHTTPProxyReconcile(clusterName multicluster.ClusterName, httpProxy *networkingv1alpha.HTTPProxy) *httpProxyReconcileObservation {
	return &httpProxyReconcileObservation{
		step: httpProxyStepValidate,
		key: httpProxyKey{
			cluster:        clusterName,
			NamespacedName: types.NamespacedName{Namespace: httpProxy.Namespace, Name: httpProxy.Name},
		},
		generation:        httpProxy.Generation,
		created:           httpProxy.CreationTimestamp.Time,
		wasProgrammed:     httpProxyProgrammedAtGeneration(httpProxy),
		reconcileStarted:  httpProxyProgramming.now(),
		programmedTracker: httpProxyProgramming,
	}
}

func (o *httpProxyReconcileObservation) failed(ctx context.Context, err error, accepted, programmed *metav1.Condition) {
	if err == nil {
		if o.isDerivedWrite() && accepted.Status != metav1.ConditionTrue && accepted.Reason == networkingv1alpha.HTTPProxyReasonDerivedResourceInvalid {
			httpProxyReconcileErrorsTotal.WithLabelValues(string(o.step), httpProxyErrorReasonInvalid).Inc()
		}
		return
	}

	reason := httpProxyErrorReason(err)
	httpProxyReconcileErrorsTotal.WithLabelValues(string(o.step), reason).Inc()

	if programmed.Status == metav1.ConditionTrue || programmed.Message != httpProxyDefaultProgrammedMessage {
		return
	}

	if o.isDerivedWrite() && reason == httpProxyErrorReasonInvalid {
		if msg, ok := derivedResourceInvalidMessage(err); ok {
			programmed.Message = sanitizeConditionMessage(msg)
			return
		}
	}

	ref := o.failureRef(reason)
	log.FromContext(ctx).Error(err, "httpproxy reconcile failed",
		"ref", ref,
		"reconcileID", controller.ReconcileIDFromContext(ctx),
		"step", o.step,
		metricLabelReason, reason,
	)
	programmed.Message = fmt.Sprintf("The HTTPProxy could not be programmed due to an internal error and will be retried (ref: %s)", ref)
}

func (o *httpProxyReconcileObservation) isDerivedWrite() bool {
	switch o.step {
	case httpProxyStepGateway, httpProxyStepHTTPRouteFilter, httpProxyStepHTTPRoute, httpProxyStepEndpointSlice:
		return true
	}
	return false
}

func (o *httpProxyReconcileObservation) failureRef(reason string) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s/%s/%s/%d/%s/%s", o.key.cluster, o.key.Namespace, o.key.Name, o.generation, o.step, reason)
	return fmt.Sprintf("%08x", h.Sum32())
}

func (o *httpProxyReconcileObservation) statusFailed(err error) {
	httpProxyReconcileErrorsTotal.WithLabelValues(string(httpProxyStepStatus), httpProxyErrorReason(err)).Inc()
}

func (o *httpProxyReconcileObservation) finished(programmed *metav1.Condition, persisted bool) {
	o.programmedTracker.observe(o, programmed.Status == metav1.ConditionTrue, persisted)
}

func httpProxyProgrammedAtGeneration(httpProxy *networkingv1alpha.HTTPProxy) bool {
	c := apimeta.FindStatusCondition(httpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
	return c != nil && c.Status == metav1.ConditionTrue && c.ObservedGeneration == httpProxy.Generation
}

type httpProxyPendingGeneration struct {
	generation int64
	since      time.Time
}

type httpProxyProgrammingTracker struct {
	mu       sync.Mutex
	pending  map[httpProxyKey]httpProxyPendingGeneration
	recorded map[httpProxyKey]int64
	now      func() time.Time
}

func newHTTPProxyProgrammingTracker(now func() time.Time) *httpProxyProgrammingTracker {
	return &httpProxyProgrammingTracker{
		pending:  map[httpProxyKey]httpProxyPendingGeneration{},
		recorded: map[httpProxyKey]int64{},
		now:      now,
	}
}

func (t *httpProxyProgrammingTracker) observe(o *httpProxyReconcileObservation, programmed, persisted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if recorded, ok := t.recorded[o.key]; o.wasProgrammed || (ok && recorded >= o.generation) {
		delete(t.pending, o.key)
		return
	}

	entry, ok := t.pending[o.key]
	if !ok || entry.generation != o.generation {
		entry = httpProxyPendingGeneration{generation: o.generation, since: o.reconcileStarted}
		if o.generation == 1 && !o.created.IsZero() {
			entry.since = o.created
		}
	}

	if !programmed || !persisted {
		t.pending[o.key] = entry
		return
	}

	change := httpProxyChangeUpdate
	if o.generation == 1 {
		change = httpProxyChangeCreate
	}
	httpProxyProgrammingDuration.WithLabelValues(change).Observe(t.now().Sub(entry.since).Seconds())
	delete(t.pending, o.key)
	t.recorded[o.key] = o.generation
}

func (t *httpProxyProgrammingTracker) retain(live map[httpProxyKey]struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.pending {
		if _, ok := live[k]; !ok {
			delete(t.pending, k)
		}
	}
	for k := range t.recorded {
		if _, ok := live[k]; !ok {
			delete(t.recorded, k)
		}
	}
}

func httpProxyUnprogrammedState(httpProxy *networkingv1alpha.HTTPProxy) (reason string, since time.Time, unprogrammed bool) {
	if httpProxyProgrammedAtGeneration(httpProxy) {
		return "", time.Time{}, false
	}

	programmed := apimeta.FindStatusCondition(httpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
	if programmed == nil || programmed.ObservedGeneration != httpProxy.Generation {
		return httpProxyReasonNotObserved, httpProxyLastSpecChange(httpProxy), true
	}

	since = programmed.LastTransitionTime.Time
	if since.IsZero() {
		since = httpProxy.CreationTimestamp.Time
	}

	accepted := apimeta.FindStatusCondition(httpProxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionAccepted)
	if accepted != nil && accepted.Status != metav1.ConditionTrue && accepted.ObservedGeneration == httpProxy.Generation {
		switch accepted.Reason {
		case networkingv1alpha.HTTPProxyReasonInvalid, networkingv1alpha.HTTPProxyReasonDerivedResourceInvalid:
			return accepted.Reason, since, true
		}
	}

	return boundedConditionReason(programmed.Reason), since, true
}

func httpProxyLastSpecChange(httpProxy *networkingv1alpha.HTTPProxy) time.Time {
	latest := httpProxy.CreationTimestamp.Time
	for _, mf := range httpProxy.ManagedFields {
		if mf.Subresource == "" && mf.Time != nil && mf.Time.After(latest) {
			latest = mf.Time.Time
		}
	}
	return latest
}

var conditionReasonPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)

func boundedConditionReason(reason string) string {
	if !conditionReasonPattern.MatchString(reason) {
		return httpProxyReasonOther
	}
	return reason
}

type httpProxyFleetCollector struct {
	now      func() time.Time
	started  atomic.Bool
	mu       sync.Mutex
	clusters map[multicluster.ClusterName]cluster.Cluster

	totalDesc        *prometheus.Desc
	unprogrammedDesc *prometheus.Desc
	oldestDesc       *prometheus.Desc
}

func newHTTPProxyFleetCollector(now func() time.Time) *httpProxyFleetCollector {
	return &httpProxyFleetCollector{
		now:      now,
		clusters: map[multicluster.ClusterName]cluster.Cluster{},
		totalDesc: prometheus.NewDesc(
			"nso_httpproxies",
			"HTTPProxies in project control planes reconciled by this replica, excluding those being deleted.",
			nil, nil,
		),
		unprogrammedDesc: prometheus.NewDesc(
			"nso_httpproxy_unprogrammed",
			"HTTPProxies not Programmed at their current generation, by Programmed condition reason.",
			[]string{metricLabelReason}, nil,
		),
		oldestDesc: prometheus.NewDesc(
			"nso_httpproxy_oldest_unprogrammed_seconds",
			"Seconds the longest-unprogrammed HTTPProxy has been not Programmed, by Programmed condition reason.",
			[]string{metricLabelReason}, nil,
		),
	}
}

func (c *httpProxyFleetCollector) Start(ctx context.Context) error {
	c.started.Store(true)
	<-ctx.Done()
	c.started.Store(false)
	return nil
}

func (c *httpProxyFleetCollector) Engage(ctx context.Context, name multicluster.ClusterName, cl cluster.Cluster) error {
	c.mu.Lock()
	c.clusters[name] = cl
	c.mu.Unlock()

	go func() {
		<-ctx.Done()
		c.mu.Lock()
		if c.clusters[name] == cl {
			delete(c.clusters, name)
		}
		c.mu.Unlock()
	}()
	return nil
}

func (c *httpProxyFleetCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.totalDesc
	ch <- c.unprogrammedDesc
	ch <- c.oldestDesc
}

func (c *httpProxyFleetCollector) Collect(ch chan<- prometheus.Metric) {
	if !c.started.Load() {
		return
	}

	c.mu.Lock()
	clusters := make(map[multicluster.ClusterName]cluster.Cluster, len(c.clusters))
	for name, cl := range c.clusters {
		clusters[name] = cl
	}
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), httpProxyFleetListTimeout)
	defer cancel()

	now := c.now()
	total := 0
	counts := map[string]int{networkingv1alpha.HTTPProxyReasonPending: 0}
	oldest := map[string]time.Time{networkingv1alpha.HTTPProxyReasonPending: now}
	live := map[httpProxyKey]struct{}{}
	complete := true

	for name, cl := range clusters {
		var list networkingv1alpha.HTTPProxyList
		if err := cl.GetClient().List(ctx, &list); err != nil {
			log.Log.WithName("httpproxy-metrics").Error(err, "failed to list httpproxies", "cluster", name)
			complete = false
			continue
		}
		for i := range list.Items {
			httpProxy := &list.Items[i]
			live[httpProxyKey{cluster: name, NamespacedName: types.NamespacedName{Namespace: httpProxy.Namespace, Name: httpProxy.Name}}] = struct{}{}
			if !httpProxy.DeletionTimestamp.IsZero() {
				continue
			}
			total++
			reason, since, unprogrammed := httpProxyUnprogrammedState(httpProxy)
			if !unprogrammed {
				continue
			}
			counts[reason]++
			if o, ok := oldest[reason]; !ok || since.Before(o) || counts[reason] == 1 {
				oldest[reason] = since
			}
		}
	}

	if complete {
		httpProxyProgramming.retain(live)
	}

	ch <- prometheus.MustNewConstMetric(c.totalDesc, prometheus.GaugeValue, float64(total))
	for reason, n := range counts {
		ch <- prometheus.MustNewConstMetric(c.unprogrammedDesc, prometheus.GaugeValue, float64(n), reason)
		ch <- prometheus.MustNewConstMetric(c.oldestDesc, prometheus.GaugeValue, max(0, now.Sub(oldest[reason]).Seconds()), reason)
	}
}
