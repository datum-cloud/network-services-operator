// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

func TestHTTPProxyErrorReason(t *testing.T) {
	gr := schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Kind: "HTTPRoute"}, "r", field.ErrorList{field.Required(field.NewPath("spec"), "")}), httpProxyErrorReasonInvalid},
		{"bad request", apierrors.NewBadRequest("bad"), httpProxyErrorReasonInvalid},
		{"conflict", apierrors.NewConflict(gr, "r", errors.New("stale")), httpProxyErrorReasonConflict},
		{"already exists", apierrors.NewAlreadyExists(gr, "r"), httpProxyErrorReasonConflict},
		{"forbidden", apierrors.NewForbidden(gr, "r", errors.New("quota")), httpProxyErrorReasonForbidden},
		{"not found", apierrors.NewNotFound(gr, "r"), httpProxyErrorReasonNotFound},
		{"server timeout", apierrors.NewServerTimeout(gr, "create", 1), httpProxyErrorReasonTimeout},
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), httpProxyErrorReasonTimeout},
		{"wrapped forbidden", fmt.Errorf("failed updating httproute resource: %w", apierrors.NewForbidden(gr, "r", errors.New("x"))), httpProxyErrorReasonForbidden},
		{"other", errors.New("boom"), httpProxyErrorReasonOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, httpProxyErrorReason(tt.err))
		})
	}
}

func defaultedProgrammed() *metav1.Condition {
	return &metav1.Condition{Status: metav1.ConditionFalse, Reason: networkingv1alpha.HTTPProxyReasonPending, Message: httpProxyDefaultProgrammedMessage}
}

func newTestObservation(step httpProxyStep) *httpProxyReconcileObservation {
	return &httpProxyReconcileObservation{
		step:       step,
		generation: 3,
		key:        httpProxyKey{cluster: "project-a", NamespacedName: types.NamespacedName{Namespace: "ns", Name: "p"}},
	}
}

func invalidHTTPRouteError() error {
	path := field.NewPath("spec", "rules").Index(0).Child("filters").Index(0).Child("requestRedirect", "statusCode")
	return fmt.Errorf("failed updating httproute resource: %w", apierrors.NewInvalid(
		schema.GroupKind{Group: gatewayv1.GroupName, Kind: "HTTPRoute"},
		"internal-route-name",
		field.ErrorList{field.NotSupported(path, 308, []string{"301", "302"})},
	))
}

func TestHTTPProxyObservationInvalidDerivedWriteSurfacesOnlyCauses(t *testing.T) {
	programmed := defaultedProgrammed()
	newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), invalidHTTPRouteError(), &metav1.Condition{}, programmed)

	assert.Equal(t,
		`The HTTPProxy cannot be programmed: the HTTPRoute generated from it is invalid: spec.rules[0].filters[0].requestRedirect.statusCode: Unsupported value: 308: supported values: "301", "302"`,
		programmed.Message)
	assert.NotContains(t, programmed.Message, "internal-route-name")
	assert.NotContains(t, programmed.Message, "failed updating")
	assert.NotContains(t, programmed.Message, "gateway.networking.k8s.io")
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, programmed.Reason)
}

