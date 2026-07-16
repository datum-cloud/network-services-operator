// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// TestHostnameRemoval_ReapsRecordInSameReconcile is a regression test for
// network-services-operator#283: removing a custom hostname from a Gateway
// left its DNSRecordSet orphaned. ensureHostnameVerification granted a grace
// period for hostnames still present on the downstream gateway's
// pre-reconcile listeners (so a hostname survives a deleted Domain), but
// applied it even when the hostname had been removed from the upstream
// gateway entirely. That kept the hostname "claimed" for one extra reconcile
// pass, and since getDesiredDownstreamGateway only ever copies listeners
// that still exist upstream, nothing forced that follow-up pass to happen --
// the record could orphan indefinitely.
//
// This drives two reconcile passes end-to-end (ensureHostnamesClaimed ->
// getDesiredDownstreamGateway -> ensureDNSRecordSets) against fake upstream
// and downstream clients, removing the hostname between passes exactly as
// the Gateway controller would across two real reconciles.
func TestHostnameRemoval_ReapsRecordInSameReconcile(t *testing.T) {
	const ns = "test-ns"
	const upstreamCluster = "test-suite"
	ctx := log.IntoContext(context.Background(), zap.New())
	s := newDNSTestScheme(t)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "gateways.test.local",
			EnableDNSIntegration:                  true,
		},
	}

	domain := newVerifiedDNSZoneDomain(ns, "ab.dk", true)
	zone := newDNSZone(ns, "ab-dk", "ab.dk")

	gw := newTestGatewayForDNS(ns, "ab-website", func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = []gatewayv1.Listener{
			{
				Name:     "https-hostname-0",
				Port:     DefaultHTTPSPort,
				Protocol: gatewayv1.HTTPSProtocolType,
				Hostname: gwHostname("www.ab.dk"),
			},
		}
	})

	downstreamGateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-downstream",
			Name:      "ab-website",
		},
	}
	downstreamGateway.SetUID(uuid.NewUUID())
	downstreamGateway.SetCreationTimestamp(metav1.Now())

	fakeUpstreamClient := buildFakeUpstreamClientForDNS(s, gw, domain, zone)
	fakeDownstreamClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(downstreamGateway).
		Build()

	mgr := &fakeMockManager{cl: fakeUpstreamClient}
	reconciler := &GatewayReconciler{
		mgr:               mgr,
		Config:            testConfig,
		DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
	}

	rsName := dnsRecordSetName(gw.Name, "www.ab.dk")

	// --- Reconcile pass 1: hostname present, record gets created. ---
	_, claimed1, _, err := reconciler.ensureHostnamesClaimed(ctx, upstreamCluster, fakeUpstreamClient, gw, downstreamGateway)
	require.NoError(t, err)
	require.Contains(t, claimed1, "www.ab.dk", "pass 1: hostname should be claimed")

	desired1 := reconciler.getDesiredDownstreamGateway(ctx, gw, claimed1, nil)
	downstreamGateway.Spec.Listeners = desired1.Spec.Listeners
	require.NoError(t, fakeDownstreamClient.Update(ctx, downstreamGateway))
	require.True(t, hasListenerHostname(downstreamGateway, "www.ab.dk"), "pass 1: downstream should carry the www.ab.dk listener")

	_, dnsResult1 := reconciler.ensureDNSRecordSets(ctx, fakeUpstreamClient, gw, claimed1)
	require.NoError(t, dnsResult1.Err)

	var rs dnsv1alpha1.DNSRecordSet
	require.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: rsName}, &rs),
		"pass 1: DNSRecordSet for www.ab.dk should exist")

	// The fake client doesn't stamp CreationTimestamp on Create the way a real
	// apiserver does; ensureHostnamesClaimed uses CreationTimestamp.IsZero() to
	// detect "already exists", so backfill it to keep the fake client honest
	// about an object that's genuinely already there.
	var hostnameCM corev1.ConfigMap
	require.NoError(t, fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "www.ab.dk"}, &hostnameCM))
	hostnameCM.SetCreationTimestamp(metav1.Now())
	require.NoError(t, fakeDownstreamClient.Update(ctx, &hostnameCM))

	// --- User removes the hostname from the Gateway (e.g. via HTTPProxy edit). ---
	gw.Spec.Listeners = nil
	require.NoError(t, fakeUpstreamClient.Update(ctx, gw))

	// --- Reconcile pass 2: hostname is gone from upstream. The fetched
	// downstream snapshot at the top of this reconcile still has the old
	// listener (it hasn't been updated yet this pass) -- that's exactly the
	// state that used to keep the hostname claimed for an extra cycle. ---
	_, claimed2, _, err := reconciler.ensureHostnamesClaimed(ctx, upstreamCluster, fakeUpstreamClient, gw, downstreamGateway)
	require.NoError(t, err)
	assert.NotContains(t, claimed2, "www.ab.dk",
		"pass 2: a hostname removed from the upstream gateway must not be resurrected by the stale downstream listener")

	desired2 := reconciler.getDesiredDownstreamGateway(ctx, gw, claimed2, nil)
	downstreamGateway.Spec.Listeners = desired2.Spec.Listeners
	require.NoError(t, fakeDownstreamClient.Update(ctx, downstreamGateway))
	assert.False(t, hasListenerHostname(downstreamGateway, "www.ab.dk"), "pass 2: downstream listener is dropped")

	_, dnsResult2 := reconciler.ensureDNSRecordSets(ctx, fakeUpstreamClient, gw, claimed2)
	require.NoError(t, dnsResult2.Err)

	err = fakeUpstreamClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: rsName}, &rs)
	assert.True(t, apierrors.IsNotFound(err), "pass 2: DNSRecordSet is reaped in the same reconcile that removed the hostname")
}

func hasListenerHostname(gw *gatewayv1.Gateway, hostname string) bool {
	for _, l := range gw.Spec.Listeners {
		if l.Hostname != nil && string(*l.Hostname) == hostname {
			return true
		}
	}
	return false
}

func gwHostname(h string) *gatewayv1.Hostname {
	hn := gatewayv1.Hostname(h)
	return &hn
}
