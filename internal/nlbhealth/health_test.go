// SPDX-License-Identifier: AGPL-3.0-only

package nlbhealth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func healthFixture() *networkingv1alpha.NetworkLoadBalancerBackendHealth {
	return &networkingv1alpha.NetworkLoadBalancerBackendHealth{
		Spec: networkingv1alpha.NetworkLoadBalancerBackendHealthSpec{
			RouteRef: networkingv1alpha.NetworkLoadBalancerBackendHealthRouteRef{UID: types.UID("route-1"), Generation: 2},
			Targets: []networkingv1alpha.NetworkLoadBalancerBackendHealthTarget{{
				Name: "origin", SourceUID: types.UID("interface-1"), AttachmentUID: types.UID("attachment-1"),
				Address: "fd20::1", Probe: networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeTCP, Port: 8080},
			}},
		},
	}
}

func TestHealthyTargetsRequiresFreshMatchingObservation(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	health := healthFixture()
	health.Generation = 4
	target := health.Spec.Targets[0]
	first := NextTargetStatus(health, target, nil, nil, now)
	require.False(t, first.Healthy)
	second := NextTargetStatus(health, target, &first, nil, now.Add(ProbeInterval))
	require.True(t, second.Healthy)
	health.Status.Targets = []networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus{second}
	require.Empty(t, HealthyTargets(health, now.Add(ProbeInterval)))
	health.Status.Conditions = []metav1.Condition{{Type: "ProbesEvaluated", Status: metav1.ConditionTrue, Reason: "Evaluated", ObservedGeneration: health.Generation, LastTransitionTime: metav1.NewTime(now)}}
	require.Len(t, HealthyTargets(health, now.Add(ProbeInterval)), 1)

	cases := []struct {
		name   string
		change func(*networkingv1alpha.NetworkLoadBalancerBackendHealth)
		at     time.Time
	}{
		{name: "expired", at: now.Add(ProbeInterval + ResultLifetime)},
		{name: "pending evaluation", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) {
			h.Status.Conditions[0].Status = metav1.ConditionUnknown
		}},
		{name: "stale evaluation", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) {
			h.Status.Conditions[0].ObservedGeneration--
		}},
		{name: "new object generation", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) { h.Generation++ }},
		{name: "new route UID", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) { h.Spec.RouteRef.UID = "route-2" }},
		{name: "new route generation", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) { h.Spec.RouteRef.Generation++ }},
		{name: "replacement interface", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) {
			h.Spec.Targets[0].SourceUID = "interface-2"
		}},
		{name: "replacement attachment", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) {
			h.Spec.Targets[0].AttachmentUID = "attachment-2"
		}},
		{name: "probe changed", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) { h.Spec.Targets[0].Probe.Port++ }},
		{name: "future probe timestamp", change: func(h *networkingv1alpha.NetworkLoadBalancerBackendHealth) {
			h.Status.Targets[0].LastProbeTime = h.Status.Targets[0].ValidUntil
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			copy := health.DeepCopy()
			if tt.change != nil {
				tt.change(copy)
			}
			at := now.Add(ProbeInterval)
			if !tt.at.IsZero() {
				at = tt.at
			}
			require.Empty(t, HealthyTargets(copy, at))
		})
	}

	failed := NextTargetStatus(health, target, &second, errors.New("connection refused"), now.Add(2*ProbeInterval))
	require.False(t, failed.Healthy)
	require.Equal(t, int32(0), failed.ConsecutiveSuccesses)
	health.Status.Targets = []networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus{failed}
	require.Empty(t, HealthyTargets(health, now.Add(2*ProbeInterval)))
}

func TestProbeRequiresVPCDialerAndRejectsUnsafeTarget(t *testing.T) {
	target := healthFixture().Spec.Targets[0]
	require.ErrorContains(t, (Prober{}).Probe(context.Background(), "vpc-1", target), "VPC-aware")
	prober := Prober{DialContext: func(context.Context, string, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		if err := b.Close(); err != nil {
			return nil, err
		}
		return a, nil
	}}
	require.NoError(t, prober.Probe(context.Background(), "vpc-1", target))
	target.Address = "::1"
	require.ErrorContains(t, prober.Probe(context.Background(), "vpc-1", target), "routable IPv6")
	target.Address = "192.0.2.1"
	require.ErrorContains(t, prober.Probe(context.Background(), "vpc-1", target), "routable IPv6")
}

func TestHTTPAndHTTPSProbeResults(t *testing.T) {
	target := healthFixture().Spec.Targets[0]
	target.Probe = networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeHTTP, Port: 8080, Path: "/health"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dial := func(ctx context.Context, vpc, network, _ string) (net.Conn, error) {
		require.Equal(t, "vpc-1", vpc)
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	require.NoError(t, (Prober{DialContext: dial}).Probe(context.Background(), "vpc-1", target))
	target.Probe.Path = "/missing"
	require.ErrorContains(t, (Prober{DialContext: dial}).Probe(context.Background(), "vpc-1", target), "404")

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer tlsServer.Close()
	cert := tlsServer.Certificate()
	serverName := ""
	if len(cert.DNSNames) > 0 {
		serverName = cert.DNSNames[0]
	} else {
		serverName = cert.IPAddresses[0].String()
	}
	target.Probe = networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeHTTPS, Port: 8080, ServerName: serverName}
	tlsDial := func(ctx context.Context, _, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, tlsServer.Listener.Addr().String())
	}
	require.Error(t, (Prober{DialContext: tlsDial}).Probe(context.Background(), "vpc-1", target))
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	require.NoError(t, (Prober{DialContext: tlsDial, RootCAs: roots}).Probe(context.Background(), "vpc-1", target))
}

func TestProbeHonorsCallerDeadline(t *testing.T) {
	target := healthFixture().Spec.Targets[0]
	prober := Prober{DialContext: func(ctx context.Context, _, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.ErrorIs(t, prober.Probe(ctx, "vpc-1", target), context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
}

func TestHTTPSDefaultsToVerifiedIPSAN(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "health"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("fd20::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots.AddCert(parsed)
	prober := Prober{
		RootCAs: roots,
		DialContext: func(ctx context.Context, _, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	target := healthFixture().Spec.Targets[0]
	target.Probe = networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeHTTPS, Port: 8080}
	require.NoError(t, prober.Probe(context.Background(), "vpc-1", target))
	target.Address = "fd20::2"
	require.Error(t, prober.Probe(context.Background(), "vpc-1", target))
}