func TestHTTPProxyObservationInternalErrorsGetFixedMessage(t *testing.T) {
	gr := schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}
	tests := []struct {
		name string
		step httpProxyStep
		err  error
	}{
		{"dial", httpProxyStepHTTPRoute, errors.New(`Internal error occurred: failed calling webhook "vhttproute.networking.datumapis.com": failed to call webhook: Post "https://nso-webhook.network-services-operator-system.svc:443/validate?timeout=10s": dial tcp 10.12.4.7:443: connect: connection refused`)},
		{"rbac", httpProxyStepGateway, apierrors.NewForbidden(gr, "r", errors.New(`User "system:control@networking.datumapis.com" cannot create resource "httproutes" in API group "gateway.networking.k8s.io" in the namespace "ns"`))},
		{"webhook denial", httpProxyStepEndpointSlice, apierrors.NewBadRequest(`admission webhook "vendpointslice.nso.svc.cluster.local" denied the request: address 10.0.3.9 is not allowed`)},
		{"invalid outside a derived write", httpProxyStepStatus, invalidHTTPRouteError()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			programmed := defaultedProgrammed()
			newTestObservation(tt.step).failed(context.Background(), tt.err, &metav1.Condition{}, programmed)

			assert.Regexp(t, `^The HTTPProxy could not be programmed due to an internal error and will be retried \(ref: [0-9a-f]{8}\)$`, programmed.Message)
			for _, leak := range []string{"10.", ".svc", "system:", "webhook", "datumapis"} {
				assert.NotContains(t, programmed.Message, leak)
			}
		})
	}
}

func TestHTTPProxyObservationTransientErrorsShareRefAndLog(t *testing.T) {
	gr := schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{"not found from cache lag", apierrors.NewNotFound(gr, "r"), httpProxyErrorReason(apierrors.NewNotFound(gr, "r"))},
		{"timeout", apierrors.NewTimeoutError("slow", 1), httpProxyErrorReasonTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := log.IntoContext(context.Background(), zap.New(zap.WriteTo(&buf), zap.UseDevMode(false)))
			o := newTestObservation(httpProxyStepHTTPRoute)
			programmed := defaultedProgrammed()
			o.failed(ctx, tt.err, &metav1.Condition{}, programmed)

			ref := o.failureRef(tt.reason)
			assert.Equal(t, fmt.Sprintf("The HTTPProxy could not be programmed due to an internal error and will be retried (ref: %s)", ref), programmed.Message)
			var entry map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
			assert.Equal(t, ref, entry["ref"])
			assert.Equal(t, tt.err.Error(), entry["error"])
		})
	}
}

func TestHTTPProxyObservationMessageStableAcrossRetries(t *testing.T) {
	for _, err := range []error{errors.New("dial tcp 10.0.0.1:443: i/o timeout"), invalidHTTPRouteError()} {
		first := defaultedProgrammed()
		newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), err, &metav1.Condition{}, first)
		second := defaultedProgrammed()
		newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), err, &metav1.Condition{}, second)
		assert.Equal(t, first.Message, second.Message)
	}

	a := defaultedProgrammed()
	newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), errors.New("dial tcp 10.0.0.1:443"), &metav1.Condition{}, a)
	b := defaultedProgrammed()
	newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), errors.New("dial tcp 10.0.0.2:443: a different detail"), &metav1.Condition{}, b)
	assert.Equal(t, a.Message, b.Message, "the ref keys on the failure class, not the error text")

	c := defaultedProgrammed()
	newTestObservation(httpProxyStepGateway).failed(context.Background(), errors.New("dial tcp 10.0.0.1:443"), &metav1.Condition{}, c)
	assert.NotEqual(t, a.Message, c.Message, "a different failing step gets a different ref")
}

func TestHTTPProxyObservationKeepsSpecificMessages(t *testing.T) {
	for _, msg := range []string{
		`Underlying HTTPRoute with the name "p" already exists and is owned by a different resource.`,
		`The HTTPProxy cannot be programmed: referenced EndpointSlice "x" not found`,
	} {
		programmed := &metav1.Condition{Status: metav1.ConditionFalse, Reason: networkingv1alpha.HTTPProxyReasonConflict, Message: msg}
		newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), errors.New("boom"), &metav1.Condition{}, programmed)
		assert.Equal(t, msg, programmed.Message)
	}

	untouched := defaultedProgrammed()
	newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), nil, &metav1.Condition{}, untouched)
	assert.Equal(t, httpProxyDefaultProgrammedMessage, untouched.Message)
}

