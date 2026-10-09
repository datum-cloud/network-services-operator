// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// TestHTTPProxyHostnameStatusFollowsDNSRecordSet checks that a hostname's
// DNSRecordProgrammed condition follows its record set without waiting for an
// unrelated event to reconcile the HTTPProxy.
func TestHTTPProxyHostnameStatusFollowsDNSRecordSet(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	log.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, s := startDNSStatusEnv(t)
	c, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)
	startHTTPProxyManager(t, ctx, cfg, s)

	const ns, name, hostname = "default", "proxy", "app.example.test"
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: []gatewayv1.Hostname{hostname},
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "http://www.example.com"}},
			}},
		},
	}
	require.NoError(t, c.Create(ctx, proxy))

	var gateway gatewayv1.Gateway
	eventually(t, "the HTTPProxy's Gateway", func() bool {
		return c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &gateway) == nil
	})

	recordSet := gatewayRecordSet(ns, name, hostname)
	require.NoError(t, c.Create(ctx, recordSet))
	setProgrammed(t, ctx, c, recordSet, metav1.ConditionFalse, "Pending")

	// The Gateway controller is not running, so accept the Gateway by hand.
	// The HTTPProxy controller builds hostname statuses only once it is.
	apimeta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gatewayv1.GatewayReasonAccepted),
		ObservedGeneration: gateway.Generation,
	})
	require.NoError(t, c.Status().Update(ctx, &gateway))
	eventually(t, "DNSRecordProgrammed=False on the hostname", func() bool {
		return hostnameDNSCondition(ctx, c, ns, name, hostname).Status == metav1.ConditionFalse
	})

	programmedAt := time.Now()
	setProgrammed(t, ctx, c, recordSet, metav1.ConditionTrue, "Programmed")
	eventually(t, "DNSRecordProgrammed=True after the record set is programmed", func() bool {
		cond := hostnameDNSCondition(ctx, c, ns, name, hostname)
		return cond.Status == metav1.ConditionTrue && cond.Reason == networkingv1alpha.DNSRecordReasonCreated
	})
	t.Logf("the hostname status followed the record set in %s", time.Since(programmedAt).Round(time.Millisecond))
}

func startDNSStatusEnv(t *testing.T) (*rest.Config, *runtime.Scheme) {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "go.miloapis.com/dns-operator").Output()
	require.NoError(t, err)
	env := &envtest.Environment{
		CRDDirectoryPaths: append([]string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "config", "tools", "envoy-gateway", "charts",
				"gateway-helm-v0.0.0-latest", "gateway-helm", "crds", "generated"),
			filepath.Join(strings.TrimSpace(string(out)), "config", "crd", "bases"),
		}, installedGatewayAPICRDPaths(t)...),
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	require.NoError(t, discoveryv1.AddToScheme(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	require.NoError(t, dnsv1alpha1.AddToScheme(s))
	return cfg, s
}

func startHTTPProxyManager(t *testing.T, ctx context.Context, cfg *rest.Config, s *runtime.Scheme) {
	t.Helper()
	provider := &staticProvider{clusters: map[string]cluster.Cluster{}}
	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:     s,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	const clusterName = "project"
	provider.clusters[clusterName] = mgr.GetLocalManager()

	operatorConfig := config.NetworkServicesOperator{}
	config.SetObjectDefaults_NetworkServicesOperator(&operatorConfig)
	operatorConfig.Gateway.TargetDomain = "example.com"
	operatorConfig.Gateway.DefaultListenerTLSSecretName = "default-listener-tls"
	operatorConfig.Gateway.EnableDNSIntegration = true
	operatorConfig.Gateway.CertificateService.Enabled = false

	r := &HTTPProxyReconciler{Config: operatorConfig}
	require.NoError(t, r.SetupWithManager(mgr))
	require.NoError(t, mgr.Engage(ctx, clusterName, mgr.GetLocalManager()))
	go func() { _ = mgr.Start(ctx) }()
}

// gatewayRecordSet returns the record set the Gateway controller writes for a
// hostname, labelled with the Gateway it was written for.
func gatewayRecordSet(ns, gatewayName, hostname string) *dnsv1alpha1.DNSRecordSet {
	return &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      gatewayName + "-app",
			Labels: map[string]string{
				labelDNSManaged:    labelValueTrue,
				labelDNSSourceKind: KindGateway,
				labelDNSSourceName: gatewayName,
				labelDNSSourceNS:   ns,
			},
			Annotations: map[string]string{annotationDNSHostname: hostname},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: "example-test"},
			RecordType: dnsv1alpha1.RRTypeCNAME,
			Records: []dnsv1alpha1.RecordEntry{{
				Name:  "app",
				CNAME: &dnsv1alpha1.CNAMERecordSpec{Content: "proxy.example.com."},
			}},
		},
	}
}

func setProgrammed(t *testing.T, ctx context.Context, c client.Client, rs *dnsv1alpha1.DNSRecordSet, status metav1.ConditionStatus, reason string) {
	t.Helper()
	apimeta.SetStatusCondition(&rs.Status.Conditions, metav1.Condition{
		Type: conditionTypeProgrammed, Status: status, Reason: reason, ObservedGeneration: rs.Generation,
	})
	require.NoError(t, c.Status().Update(ctx, rs))
}

func hostnameDNSCondition(ctx context.Context, c client.Client, ns, name, hostname string) metav1.Condition {
	var proxy networkingv1alpha.HTTPProxy
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &proxy); err != nil {
		return metav1.Condition{}
	}
	for _, hs := range proxy.Status.HostnameStatuses {
		if hs.Hostname != hostname {
			continue
		}
		if cond := apimeta.FindStatusCondition(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed); cond != nil {
			return *cond
		}
	}
	return metav1.Condition{}
}

// eventually waits for ok, well inside the five minutes after which the
// HTTPProxy controller rechecks a hostname on its own.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
