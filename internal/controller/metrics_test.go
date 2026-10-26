// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/clusterdiscovery"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

func TestControllerMetricsAreServedByTheManager(t *testing.T) {
	gatewayLabels := []string{"test-project", "metrics-test", "gateway"}
	listenerLabels := append(append([]string{}, gatewayLabels...), "https", "example.com")
	certServiceLabels := []string{"test-project", "metrics-test", "gateway", "https", "Failed"}

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

func TestCertificateServiceMetricsKeepProjectsApart(t *testing.T) {
	const listener = gatewayv1.SectionName("https-0")
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "shared-gw", UID: uuid.NewUUID()}}
	projects := []string{"project-a", "project-b"}
	r := &GatewayReconciler{}
	for _, project := range projects {
		recordCertificateServiceFailure(project, gw, listener, certificateServiceReasonRejected)
		publishCertificateServiceFailing(project, gw, map[gatewayv1.SectionName]string{listener: certificateServiceReasonRejected})
		t.Cleanup(func() { r.forgetGateway(project, gw) })
	}

	series := func(vec prometheus.Collector, project string) int {
		ch := make(chan prometheus.Metric, 64)
		vec.Collect(ch)
		close(ch)
		count := 0
		for metric := range ch {
			var m dto.Metric
			require.NoError(t, metric.Write(&m))
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels[metricLabelProject] == project && labels[metricLabelResourceNamespace] == gw.Namespace && labels[metricLabelResourceName] == gw.Name {
				count++
			}
		}
		return count
	}
	for _, project := range projects {
		require.Equal(t, 1, series(certificateServiceFailuresTotal, project), project)
		require.Equal(t, 1, series(certificateServiceListenerFailing, project), project)
	}

	publishCertificateServiceFailing("project-a", gw, nil)
	require.Zero(t, series(certificateServiceListenerFailing, "project-a"))
	require.Equal(t, 1, series(certificateServiceListenerFailing, "project-b"), "one project's recovery must not clear another's failing listener")

	r.forgetGateway("project-a", gw)
	require.Zero(t, series(certificateServiceFailuresTotal, "project-a"))
	require.Equal(t, 1, series(certificateServiceFailuresTotal, "project-b"), "one project's gateway deletion must not drop another's failures")
	require.Equal(t, 1, series(certificateServiceListenerFailing, "project-b"))
}

type engagedClusterNames chan multicluster.ClusterName

func (e engagedClusterNames) Engage(_ context.Context, name multicluster.ClusterName, _ cluster.Cluster) error {
	e <- name
	return nil
}

func TestSingleClusterDiscoveryLabelsMetricsWithTheEngagedClusterName(t *testing.T) {
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx, cancel := context.WithCancel(log.IntoContext(context.Background(), logger))
	t.Cleanup(cancel)

	withoutCertificates := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(withoutCertificates))
	require.NoError(t, gatewayv1.Install(withoutCertificates))
	require.NoError(t, discoveryv1.AddToScheme(withoutCertificates))
	require.NoError(t, networkingv1alpha.AddToScheme(withoutCertificates))
	downstreamScheme := newCertificateServiceTestScheme(t)

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "single-test", UID: uuid.NewUUID()}}
	const hostname = "*.shop.example.com"
	testCfg := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
	upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, "single-gw", func(g *gatewayv1.Gateway) {
		g.UID = uuid.NewUUID()
		g.Spec.Listeners = append(g.Spec.Listeners, tlsListener("https-hostname-0", hostname))
	})
	domain := dnsProvenDomain(upstreamNamespace.Name)
	gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}
	route := newHTTPRoute(upstreamNamespace.Name, "app", func(r *gatewayv1.HTTPRoute) {
		r.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(upstreamGateway.Name)}}
		r.Spec.Hostnames = []gatewayv1.Hostname{hostname}
	})
	for _, obj := range []client.Object{domain, gatewayClass, route} {
		obj.SetUID(uuid.NewUUID())
		obj.SetCreationTimestamp(metav1.Now())
	}
	fakeUpstreamClient := fake.NewClientBuilder().WithScheme(withoutCertificates).
		WithObjects(upstreamGateway, upstreamNamespace, domain, gatewayClass, route).
		WithStatusSubresource(upstreamGateway, route).Build()
	fakeDownstreamClient := fake.NewClientBuilder().WithScheme(downstreamScheme).
		WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).
		WithStatusSubresource(&gatewayv1.Gateway{}).Build()

	_, provider, _, err := clusterdiscovery.Initialize(
		config.DiscoveryConfig{Mode: multiclusterproviders.ProviderSingle},
		config.ClientConnectionConfig{},
		&fakeCluster{cl: fakeUpstreamClient},
		withoutCertificates,
	)
	require.NoError(t, err)
	runnable, ok := provider.(multicluster.ProviderRunnable)
	require.True(t, ok)

	engaged := make(engagedClusterNames, 1)
	go func() { _ = runnable.Start(ctx, engaged) }()
	var clusterName multicluster.ClusterName
	select {
	case clusterName = <-engaged:
	case <-time.After(10 * time.Second):
		t.Fatal("single-cluster discovery never engaged a cluster")
	}
	require.NotEmpty(t, clusterName)

	reconciler := &GatewayReconciler{
		mgr:                      &fakeMockManager{cl: fakeUpstreamClient},
		Config:                   testCfg,
		DownstreamCluster:        &fakeCluster{cl: fakeDownstreamClient},
		CertificateServiceReader: fake.NewClientBuilder().WithScheme(downstreamScheme).Build(),
		WildcardEntitlements:     fakeWildcardEntitlements{entitled: true},
	}
	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(string(clusterName), fakeUpstreamClient, fakeDownstreamClient)
	prepareUpstreamGateway(reconciler, upstreamGateway)
	t.Cleanup(func() { clearListenerCertMetrics(string(clusterName), upstreamGateway.Namespace, upstreamGateway.Name) })

	result, _ := reconciler.ensureDownstreamGateway(ctx, string(clusterName), fakeUpstreamClient, upstreamGateway, downstreamStrategy)
	require.NoError(t, result.Err)

	managed := testutil.ToFloat64(gatewayListenerCertManaged.With(prometheus.Labels{
		metricLabelProject: string(clusterName), metricLabelResourceNamespace: upstreamGateway.Namespace, metricLabelResourceName: upstreamGateway.Name,
		metricLabelListener: "https-hostname-0", metricLabelHostname: hostname,
	}))
	require.Equal(t, 1.0, managed, "the project label is the cluster name single-cluster discovery engaged")
}
