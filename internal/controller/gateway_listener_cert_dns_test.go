// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

type listenerDNS struct {
	cnames map[string]string
	addrs  map[string][]string
	errs   map[string]error
}

func (d listenerDNS) observer() *routingObserver {
	return &routingObserver{
		lookupCNAME: func(_ context.Context, host string) (string, error) {
			if err, ok := d.errs[host]; ok {
				return "", err
			}
			if target, ok := d.cnames[host]; ok {
				return target, nil
			}
			return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		},
		lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			if err, ok := d.errs[host]; ok {
				return nil, err
			}
			if len(d.addrs[host]) == 0 {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			out := make([]net.IPAddr, 0, len(d.addrs[host]))
			for _, a := range d.addrs[host] {
				out = append(out, net.IPAddr{IP: net.ParseIP(a)})
			}
			return out, nil
		},
		now:   time.Now,
		cache: map[string]routingObservation{},
	}
}

func TestWithheldListenerReasonFollowsWhereTheHostnameResolves(t *testing.T) {
	const (
		hostname = "shop.example.com"
		listener = gatewayv1.SectionName("https-shop")
		project  = "dns-reason-project"
	)
	gatewayV4 := "203.0.113.10"
	gatewayV6 := "2001:db8::10"

	testConfig := config.NetworkServicesOperator{Gateway: config.GatewayConfig{TargetDomain: "test-suite.com"}}
	gw := newGateway(testConfig, "default", "dns-reason-gw", func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
			Name:     listener,
			Protocol: gatewayv1.HTTPSProtocolType,
			Port:     DefaultHTTPSPort,
			Hostname: ptr.To(gatewayv1.Hostname(hostname)),
			TLS: &gatewayv1.ListenerTLSConfig{
				Mode: ptr.To(gatewayv1.TLSModeTerminate),
				Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
					gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer",
				},
			},
		})
	})
	canonical := testConfig.Gateway.GatewayDNSAddress(gw)
	canonicalAddrs := map[string][]string{canonical + ".": {gatewayV4, gatewayV6}}

	timeout := &net.DNSError{Err: "i/o timeout", Name: hostname + ".", IsTimeout: true}

	tests := []struct {
		name       string
		dns        listenerDNS
		wantReason string
	}{
		{
			name:       "no records for the hostname is the customer's DNS",
			dns:        listenerDNS{addrs: canonicalAddrs},
			wantReason: listenerCertReasonDNSNotPointingToGateway,
		},
		{
			name: "hostname resolving to another host is the customer's DNS",
			dns: listenerDNS{addrs: map[string][]string{
				canonical + ".": {gatewayV4, gatewayV6},
				hostname + ".":  {"198.51.100.7", "2001:db8:ffff::7"},
			}},
			wantReason: listenerCertReasonDNSNotPointingToGateway,
		},
		{
			name: "hostname resolving to the gateway is a platform failure",
			dns: listenerDNS{addrs: map[string][]string{
				canonical + ".": {gatewayV4, gatewayV6},
				hostname + ".":  {gatewayV4},
			}},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name: "hostname CNAMEd to the gateway is a platform failure",
			dns: listenerDNS{
				cnames: map[string]string{hostname + ".": canonical + "."},
				addrs:  canonicalAddrs,
			},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name: "a failed lookup does not hide the failure",
			dns: listenerDNS{
				addrs: canonicalAddrs,
				errs:  map[string]error{hostname + ".": timeout},
			},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name: "a failed lookup of the gateway's own hostname does not hide the failure",
			dns: listenerDNS{
				addrs: map[string][]string{hostname + ".": {"198.51.100.7"}},
				errs:  map[string]error{canonical + ".": timeout},
			},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name:       "no DNS observer does not hide the failure",
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := runtime.NewScheme()
			require.NoError(t, scheme.AddToScheme(testScheme))
			require.NoError(t, cmv1.AddToScheme(testScheme))

			downstreamNamespace := fmt.Sprintf("ns-%s", uuid.NewUUID())
			downstream := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(newDownstreamListenerCertObjects(t, downstreamNamespace, listenerCertState{
					gatewayName: gw.Name, listenerName: listener, hostname: hostname, ready: false,
				})...).
				Build()

			r := &GatewayReconciler{Config: testConfig}
			if tt.dns.addrs != nil || tt.dns.errs != nil || tt.dns.cnames != nil {
				r.routing = tt.dns.observer()
			}

			health := r.evaluateListenerCertHealth(context.Background(), project, nil, downstream, downstreamNamespace, gw, []string{hostname})
			status, ok := health[listener]
			require.True(t, ok)
			require.False(t, status.healthy)
			assert.Equal(t, gatewayv1.ListenerReasonInvalidCertificateRef, status.reason, "the customer-facing reason is unchanged")

			recordListenerCertMetrics(context.Background(), project, gw, health)
			t.Cleanup(func() { clearListenerCertMetrics(project, gw.Namespace, gw.Name) })

			withheld := func(reason string) float64 {
				return testutil.ToFloat64(gatewayListenerCertWithheld.With(prometheus.Labels{
					metricLabelProject: project, metricLabelResourceNamespace: gw.Namespace, metricLabelResourceName: gw.Name,
					metricLabelListener: string(listener), metricLabelHostname: hostname, metricLabelReason: reason,
				}))
			}
			assert.Equal(t, 1.0, withheld(tt.wantReason))
			assert.Equal(t, 1, withheldSeries(t, project), "one withheld series for the listener")
		})
	}
}