func TestHTTPProxyObservationCountsErrors(t *testing.T) {
	counter := httpProxyReconcileErrorsTotal.WithLabelValues("httproute", "other")
	before := testutil.ToFloat64(counter)
	newTestObservation(httpProxyStepHTTPRoute).failed(context.Background(), errors.New("boom"), &metav1.Condition{}, defaultedProgrammed())
	assert.Equal(t, before+1, testutil.ToFloat64(counter))
}

func TestHTTPProxyObservationCountsSwallowedInvalidWrite(t *testing.T) {
	invalidAccepted := &metav1.Condition{Status: metav1.ConditionFalse, Reason: networkingv1alpha.HTTPProxyReasonDerivedResourceInvalid}

	route := httpProxyReconcileErrorsTotal.WithLabelValues("httproute", "invalid")
	before := testutil.ToFloat64(route)
	(&httpProxyReconcileObservation{step: httpProxyStepHTTPRoute}).failed(context.Background(), nil, invalidAccepted, &metav1.Condition{})
	assert.Equal(t, before+1, testutil.ToFloat64(route))

	validate := httpProxyReconcileErrorsTotal.WithLabelValues("validate", "invalid")
	before = testutil.ToFloat64(validate)
	(&httpProxyReconcileObservation{step: httpProxyStepValidate}).failed(context.Background(), nil, invalidAccepted, &metav1.Condition{})
	assert.Equal(t, before, testutil.ToFloat64(validate), "a spec the user got wrong is not a platform failure")

	for _, reason := range []string{networkingv1alpha.HTTPProxyReasonPending, networkingv1alpha.HTTPProxyReasonInvalid} {
		before = testutil.ToFloat64(route)
		(&httpProxyReconcileObservation{step: httpProxyStepHTTPRoute}).failed(context.Background(), nil, &metav1.Condition{Status: metav1.ConditionFalse, Reason: reason}, &metav1.Condition{})
		assert.Equal(t, before, testutil.ToFloat64(route), "only DerivedResourceInvalid counts; Invalid is the user's own spec")
	}
}

func histogramSampleCount(t *testing.T, change string) uint64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, httpProxyProgrammingDuration.WithLabelValues(change).(prometheus.Metric).Write(&m))
	return m.GetHistogram().GetSampleCount()
}

func histogramSampleSum(t *testing.T, change string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, httpProxyProgrammingDuration.WithLabelValues(change).(prometheus.Metric).Write(&m))
	return m.GetHistogram().GetSampleSum()
}

