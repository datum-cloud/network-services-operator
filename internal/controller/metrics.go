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

// Metric label name constants for TLS certificate health metrics.
const (
	metricLabelListener = "listener"
	metricLabelHostname = "hostname"
	metricLabelSecret   = "secret"
	metricLabelReason   = "reason"
	metricLabelProject  = "project"
	metricLabelLocation = "location"
	metricLabelSource   = "source"
)

var (
	// missingAllocationsTotal counts addresses no IPClaim holds. IPAM may give
	// the same address to another claim.
	missingAllocationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_network_interface_missing_allocations_total",
			Help: "Total addresses advertised by a NetworkInterface with no IPClaim holding them, by project.",
		},
		[]string{metricLabelProject},
	)

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
	// upstream resource to the downstream cluster (CreateOrUpdate). Labeled by
	// resource kind and outcome (success | error) so per-family latency
	// regressions are attributable.
	replicatorSyncDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nso_replicator_sync_duration_seconds",
			Help:    "Duration of a single downstream resource sync (CreateOrUpdate) by resource kind and outcome.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{metricLabelResourceKind, "outcome"},
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

	// gatewayListenerCertWithheld is 1 for each upstream Gateway listener that NSO
	// is currently withholding from the downstream because its TLS certificate is
	// unusable. The series for a listener is removed when the listener recovers,
	// is removed from the Gateway, or the Gateway is deleted.
	//
	// Use sum(nso_gateway_listener_cert_withheld) to count how many listeners are
	// currently dark across the fleet, or filter by namespace/name/listener/hostname
	// to find the specific affected object during an incident.
	gatewayListenerCertWithheld = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_gateway_listener_cert_withheld",
			Help: "1 when a Gateway listener is withheld from the downstream because its TLS certificate is unusable, 0 after it recovers.",
		},
		[]string{jsonKeyNamespace, jsonKeyName, metricLabelListener, metricLabelHostname, metricLabelReason},
	)

	// gatewayListenerCertGatingTotal counts every reconcile cycle in which a
	// Gateway listener is withheld due to an unusable certificate. A rising rate
	// means new cert failures are arriving, not just that existing ones persist.
	gatewayListenerCertGatingTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_gateway_listener_cert_gating_total",
			Help: "Total reconcile cycles in which a Gateway listener was withheld because its TLS certificate was unusable.",
		},
		[]string{jsonKeyNamespace, jsonKeyName, metricLabelListener, metricLabelHostname, metricLabelReason},
	)

	// gatewayListenerCertExpiryTime is the Unix timestamp (seconds) at which a
	// managed Gateway listener's TLS certificate expires. Only set for listeners
	// with a healthy certificate whose expiry is known. Use this to alert before
	// a cert expires and NSO begins gating the listener.
	//
	// Query time-to-expiry in days:
	//   (nso_gateway_listener_cert_expiry_time - time()) / 86400
	gatewayListenerCertExpiryTime = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_gateway_listener_cert_expiry_time",
			Help: "Unix timestamp when the managed TLS certificate for this Gateway listener expires. Only present when the certificate is healthy.",
		},
		[]string{jsonKeyNamespace, jsonKeyName, metricLabelListener, metricLabelHostname, metricLabelSecret},
	)

	// gatewayListenerCertManaged counts the total number of Gateway listeners
	// that NSO evaluates for certificate health each reconcile. Together with
	// gatewayListenerCertWithheld this gives the fraction of managed listeners
	// that are currently serving (the SLI ratio).
	gatewayListenerCertManaged = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_gateway_listener_cert_managed",
			Help: "1 for each Gateway listener whose TLS certificate is managed and evaluated by NSO, regardless of health.",
		},
		[]string{jsonKeyNamespace, jsonKeyName, metricLabelListener, metricLabelHostname},
	)

	// locationSourceTotal and locationPublishedTotal are sampled in one pass.
	// The difference between them is the count of locations not published:
	//   nso_location_source_total - nso_location_published_total
	locationSourceTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nso_location_source_total",
			Help: "Locations at the source that carry a city code and are therefore publishable.",
		},
	)

	locationPublishedTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nso_location_published_total",
			Help: "ServingLocations this publisher currently owns on the federation hub, excluding copies retained by a blocked removal.",
		},
	)

	// locationRetainedTotal counts copies kept because their removal is
	// blocked. They are excluded from locationPublishedTotal.
	locationRetainedTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nso_location_retained_total",
			Help: "Published ServingLocations retained because their removal is blocked. Alert on a threshold measured in days, not minutes.",
		},
	)

	locationPublishTimestamp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_location_publish_timestamp_seconds",
			Help: "Unix timestamp at which this location's published content last changed.",
		},
		[]string{metricLabelLocation},
	)

	locationRemovalBlocked = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_location_removal_blocked",
			Help: "1 while a published ServingLocation is retained because its removal guard refuses the delete, by reason.",
		},
		[]string{metricLabelLocation, metricLabelReason},
	)

	locationMatchedClusters = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_location_matched_clusters",
			Help: "Ready hub clusters labelled for this location. Zero means the generated policy places the copy nowhere.",
		},
		[]string{metricLabelLocation},
	)

	locationPublishMismatch = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_location_publish_mismatch",
			Help: "1 while a source Location cannot be published, by reason. A refusal is reported, never silently skipped.",
		},
		[]string{metricLabelLocation, metricLabelReason},
	)

	locationPublisherConflictsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nso_location_publisher_conflicts_total",
			Help: "Conflicts observed by the location publisher, by location and reason.",
		},
		[]string{metricLabelLocation, metricLabelReason},
	)

	cellLocationIdentitySource = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_cell_location_identity_source",
			Help: "1 for the source this cell resolved its location identity from (Delivered or Configured).",
		},
		[]string{metricLabelSource, metricLabelLocation},
	)

	cellLocationIdentityMismatch = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_cell_location_identity_mismatch",
			Help: "1 while a cell's delivered ServingLocation disagrees with its configured location. Delivered wins; the disagreement is still wrong.",
		},
		[]string{metricLabelLocation},
	)

	cellLocationIdentityWaiting = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nso_cell_location_identity_waiting",
			Help: "1 while a cell cannot name the location it serves, by reason.",
		},
		[]string{metricLabelReason},
	)
)
