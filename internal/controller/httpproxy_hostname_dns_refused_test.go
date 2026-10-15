// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// TestHTTPProxyReportsARefusedDNSRecord covers issue #537: one hostname's DNS
// record set is refused while every listener is accepted. The HTTPProxy must
// say so in a condition that names the hostname, and must report success once
// the record set is programmed.
// TestHTTPProxyCarriesItsGatewaysDNSRecordsCondition checks that an HTTPProxy
// reports DNSRecordsProgrammed as its Gateway does, as it reports Accepted and
// Programmed, while HostnamesVerified keeps reporting ownership (#537).
func TestHTTPProxyCarriesItsGatewaysDNSRecordsCondition(t *testing.T) {
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

	const ns, name, refused, served = "default", "proxy", "app.example.test", "www.example.test"
	gateway := refusedRecordProxy(t, ctx, c, ns, name, refused, served)
	key := client.ObjectKey{Namespace: ns, Name: name}

	// The Gateway controller is not running, so its DNS aggregate is written by hand.
	setGatewayDNSRecords(t, ctx, c, gateway, metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, 0)
	eventuallyProxyDNSRecords(t, ctx, c, key, metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure)
	require.True(t, proxyCondition(ctx, c, key, networkingv1alpha.HTTPProxyConditionHostnamesVerified).Status == metav1.ConditionTrue,
		"HostnamesVerified reports ownership and stays True while a record is refused")

	setGatewayDNSRecords(t, ctx, c, gateway, metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, 0)
	eventuallyProxyDNSRecords(t, ctx, c, key, metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated)

	// A condition the Gateway wrote for an earlier generation of itself may be
	// about other hostnames, so it is never carried as True.
	setGatewayDNSRecords(t, ctx, c, gateway, metav1.ConditionTrue, networkingv1alpha.DNSRecordsProgrammedReasonAllCreated, -1)
	eventuallyProxyDNSRecords(t, ctx, c, key, metav1.ConditionUnknown, networkingv1alpha.HTTPProxyReasonPending)

	// No hostname uses Datum-managed DNS: the Gateway reports no condition, nor does the HTTPProxy.
	removeGatewayDNSRecords(t, ctx, c, gateway)
	eventually(t, "no DNSRecordsProgrammed on the HTTPProxy once its Gateway reports none", func() bool {
		return proxyCondition(ctx, c, key, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed).Type == ""
	})
}

// refusedRecordProxy creates an HTTPProxy with two hostnames, accepts its
// Gateway by hand, and gives the hostnames one refused and one programmed
// record set, as dns-operator reports a PowerDNS refusal.
func refusedRecordProxy(t *testing.T, ctx context.Context, c client.Client, ns, name, refused, served string) *gatewayv1.Gateway {
	t.Helper()
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(refused), gatewayv1.Hostname(served)},
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "http://www.example.com"}},
			}},
		},
	}
	require.NoError(t, c.Create(ctx, proxy))

	gateway := &gatewayv1.Gateway{}
	eventually(t, "the HTTPProxy's Gateway with a listener per hostname", func() bool {
		return c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, gateway) == nil &&
			listenerHostnames(gateway) >= 2
	})
	refusedSet := gatewayRecordSet(ns, name, refused)
	require.NoError(t, c.Create(ctx, refusedSet))
	servedSet := gatewayRecordSet(ns, name, served)
	servedSet.Name = name + "-www"
	servedSet.Spec.Records[0].Name = "www"
	require.NoError(t, c.Create(ctx, servedSet))
	setRefused(t, ctx, c, refusedSet)
	setProgrammed(t, ctx, c, servedSet, metav1.ConditionTrue, "Programmed")

	acceptGatewayAndListeners(t, ctx, c, gateway)
	eventually(t, "the hostname statuses, built once the Gateway is accepted", func() bool {
		return hostnameDNSCondition(ctx, c, ns, name, refused).Status == metav1.ConditionFalse
	})
	return gateway
}