func withheldSeries(t *testing.T, project string) int {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	gatewayListenerCertWithheld.Collect(ch)
	close(ch)
	count := 0
	for m := range ch {
		var out dto.Metric
		require.NoError(t, m.Write(&out))
		for _, l := range out.GetLabel() {
			if l.GetName() == metricLabelProject && l.GetValue() == project {
				count++
			}
		}
	}
	return count
}

func TestRoutingObserverOnlyProvesElsewhereFromAnAnswer(t *testing.T) {
	t.Parallel()

	const canonical = "gw.test-suite.com"
	ctx := context.Background()
	base := map[string][]string{canonical + ".": {"203.0.113.10"}}

	nx := listenerDNS{addrs: base}.observer()
	assert.Equal(t, routingElsewhere, nx.observe(ctx, "gone.example.com", canonical))

	servfail := listenerDNS{addrs: base, errs: map[string]error{
		"flaky.example.com.": &net.DNSError{Err: "server misbehaving", Name: "flaky.example.com.", IsTemporary: true},
	}}.observer()
	assert.Equal(t, routingUnknown, servfail.observe(ctx, "flaky.example.com", canonical))
	assert.False(t, servfail.routesTo(ctx, "flaky.example.com", canonical))

	noCanonical := listenerDNS{addrs: map[string][]string{"elsewhere.example.com.": {"198.51.100.7"}}}.observer()
	assert.Equal(t, routingUnknown, noCanonical.observe(ctx, "elsewhere.example.com", canonical))
}

func TestWildcardListenerReasonFollowsDNSDelegation(t *testing.T) {
	const (
		hostname = "*.shop.example.com"
		listener = gatewayv1.SectionName("https-wildcard")
		project  = "delegation-reason-project"
	)
	now := time.Now()
	old := metav1.NewTime(now.Add(-48 * time.Hour))
	notReady := metav1.Condition{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: old}
	delegation := func(status metav1.ConditionStatus) metav1.Condition {
		return metav1.Condition{Type: certificatesv1alpha1.ConditionDNSDelegationReady, Status: status, Reason: "Checked", LastTransitionTime: old}
	}

	tests := []struct {
		name       string
		conditions []metav1.Condition
		wantReason string
	}{
		{
			name:       "delegation authoritatively missing is the customer's DNS",
			conditions: []metav1.Condition{delegation(metav1.ConditionFalse), notReady},
			wantReason: listenerCertReasonDNSDelegationMissing,
		},
		{
			name:       "delegation unknown still counts as a platform failure",
			conditions: []metav1.Condition{delegation(metav1.ConditionUnknown), notReady},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name:       "no delegation condition still counts as a platform failure",
			conditions: []metav1.Condition{notReady},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name:       "delegation in place and certificate stuck is a platform failure",
			conditions: []metav1.Condition{delegation(metav1.ConditionTrue), notReady},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
		{
			name: "a rejected request stays a platform failure whatever the delegation says",
			conditions: []metav1.Condition{
				delegation(metav1.ConditionFalse),
				{Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionFalse, Reason: "Denied", Message: "denied"},
			},
			wantReason: string(gatewayv1.ListenerReasonInvalidCertificateRef),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := newCertificateServiceTestScheme(t)
			testConfig := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
			gw := newGateway(testConfig, "default", "delegation-gw", func(g *gatewayv1.Gateway) {
				g.UID = uuid.NewUUID()
				g.Spec.Listeners = append(g.Spec.Listeners, tlsListener(listener, hostname))
			})

			cert := &certificatesv1alpha1.TLSCertificate{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         gw.Namespace,
					Name:              tlsCertificateName(gw.Name, listener),
					CreationTimestamp: old,
				},
				Status: certificatesv1alpha1.TLSCertificateStatus{Conditions: tt.conditions},
			}
			upstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(cert).Build()
			downstream := fake.NewClientBuilder().WithScheme(testScheme).Build()

			r := &GatewayReconciler{Config: testConfig}
			status := r.listenerTLSCertificateHealth(context.Background(), project, upstream, downstream, "downstream-ns", gw, listener, hostname, now)
			require.False(t, status.healthy)
			assert.Equal(t, gatewayv1.ListenerReasonInvalidCertificateRef, status.reason, "the customer-facing reason is unchanged")

			health := map[gatewayv1.SectionName]listenerCertStatus{listener: status}
			recordListenerCertMetrics(context.Background(), project, gw, health)
			t.Cleanup(func() { clearListenerCertMetrics(project, gw.Namespace, gw.Name) })

			got := testutil.ToFloat64(gatewayListenerCertWithheld.With(prometheus.Labels{
				metricLabelProject: project, metricLabelResourceNamespace: gw.Namespace, metricLabelResourceName: gw.Name,
				metricLabelListener: string(listener), metricLabelHostname: hostname, metricLabelReason: tt.wantReason,
			}))
			assert.Equal(t, 1.0, got)
			assert.Equal(t, 1, withheldSeries(t, project), "one withheld series for the listener")
		})
	}
}
