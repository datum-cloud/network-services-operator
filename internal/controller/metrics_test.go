// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestControllerMetricsAreServedByTheManager(t *testing.T) {
	gatewayLabels := []string{"test-project", "metrics-test", "gateway"}
	certGatewayLabels := []string{"metrics-test", "gateway"}
	listenerLabels := append(append([]string{}, certGatewayLabels...), "https", "example.com")
	certServiceLabels := []string{"metrics-test", "gateway", "https", "Failed"}

	touch := []struct {
		vec    *prometheus.MetricVec
		labels []string
	}{
		{gatewayProgrammed.MetricVec, gatewayLabels},
		{gatewayListenerCertWithheld.MetricVec, append(append([]string{}, listenerLabels...), "InvalidCertificateRef")},
		{gatewayListenerCertGatingTotal.MetricVec, append(append([]string{}, listenerLabels...), "InvalidCertificateRef")},
		{gatewayListenerCertExpiryTime.MetricVec, append(append([]string{}, listenerLabels...), "secret")},
		{gatewayListenerCertManaged.MetricVec, listenerLabels},
		{certificateServiceFailuresTotal.MetricVec, certServiceLabels},
		{certificateServiceListenerFailing.MetricVec, certServiceLabels},
		{replicatorConflictsTotal.MetricVec, []string{"MetricsTest"}},
		{replicatorSyncDuration.MetricVec, []string{"MetricsTest", "success"}},
		{missingAllocationsTotal.MetricVec, []string{"metrics-test"}},
		{cellLocationIdentitySource.MetricVec, []string{"metrics-test", "metrics-test"}},
		{cellLocationIdentityMismatch.MetricVec, []string{"metrics-test"}},
		{cellLocationIdentityWaiting.MetricVec, []string{"metrics-test"}},
		{httpProxyReconcileErrorsTotal.MetricVec, []string{"metrics-test", httpProxyErrorReasonOther}},
		{httpProxyProgrammingDuration.MetricVec, []string{httpProxyChangeCreate}},
	}
	for _, m := range touch {
		_, err := m.vec.GetMetricWithLabelValues(m.labels...)
		require.NoError(t, err)
		t.Cleanup(func() { m.vec.DeleteLabelValues(m.labels...) })
	}

	gathered, err := ctrlmetrics.Registry.Gather()
	require.NoError(t, err)

	served := make(map[string]struct{}, len(gathered))
	for _, family := range gathered {
		served[family.GetName()] = struct{}{}
	}

	for _, name := range []string{
		"nso_gateway_programmed",
		"nso_gateway_listener_cert_withheld",
		"nso_gateway_listener_cert_gating_total",
		"nso_gateway_listener_cert_expiry_time",
		"nso_gateway_listener_cert_managed",
		"nso_certificate_service_failures_total",
		"nso_certificate_service_listener_failing",
		"nso_replicator_conflicts_total",
		"nso_replicator_sync_duration_seconds",
		"nso_network_interface_missing_allocations_total",
		"nso_cell_location_identity_source",
		"nso_cell_location_identity_mismatch",
		"nso_cell_location_identity_waiting",
		"nso_httpproxy_reconcile_errors_total",
		"nso_httpproxy_programming_duration_seconds",
	} {
		_, ok := served[name]
		require.Truef(t, ok, "%s is not registered on the registry the manager serves at /metrics", name)
	}
}
