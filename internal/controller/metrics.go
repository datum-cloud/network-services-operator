// SPDX-License-Identifier: AGPL-3.0-only

// Package controller defines and registers Prometheus metrics for NSO's
// gateway operator controllers. All metrics use the "nso_" prefix and are
// registered against prometheus.DefaultRegisterer, which is the same registry
// that controller-runtime exposes at /metrics. No separate registry is used.
package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// replicatorConflictsTotal counts resource-version conflicts observed by the
	// gateway-resource-replicator controller. Conflicts arise when the upstream or
	// downstream API server rejects an update because the local object's
	// resourceVersion is stale (concurrent writes). The controller-runtime retries
	// automatically, so conflicts are not fatal — but a rising rate indicates the
	// replication path is saturated under high churn.
	replicatorConflictsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_replicator_conflicts_total",
			Help: "Total resource-version conflicts encountered by the gateway-resource-replicator controller, by resource kind.",
		},
		[]string{metricLabelResourceKind},
	)

	// replicatorSyncDuration is a histogram of the time taken to sync one
	// upstream resource to the downstream cluster (CreateOrUpdate + status mirror).
	// Labeled by resource kind and outcome (success | error) so per-family latency
	// regressions are attributable.
	replicatorSyncDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nso_replicator_sync_duration_seconds",
			Help:    "Duration of a single downstream resource sync (CreateOrUpdate + optional status mirror) by resource kind and outcome.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{metricLabelResourceKind, "outcome"},
	)

	// replicatorStatusMirrorErrorsTotal counts failures to mirror upstream status
	// to the downstream resource in mirrorUpstreamStatusToDownstream. Under the
	// two-cluster topology, downstream consumers (e.g. the extension server) read
	// Connector status from the local edge cluster; mirror failures mean the
	// extension server sees stale (potentially incorrect) connector liveness state.
	replicatorStatusMirrorErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_replicator_status_mirror_errors_total",
			Help: "Total upstream→downstream status mirror failures in the gateway-resource-replicator controller, by resource kind.",
		},
		[]string{metricLabelResourceKind},
	)

	// gatewayProgrammedTotal is a per-gateway gauge that is set to 1 when the
	// upstream Gateway's downstream copy has Programmed=True and 0 when it does
	// not. Sum across all label sets to get the fleet-wide programmed count:
	//   sum(nso_gateway_programmed_total)
	// A value below the total gateway count indicates partial fleet failure —
	// either data-plane capacity exhaustion or a config programming failure for
	// the unprogrammed gateways.
	gatewayProgrammedTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_gateway_programmed_total",
			Help: "1 if the downstream Gateway has Programmed=True, 0 otherwise. Sum for fleet-wide programmed count.",
		},
		[]string{jsonKeyNamespace, jsonKeyName},
	)
)