func TestHTTPProxyProgrammingTracker(t *testing.T) {
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := base
	tracker := newHTTPProxyProgrammingTracker(func() time.Time { return now })
	key := httpProxyKey{cluster: "c", NamespacedName: types.NamespacedName{Namespace: "ns", Name: "p"}}

	obs := func(generation int64, wasProgrammed bool) *httpProxyReconcileObservation {
		return &httpProxyReconcileObservation{
			key:               key,
			generation:        generation,
			created:           base.Add(-30 * time.Second),
			wasProgrammed:     wasProgrammed,
			reconcileStarted:  now,
			programmedTracker: tracker,
		}
	}

	t.Run("create is timed from creation", func(t *testing.T) {
		count, sum := histogramSampleCount(t, "create"), histogramSampleSum(t, "create")
		tracker.observe(obs(1, false), false, true)
		now = base.Add(90 * time.Second)
		tracker.observe(obs(1, false), true, true)
		assert.Equal(t, count+1, histogramSampleCount(t, "create"))
		assert.InDelta(t, sum+120, histogramSampleSum(t, "create"), 0.001)
		assert.Empty(t, tracker.pending)
	})

	t.Run("update is timed from first reconcile of the generation", func(t *testing.T) {
		now = base
		count, sum := histogramSampleCount(t, "update"), histogramSampleSum(t, "update")
		tracker.observe(obs(2, false), false, true)
		now = base.Add(10 * time.Second)
		tracker.observe(obs(2, false), false, true)
		now = base.Add(45 * time.Second)
		tracker.observe(obs(2, false), true, true)
		assert.Equal(t, count+1, histogramSampleCount(t, "update"))
		assert.InDelta(t, sum+45, histogramSampleSum(t, "update"), 0.001)
	})

	t.Run("a newer generation restarts the clock", func(t *testing.T) {
		now = base
		sum := histogramSampleSum(t, "update")
		tracker.observe(obs(3, false), false, true)
		now = base.Add(100 * time.Second)
		tracker.observe(obs(4, false), false, true)
		now = base.Add(105 * time.Second)
		tracker.observe(obs(4, false), true, true)
		assert.InDelta(t, sum+5, histogramSampleSum(t, "update"), 0.001)
	})

	t.Run("already programmed is not observed", func(t *testing.T) {
		count := histogramSampleCount(t, "update")
		tracker.observe(obs(5, true), true, true)
		assert.Equal(t, count, histogramSampleCount(t, "update"))
		assert.Empty(t, tracker.pending)
	})

	t.Run("unpersisted programmed status is not observed", func(t *testing.T) {
		now = base
		count := histogramSampleCount(t, "update")
		tracker.observe(obs(6, false), true, false)
		assert.Equal(t, count, histogramSampleCount(t, "update"))
		assert.Contains(t, tracker.pending, key)
	})

	t.Run("a stale-cache reconcile after a recorded generation adds no sample", func(t *testing.T) {
		now = base
		count := histogramSampleCount(t, "create")
		tracker.observe(obs(1, false), true, true)
		assert.Equal(t, count, histogramSampleCount(t, "create"))

		tracker.observe(obs(7, false), true, true)
		updates := histogramSampleCount(t, "update")
		tracker.observe(obs(7, false), false, true)
		tracker.observe(obs(7, false), true, true)
		assert.Equal(t, updates, histogramSampleCount(t, "update"))
		assert.Empty(t, tracker.pending)
	})

	t.Run("retain drops proxies that are gone", func(t *testing.T) {
		tracker.retain(map[httpProxyKey]struct{}{})
		assert.Empty(t, tracker.pending)
		assert.Empty(t, tracker.recorded)
	})
}

func TestHTTPProxyUnprogrammedState(t *testing.T) {
	created := metav1.NewTime(time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC))
	transition := metav1.NewTime(time.Date(2026, 10, 6, 11, 0, 0, 0, time.UTC))
	edited := metav1.NewTime(time.Date(2026, 10, 6, 11, 30, 0, 0, time.UTC))

	proxy := func(generation int64, conds ...metav1.Condition) *networkingv1alpha.HTTPProxy {
		p := &networkingv1alpha.HTTPProxy{}
		p.Generation = generation
		p.CreationTimestamp = created
		p.ManagedFields = []metav1.ManagedFieldsEntry{
			{Manager: "user", Time: &edited},
			{Manager: "nso", Subresource: "status", Time: &metav1.Time{Time: edited.Add(time.Hour)}},
		}
		p.Status.Conditions = conds
		return p
	}
	programmed := func(status metav1.ConditionStatus, reason string, gen int64) metav1.Condition {
		return metav1.Condition{Type: networkingv1alpha.HTTPProxyConditionProgrammed, Status: status, Reason: reason, ObservedGeneration: gen, LastTransitionTime: transition}
	}

	tests := []struct {
		name         string
		proxy        *networkingv1alpha.HTTPProxy
		unprogrammed bool
		reason       string
		since        time.Time
	}{
		{"programmed", proxy(2, programmed(metav1.ConditionTrue, "Programmed", 2)), false, "", time.Time{}},
		{"no condition", proxy(1), true, httpProxyReasonNotObserved, edited.Time},
		{"programmed at older generation", proxy(3, programmed(metav1.ConditionTrue, "Programmed", 2)), true, httpProxyReasonNotObserved, edited.Time},
		{"pending", proxy(2, programmed(metav1.ConditionFalse, "Pending", 2)), true, "Pending", transition.Time},
		{"gateway reason passes through", proxy(2, programmed(metav1.ConditionFalse, "AddressNotAssigned", 2)), true, "AddressNotAssigned", transition.Time},
		{"unbounded reason is folded", proxy(2, programmed(metav1.ConditionFalse, "not a reason!", 2)), true, httpProxyReasonOther, transition.Time},
		{"invalid spec", proxy(2,
			programmed(metav1.ConditionFalse, "Pending", 2),
			metav1.Condition{Type: networkingv1alpha.HTTPProxyConditionAccepted, Status: metav1.ConditionFalse, Reason: "Invalid", ObservedGeneration: 2},
		), true, networkingv1alpha.HTTPProxyReasonInvalid, transition.Time},
		{"generated resource rejected", proxy(2,
			programmed(metav1.ConditionFalse, "Pending", 2),
			metav1.Condition{Type: networkingv1alpha.HTTPProxyConditionAccepted, Status: metav1.ConditionFalse, Reason: "DerivedResourceInvalid", ObservedGeneration: 2},
		), true, networkingv1alpha.HTTPProxyReasonDerivedResourceInvalid, transition.Time},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, since, unprogrammed := httpProxyUnprogrammedState(tt.proxy)
			assert.Equal(t, tt.unprogrammed, unprogrammed)
			assert.Equal(t, tt.reason, reason)
			assert.True(t, tt.since.Equal(since), "since: want %s, got %s", tt.since, since)
		})
	}
}