// setGatewayDNSRecords writes the Gateway's DNSRecordsProgrammed as the Gateway
// controller does, for the Gateway's generation plus generationOffset.
func setGatewayDNSRecords(t *testing.T, ctx context.Context, c client.Client, gateway *gatewayv1.Gateway, status metav1.ConditionStatus, reason string, generationOffset int64) {
	t.Helper()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(gateway), gateway))
	apimeta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
		Type: networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed, Status: status, Reason: reason,
		Message:            "1/2 hostnames have DNS records programmed; see per-hostname conditions for details",
		ObservedGeneration: gateway.Generation + generationOffset,
	})
	require.NoError(t, c.Status().Update(ctx, gateway))
}

func removeGatewayDNSRecords(t *testing.T, ctx context.Context, c client.Client, gateway *gatewayv1.Gateway) {
	t.Helper()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(gateway), gateway))
	apimeta.RemoveStatusCondition(&gateway.Status.Conditions, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
	require.NoError(t, c.Status().Update(ctx, gateway))
}

func eventuallyProxyDNSRecords(t *testing.T, ctx context.Context, c client.Client, key client.ObjectKey, status metav1.ConditionStatus, reason string) {
	t.Helper()
	eventually(t, "DNSRecordsProgrammed="+string(status)+" "+reason+" on the HTTPProxy", func() bool {
		cond := proxyCondition(ctx, c, key, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
		return cond.Status == status && cond.Reason == reason
	})
}

// proxyCondition returns the HTTPProxy's condition of conditionType, or an empty condition.
func proxyCondition(ctx context.Context, c client.Client, key client.ObjectKey, conditionType string) metav1.Condition {
	var proxy networkingv1alpha.HTTPProxy
	if c.Get(ctx, key, &proxy) != nil {
		return metav1.Condition{}
	}
	if cond := apimeta.FindStatusCondition(proxy.Status.Conditions, conditionType); cond != nil {
		return *cond
	}
	return metav1.Condition{}
}

// listenerHostnames counts the Gateway's listeners that carry a hostname.
func listenerHostnames(gateway *gatewayv1.Gateway) int {
	n := 0
	for _, l := range gateway.Spec.Listeners {
		if l.Hostname != nil {
			n++
		}
	}
	return n
}

// acceptGatewayAndListeners accepts the Gateway and every listener by hand: the
// Gateway controller is not running.
func acceptGatewayAndListeners(t *testing.T, ctx context.Context, c client.Client, gateway *gatewayv1.Gateway) {
	t.Helper()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(gateway), gateway))
	apimeta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
		Type: string(gatewayv1.GatewayConditionAccepted), Status: metav1.ConditionTrue,
		Reason: string(gatewayv1.GatewayReasonAccepted), ObservedGeneration: gateway.Generation,
	})
	gateway.Status.Listeners = nil
	for _, l := range gateway.Spec.Listeners {
		gateway.Status.Listeners = append(gateway.Status.Listeners, gatewayv1.ListenerStatus{
			Name:           l.Name,
			SupportedKinds: []gatewayv1.RouteGroupKind{{Kind: "HTTPRoute"}},
			Conditions: []metav1.Condition{{
				Type: string(gatewayv1.ListenerConditionAccepted), Status: metav1.ConditionTrue,
				Reason: string(gatewayv1.ListenerReasonAccepted), ObservedGeneration: gateway.Generation,
				LastTransitionTime: metav1.Now(),
			}},
		})
	}
	require.NoError(t, c.Status().Update(ctx, gateway))
}

// setRefused marks the record set refused the way dns-operator reports a
// PowerDNS refusal.
func setRefused(t *testing.T, ctx context.Context, c client.Client, rs *dnsv1alpha1.DNSRecordSet) {
	t.Helper()
	apimeta.SetStatusCondition(&rs.Status.Conditions, metav1.Condition{
		Type: conditionTypeProgrammed, Status: metav1.ConditionFalse, Reason: "PDNSError",
		Message:            "RRset app.example.test. IN ALIAS: Conflicts with pre-existing RRset",
		ObservedGeneration: rs.Generation,
	})
	require.NoError(t, c.Status().Update(ctx, rs))
}
