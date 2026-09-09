// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestControllerMetricsAreServedByTheManager(t *testing.T) {
	gatewayProgrammedTotal.WithLabelValues("test-project", "default", "gateway").Set(0)
	gatewayListenerCertWithheld.WithLabelValues("test-project", "default", "gateway", "https", "example.com", "InvalidCertificateRef").Set(1)
	gatewayListenerCertGatingTotal.WithLabelValues("test-project", "default", "gateway", "https", "example.com", "InvalidCertificateRef").Inc()
	gatewayListenerCertExpiryTime.WithLabelValues("test-project", "default", "gateway", "https", "example.com", "secret").Set(0)
	gatewayListenerCertManaged.WithLabelValues("test-project", "default", "gateway", "https", "example.com").Set(1)
	replicatorConflictsTotal.WithLabelValues("Gateway").Inc()
	replicatorSyncDuration.WithLabelValues("Gateway", "success").Observe(0)
	missingAllocationsTotal.WithLabelValues("project").Inc()

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
		"nso_network_interface_missing_allocations_total",
	} {
		_, ok := served[name]
		require.Truef(t, ok, "%s is not registered on the registry the manager serves at /metrics", name)
	}
}

func TestNoControllerMetricRegistersOnTheDefaultGatherer(t *testing.T) {
	gathered, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, family := range gathered {
		name := family.GetName()
		if !strings.HasPrefix(name, "nso_") {
			continue
		}
		require.Truef(t, strings.HasPrefix(name, "nso_extension_"),
			"%s is registered on prometheus.DefaultRegisterer, which the manager never serves; use promauto.With(ctrlmetrics.Registry) instead", name)
	}
}