func TestHTTPProxyFleetCollector(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cond := func(status metav1.ConditionStatus, reason string, ago time.Duration) []metav1.Condition {
		return []metav1.Condition{{
			Type: networkingv1alpha.HTTPProxyConditionProgrammed, Status: status, Reason: reason,
			ObservedGeneration: 1, LastTransitionTime: metav1.NewTime(now.Add(-ago)),
		}}
	}
	mk := func(name string, conds []metav1.Condition) *networkingv1alpha.HTTPProxy {
		p := &networkingv1alpha.HTTPProxy{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, Generation: 1}}
		p.Status.Conditions = conds
		return p
	}

	cl := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(
		mk("ok", cond(metav1.ConditionTrue, "Programmed", time.Hour)),
		mk("stuck", cond(metav1.ConditionFalse, "Pending", 90*time.Minute)),
		mk("new", cond(metav1.ConditionFalse, "Pending", 2*time.Minute)),
		mk("conflict", cond(metav1.ConditionFalse, "Conflict", 10*time.Minute)),
	).Build()

	c := newHTTPProxyFleetCollector(func() time.Time { return now })

	assert.Equal(t, 0, testutil.CollectAndCount(c), "reports nothing before the manager starts it")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Start(ctx) }()
	require.Eventually(t, c.started.Load, time.Second, time.Millisecond)

	engageCtx, disengage := context.WithCancel(context.Background())
	require.NoError(t, c.Engage(engageCtx, "project-a", &fakeCluster{cl: cl}))

	expected := `
# HELP nso_httpproxies HTTPProxies in project control planes reconciled by this replica, excluding those being deleted.
# TYPE nso_httpproxies gauge
nso_httpproxies 4
# HELP nso_httpproxy_oldest_unprogrammed_seconds Seconds the longest-unprogrammed HTTPProxy has been not Programmed, by Programmed condition reason.
# TYPE nso_httpproxy_oldest_unprogrammed_seconds gauge
nso_httpproxy_oldest_unprogrammed_seconds{reason="Conflict"} 600
nso_httpproxy_oldest_unprogrammed_seconds{reason="Pending"} 5400
# HELP nso_httpproxy_unprogrammed HTTPProxies not Programmed at their current generation, by Programmed condition reason.
# TYPE nso_httpproxy_unprogrammed gauge
nso_httpproxy_unprogrammed{reason="Conflict"} 1
nso_httpproxy_unprogrammed{reason="Pending"} 2
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected)))

	disengage()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.clusters) == 0
	}, time.Second, time.Millisecond)

	empty := `
