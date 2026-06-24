// Package metrics defines and registers Prometheus metrics for the NSO
// extension server. All metrics use the "nso_extension_" prefix and are
// registered against prometheus.DefaultRegisterer (which is also the registry
// controller-runtime exposes via sigs.k8s.io/controller-runtime/pkg/metrics).
//
// Served by promhttp.Handler() on the /metrics path of the --health-addr HTTP
// server in internal/extensionserver/cmd/run.go.
package metrics

import (
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// outcomeLabel is the Prometheus label key used to tag metric observations by
// their outcome ("success" or "error").
const outcomeLabel = "outcome"

// extBuckets are the shared latency histogram buckets for all extension-server
// histograms. The first 10 boundaries match the original production buckets
// (0.5ms–1s) so existing recordings remain comparable. The tail (2.5/5/10s)
// was added to cover the full-RPC and per-phase histograms whose p99 exceeds
// 1s at N≥10000 — the previous ceiling made those tails invisible.
var extBuckets = []float64{
	0.0005, 0.001, 0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500, 1.0, 2.5, 5.0, 10.0,
}

var (
	// HookDuration is a histogram of PostTranslateModify handler durations,
	// keyed by outcome ("success" | "error"). Covers only handler time —
	// does NOT include gRPC marshal/unmarshal (see RPCDuration for the
	// codec-inclusive companion).
	HookDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nso_extension_hook_duration_seconds",
			Help:    "PostTranslateModify hook handler duration in seconds, by outcome.",
			Buckets: extBuckets,
		},
		[]string{outcomeLabel}, // "success" or "error"
	)

	// HookCallsTotal counts PostTranslateModify invocations, keyed by outcome.
	// Derive call rate and error rate from rate(nso_extension_hook_calls_total).
	HookCallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_extension_hook_calls_total",
			Help: "Total PostTranslateModify hook invocations by outcome.",
		},
		[]string{outcomeLabel}, // "success" or "error"
	)

	// RPCDuration is a histogram of the FULL server-side RPC duration for
	// PostTranslateModify: gRPC unmarshal(request) + handler + marshal(response).
	// The delta (rpc_total − hook_handler) isolates the proto marshal/unmarshal
	// cost of the large (~7.4 MB at N=1000) PostTranslateModify message.
	//
	// Measured via a grpc.StatsHandler (not a unary interceptor) because
	// interceptors run after decode and return before encode — they cannot
	// see codec time. The stats.Handler Begin→End span brackets the whole RPC.
	RPCDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nso_extension_rpc_duration_seconds",
			Help:    "Full server-side gRPC RPC duration in seconds (unmarshal+handler+marshal), by outcome.",
			Buckets: extBuckets,
		},
		[]string{outcomeLabel}, // "success" or "error"
	)

	// PhaseDuration breaks the PostTranslateModify handler into its distinct
	// phases so proto-serialization cost can be attributed apart from the
	// policy-index build and the xDS mutation loops.
	//
	// Phases:
	//   index_build — BuildPolicyIndexFromClient (in-memory cache lookup +
	//                 proto template construction, once per hook invocation).
	//   mutate      — All xDS iteration: WAF HCM injection + per-route TPP +
	//                 connector cluster/route rewiring.
	//
	// NOTE: phase="anypb" (proto pack/unpack cost accumulated inside the mutate
	// loops) is NOT emitted in the production server because the mutate package
	// functions do not expose a Timers accumulator. It is visible in the perf
	// prototype (test/perf/extserver) but would require refactoring the internal
	// mutate package to thread through timing hooks. Track as a future item.
	PhaseDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nso_extension_phase_duration_seconds",
			Help:    "PostTranslateModify per-phase duration in seconds (index_build, mutate).",
			Buckets: extBuckets,
		},
		[]string{"phase"},
	)

	// WAFHCMMutationsTotal counts total Coraza HCM filter injections across all
	// hook invocations. Each increment = one HCM filter injected into one listener.
	// Use rate()/sum() to derive per-build averages.
	WAFHCMMutationsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_waf_hcm_mutations_total",
			Help: "Total Coraza HCM (HttpConnectionManager) filter injections across all hook invocations.",
		},
	)

	// LocalReplyMutationsTotal counts total branded error-page (local_reply_config)
	// injections across all hook invocations. Each increment = one HCM that had a
	// branded local_reply_config attached. Use rate()/sum() to derive per-build
	// averages.
	LocalReplyMutationsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_local_reply_mutations_total",
			Help: "Total branded error-page (local_reply_config) injections across all hook invocations.",
		},
	)

	// WAFRouteMutationsTotal counts total per-route TrafficProtectionPolicy (WAF)
	// config applications across all hook invocations.
	WAFRouteMutationsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_waf_route_mutations_total",
			Help: "Total per-route TrafficProtectionPolicy (WAF) config applications across all hook invocations.",
		},
	)

	// ConnectorClustersTotal counts total connector backend cluster replacements
	// across all hook invocations. One increment = one cluster replaced with a
	// STATIC internal-upstream cluster.
	ConnectorClustersTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_connector_clusters_replaced_total",
			Help: "Total connector backend clusters replaced with STATIC internal-upstream across all hook invocations.",
		},
	)

	// ConnectorRoutesTotal counts total virtual hosts receiving connector route
	// mutations (CONNECT route prepend + offline direct_response) across all hook
	// invocations.
	ConnectorRoutesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_connector_routes_applied_total",
			Help: "Total virtual hosts with connector CONNECT/offline routes applied across all hook invocations.",
		},
	)

	// ConnectorOfflineRoutesTotal counts total user-facing forwarding routes
	// rewritten to a tunnel-offline 503 direct_response across all hook
	// invocations. Each increment = one route that previously targeted an
	// endpoint-less offline-connector cluster and now returns a deterministic
	// 503 (instead of Envoy's generic 503 no_healthy_upstream). Use rate()/sum()
	// to derive per-build averages.
	ConnectorOfflineRoutesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_connector_offline_routes_total",
			Help: "Total user-facing forwarding routes rewritten to a tunnel-offline 503 direct_response across all hook invocations.",
		},
	)

	// TLSPrunedChainsTotal tracks how often a broken certificate had to be
	// dropped from a listener to keep the rest of it serving (issue #212).
	TLSPrunedChainsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_tls_pruned_chains_total",
			Help: "Total TLS filter chains dropped for referencing an invalid (mismatched/expired) certificate across all hook invocations.",
		},
	)

	// TLSPrunedSecretsTotal tracks how many broken certificates were dropped.
	TLSPrunedSecretsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_tls_pruned_secrets_total",
			Help: "Total invalid TLS secrets dropped from the response across all hook invocations.",
		},
	)

	// TLSListenersLeftIntactTotal tracks listeners that were left untouched
	// because every certificate on them was broken; a non-zero value means a
	// listener could not be saved here and still needs the controller-side fix.
	TLSListenersLeftIntactTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_tls_listeners_left_intact_total",
			Help: "Total listeners left intact (not emptied) because all their TLS filter chains were invalid, across all hook invocations.",
		},
	)

	// CacheSynced is 1 when the informer cache has synced and the extension server
	// is accepting gRPC connections, 0 during startup or if the cache lost sync.
	CacheSynced = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nso_extension_cache_synced",
			Help: "1 if the informer cache is synced and the extension server is ready to serve requests, 0 otherwise.",
		},
	)

	// HookPanicsTotal counts panics recovered in the PostTranslateModify handler
	// by the gRPC recovery interceptor. Each recovered panic is returned to EG as
	// UNAVAILABLE so the retry policy (retryableStatusCodes: [UNAVAILABLE]) covers
	// it. Non-zero values indicate a bug in the mutation logic and should alert.
	HookPanicsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_hook_panics_total",
			Help: "Total panics recovered in PostTranslateModify handler (returned to EG as UNAVAILABLE).",
		},
	)

	// TLSReloadsTotal counts successful TLS certificate reloads via GetCertificate.
	// GetCertificate is called on every TLS handshake to pick up cert-manager
	// certificate rotations without a pod restart.
	TLSReloadsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_tls_reloads_total",
			Help: "Total successful TLS certificate reloads via GetCertificate (on each TLS handshake).",
		},
	)

	// TLSReloadErrorsTotal counts TLS certificate reload failures in GetCertificate.
	// A failed reload causes the handshake to fail with a TLS error; if this counter
	// rises after a cert-manager rotation, check file permissions and cert integrity.
	TLSReloadErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nso_extension_tls_reload_errors_total",
			Help: "Total TLS certificate reload failures in GetCertificate (handshake errors on cert-manager rotation problems).",
		},
	)

	// GRPCServerMetrics emits the standard gRPC server golden-signal counters via
	// the go-grpc-middleware prometheus provider:
	//   grpc_server_started_total{grpc_type,grpc_service,grpc_method}
	//   grpc_server_handled_total{grpc_type,grpc_service,grpc_method,grpc_code}
	//   grpc_server_msg_received_total{grpc_type,grpc_service,grpc_method}
	//   grpc_server_msg_sent_total{grpc_type,grpc_service,grpc_method}
	//
	// grpc_server_handled_total{grpc_code="ResourceExhausted"} is the key metric
	// that catches message-ceiling breaches (RESOURCE_EXHAUSTED when the snapshot
	// exceeds grpc.MaxRecvMsgSize / EG's maxMessageSize). Without this metric a
	// fleet-wide stop-programming failure at N≈540 (4 MiB default) is invisible.
	//
	// The metrics object is registered against prometheus.DefaultRegisterer here
	// so it participates in the same /metrics exposition as the nso_extension_*
	// metrics above.
	GRPCServerMetrics = func() *grpcprom.ServerMetrics {
		m := grpcprom.NewServerMetrics()
		prometheus.MustRegister(m)
		return m
	}()
)
