// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestControllerMetricsAreServedByTheManager(t *testing.T) {
	gatewayProgrammedTotal.WithLabelValues("default", "gateway").Set(0)
	gatewayListenerCertWithheld.WithLabelValues("default", "gateway", "https", "example.com", "InvalidCertificateRef").Set(1)
	gatewayListenerCertGatingTotal.WithLabelValues("default", "gateway", "https", "example.com", "InvalidCertificateRef").Inc()
	gatewayListenerCertExpiryTime.WithLabelValues("default", "gateway", "https", "example.com", "secret").Set(0)
	gatewayListenerCertManaged.WithLabelValues("default", "gateway", "https", "example.com").Set(1)
	replicatorConflictsTotal.WithLabelValues("Gateway").Inc()
	replicatorSyncDuration.WithLabelValues("Gateway", "success").Observe(0)
	missingAllocationsTotal.WithLabelValues("project").Inc()
	certificateServiceFailuresTotal.WithLabelValues("default", "gateway", "https", "IssuanceFailed").Inc()
	certificateServiceListenerFailing.WithLabelValues("default", "gateway", "https", "IssuanceFailed").Set(1)

	gathered, err := ctrlmetrics.Registry.Gather()
	require.NoError(t, err)

	served := make(map[string]struct{}, len(gathered))
	for _, family := range gathered {
		served[family.GetName()] = struct{}{}
	}

	for _, name := range []string{
		"nso_gateway_programmed_total",
		"nso_gateway_listener_cert_withheld",
		"nso_gateway_listener_cert_gating_total",
		"nso_gateway_listener_cert_expiry_time",
		"nso_gateway_listener_cert_managed",
		"nso_replicator_conflicts_total",
		"nso_replicator_sync_duration_seconds",
		"nso_certificate_service_failures_total",
		"nso_certificate_service_listener_failing",
		"nso_network_interface_missing_allocations_total",
	} {
		_, ok := served[name]
		require.Truef(t, ok, "%s is not registered on the registry the manager serves at /metrics", name)
	}
}