# HELP nso_httpproxies HTTPProxies in project control planes reconciled by this replica, excluding those being deleted.
# TYPE nso_httpproxies gauge
nso_httpproxies 0
# HELP nso_httpproxy_oldest_unprogrammed_seconds Seconds the longest-unprogrammed HTTPProxy has been not Programmed, by Programmed condition reason.
# TYPE nso_httpproxy_oldest_unprogrammed_seconds gauge
nso_httpproxy_oldest_unprogrammed_seconds{reason="Pending"} 0
# HELP nso_httpproxy_unprogrammed HTTPProxies not Programmed at their current generation, by Programmed condition reason.
# TYPE nso_httpproxy_unprogrammed gauge
nso_httpproxy_unprogrammed{reason="Pending"} 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(empty)), "a disengaged project stops being reported")
}

func TestHTTPProxyReconcileInternalErrorGetsStableFixedMessage(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	httpProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		controllerutil.AddFinalizer(h, httpProxyFinalizer)
	})

	denied := apierrors.NewForbidden(gatewayv1.Resource("httproutes"), httpProxy.Name, errors.New(`User "system:control@networking.datumapis.com" cannot create resource "httproutes": webhook "vhttproute.nso.svc" at 10.12.4.7 denied the request`))
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(httpProxy).
		WithStatusSubresource(httpProxy).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*gatewayv1.HTTPRoute); ok {
					return denied
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	reconciler := &HTTPProxyReconciler{
		mgr: &fakeMockManager{cl: fakeClient},
		Config: config.NetworkServicesOperator{
			HTTPProxy: config.HTTPProxyConfig{GatewayClassName: "test-gateway-class"},
			Gateway: config.GatewayConfig{
				ControllerName: gatewayv1.GatewayController("test-gateway-class"),
				TargetDomain:   "example.com",
			},
		},
	}

	before := testutil.ToFloat64(httpProxyReconcileErrorsTotal.WithLabelValues("httproute", "forbidden"))

	_, err := reconciler.Reconcile(context.Background(), mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	})
	require.Error(t, err)

	assert.Equal(t, before+1, testutil.ToFloat64(httpProxyReconcileErrorsTotal.WithLabelValues("httproute", "forbidden")))

	var updated networkingv1alpha.HTTPProxy
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(httpProxy), &updated))
	programmed := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
	require.NotNil(t, programmed)
	assert.Equal(t, metav1.ConditionFalse, programmed.Status)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, programmed.Reason)
	assert.Regexp(t, `^The HTTPProxy could not be programmed due to an internal error and will be retried \(ref: [0-9a-f]{8}\)$`, programmed.Message)
	for _, leak := range []string{"10.", ".svc", "system:"} {
		assert.NotContains(t, programmed.Message, leak)
	}

	_, err = reconciler.Reconcile(context.Background(), mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	})
	require.Error(t, err)
	var retried networkingv1alpha.HTTPProxy
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(httpProxy), &retried))
	assert.Equal(t, updated.ResourceVersion, retried.ResourceVersion, "a retry with the same failure must not rewrite status")

	httpProxyProgramming.mu.Lock()
	_, pending := httpProxyProgramming.pending[httpProxyKey{cluster: "test-cluster", NamespacedName: client.ObjectKeyFromObject(httpProxy)}]
	httpProxyProgramming.mu.Unlock()
	assert.True(t, pending, "an unprogrammed generation is tracked for latency")
}

func TestHTTPProxyMetricsServedByManagerRegistry(t *testing.T) {
	for _, c := range []prometheus.Collector{httpProxyReconcileErrorsTotal, httpProxyProgrammingDuration, httpProxyFleet} {
		var already prometheus.AlreadyRegisteredError
		assert.ErrorAs(t, ctrlmetrics.Registry.Register(c), &already, "must be registered on the registry the manager serves at /metrics")
	}
}

func TestHTTPProxyReconcileCountsRedirectRejectedByValidation(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	httpProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		controllerutil.AddFinalizer(h, httpProxyFinalizer)
		h.Spec.Rules[0].Filters = []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
				Scheme:     ptr.To("https"),
				StatusCode: ptr.To(308),
			},
		}}
	})

	var sawRedirect bool
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(httpProxy).
		WithStatusSubresource(httpProxy).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				route, ok := obj.(*gatewayv1.HTTPRoute)
				if !ok {
					return c.Create(ctx, obj, opts...)
				}
				for i, rule := range route.Spec.Rules {
					for j, f := range rule.Filters {
						if f.RequestRedirect != nil && f.RequestRedirect.StatusCode != nil && *f.RequestRedirect.StatusCode == 308 {
							sawRedirect = true
							path := field.NewPath("spec", "rules").Index(i).Child("filters").Index(j).Child("requestRedirect", "statusCode")
							return apierrors.NewInvalid(
								schema.GroupKind{Group: gatewayv1.GroupName, Kind: "HTTPRoute"},
								route.Name,
								field.ErrorList{field.NotSupported(path, 308, []string{"301", "302"})},
							)
						}
					}
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	reconciler := &HTTPProxyReconciler{
		mgr: &fakeMockManager{cl: fakeClient},
		Config: config.NetworkServicesOperator{
			HTTPProxy: config.HTTPProxyConfig{GatewayClassName: "test-gateway-class"},
			Gateway: config.GatewayConfig{
				ControllerName: gatewayv1.GatewayController("test-gateway-class"),
				TargetDomain:   "example.com",
			},
		},
	}

	invalid := httpProxyReconcileErrorsTotal.WithLabelValues("httproute", "invalid")
	before := testutil.ToFloat64(invalid)

	_, _ = reconciler.Reconcile(context.Background(), mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	})
	require.True(t, sawRedirect, "the derived HTTPRoute must carry the 308 redirect")

	assert.Equal(t, before+1, testutil.ToFloat64(invalid))

	var updated networkingv1alpha.HTTPProxy
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(httpProxy), &updated))
	programmed := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
	require.NotNil(t, programmed)
	assert.Equal(t, metav1.ConditionFalse, programmed.Status)

	const rejection = "spec.rules[0].filters[0].requestRedirect.statusCode: Unsupported value: 308"
	accepted := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha.HTTPProxyConditionAccepted)
	require.NotNil(t, accepted)
	assert.True(t,
		strings.Contains(programmed.Message, rejection) || strings.Contains(accepted.Message, rejection),
		"the rejection must reach the HTTPProxy status; programmed=%q accepted=%q", programmed.Message, accepted.Message)
	assert.NotEqual(t, httpProxyDefaultProgrammedMessage+"|"+"The HTTPProxy has not been scheduled", programmed.Message+"|"+accepted.Message)
	assert.NotContains(t, programmed.Message, "failed updating")
	assert.NotContains(t, programmed.Message, "gateway.networking.k8s.io")
}

func reconcileHTTPProxyOnce(t *testing.T, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
	t.Helper()
	reconciler := &HTTPProxyReconciler{
		mgr: &fakeMockManager{cl: cl},
		Config: config.NetworkServicesOperator{
			HTTPProxy: config.HTTPProxyConfig{GatewayClassName: "test-gateway-class"},
			Gateway: config.GatewayConfig{
				ControllerName: gatewayv1.GatewayController("test-gateway-class"),
				TargetDomain:   "example.com",
			},
		},
	}
	_, err := reconciler.Reconcile(context.Background(), mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	})
	var updated networkingv1alpha.HTTPProxy
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(httpProxy), &updated))
	return &updated, err
}

func fullHTTPProxyTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	require.NoError(t, discoveryv1.AddToScheme(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	return s
}

func TestHTTPProxyReconcileCollectInternalErrorGetsFixedMessage(t *testing.T) {
	httpProxy := newNetworkServiceProxy()
	controllerutil.AddFinalizer(httpProxy, httpProxyFinalizer)

	cl := fake.NewClientBuilder().
		WithScheme(fullHTTPProxyTestScheme(t)).
		WithObjects(httpProxy).
		WithStatusSubresource(httpProxy).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*networkingv1alpha.NetworkService); ok {
					return apierrors.NewForbidden(
						schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "networkservices"}, key.Name,
						errors.New(`User "system:serviceaccount:network-services-operator-system:nso" cannot get resource; webhook authz.nso.svc at 10.40.0.12 refused`),
					)
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	counter := httpProxyReconcileErrorsTotal.WithLabelValues("validate", "forbidden")
	before := testutil.ToFloat64(counter)

	first, err := reconcileHTTPProxyOnce(t, cl, httpProxy)
	require.Error(t, err)
	assert.Equal(t, before+1, testutil.ToFloat64(counter))

	programmed := apimeta.FindStatusCondition(first.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
	require.NotNil(t, programmed)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonPending, programmed.Reason)
	assert.Regexp(t, `^The HTTPProxy could not be programmed due to an internal error and will be retried \(ref: [0-9a-f]{8}\)$`, programmed.Message)
	for _, leak := range []string{"10.", ".svc", "system:", "storefront"} {
		assert.NotContains(t, programmed.Message, leak)
	}
	assert.False(t, strings.HasPrefix(programmed.Message, "The HTTPProxy cannot be programmed:"),
		"an internal error must not read as a configuration the user has to change")

	second, err := reconcileHTTPProxyOnce(t, cl, httpProxy)
	require.Error(t, err)
	assert.Equal(t, first.ResourceVersion, second.ResourceVersion, "a retry with the same failure must not rewrite status")
}

func TestHTTPProxyReconcileCollectKeepsUserFacingBackendMessages(t *testing.T) {
	t.Run("network service not found", func(t *testing.T) {
		httpProxy := newNetworkServiceProxy()
		controllerutil.AddFinalizer(httpProxy, httpProxyFinalizer)
		cl := fake.NewClientBuilder().WithScheme(fullHTTPProxyTestScheme(t)).WithObjects(httpProxy).WithStatusSubresource(httpProxy).Build()

		updated, err := reconcileHTTPProxyOnce(t, cl, httpProxy)
		require.NoError(t, err)
		programmed := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
		require.NotNil(t, programmed)
		assert.Equal(t, networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound, programmed.Reason)
		assert.Equal(t, `The HTTPProxy cannot be programmed: referenced NetworkService "storefront" not found`, programmed.Message)
	})

	t.Run("instance backend not found", func(t *testing.T) {
		httpProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
			controllerutil.AddFinalizer(h, httpProxyFinalizer)
			h.Spec.Rules[0].Backends[0] = networkingv1alpha.HTTPProxyRuleBackend{
				Instance: &networkingv1alpha.InstanceBackendRef{Name: "web-0", Port: 8080},
			}
		})
		cl := fake.NewClientBuilder().WithScheme(fullHTTPProxyTestScheme(t)).WithObjects(httpProxy).WithStatusSubresource(httpProxy).Build()

		updated, err := reconcileHTTPProxyOnce(t, cl, httpProxy)
		require.NoError(t, err)
		programmed := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
		require.NotNil(t, programmed)
		assert.Equal(t, networkingv1alpha.HTTPProxyReasonInstanceBackendNotFound, programmed.Reason)
		assert.Equal(t, `The HTTPProxy cannot be programmed: referenced EndpointSlice "web-0" not found`, programmed.Message)
	})
}
