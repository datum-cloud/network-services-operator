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
)

// TestHTTPProxyDropsAStaleDNSRecordsProgrammed checks that the HTTPProxy
// controller removes a DNSRecordsProgrammed condition its Gateway does not
// report, and leaves the HTTPProxy's other conditions in place.
func TestHTTPProxyDropsAStaleDNSRecordsProgrammed(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	log.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, s := startDNSStatusEnv(t)
	c, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)

	const ns = "default"
	stale, clean := staleTestProxy(t, ctx, c, ns, "stale"), staleTestProxy(t, ctx, c, ns, "clean")

	// A value its Gateway does not report: the Gateway here has no DNS. It is stored
	// before the controller starts, so its first read holds it: written later, a
	// status write from a cache that has not seen it yet could drop it, and the
	// test would pass without the removal.
	apimeta.SetStatusCondition(&stale.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		Message:            "0/1 hostnames have DNS records programmed",
		ObservedGeneration: stale.Generation,
	})
	require.NoError(t, c.Status().Update(ctx, stale))

	startHTTPProxyManager(t, ctx, cfg, s)

	// Accepting each Gateway by hand reconciles its HTTPProxy.
	acceptTestGateway(t, ctx, c, ns, "stale")
	acceptTestGateway(t, ctx, c, ns, "clean")

	eventually(t, "no DNSRecordsProgrammed on the stale HTTPProxy", func() bool {
		return staleTestCondition(ctx, c, stale, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed) == nil
	})
	for _, proxy := range []*networkingv1alpha.HTTPProxy{stale, clean} {
		for _, kept := range []string{networkingv1alpha.HTTPProxyConditionAccepted, networkingv1alpha.HTTPProxyConditionProgrammed} {
			require.NotNil(t, staleTestCondition(ctx, c, proxy, kept), "%s keeps %s", proxy.Name, kept)
		}
	}
	require.Nil(t, staleTestCondition(ctx, c, clean, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed),
		"an HTTPProxy that never carried DNSRecordsProgrammed gains none")
}

// staleTestProxy creates an HTTPProxy with one hostname, before its controller
// runs.
func staleTestProxy(t *testing.T, ctx context.Context, c client.Client, ns, name string) *networkingv1alpha.HTTPProxy {
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(name + ".example.test")},
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "http://www.example.com"}},
			}},
		},
	}
	require.NoError(t, c.Create(ctx, proxy))
	return proxy
}

// acceptTestGateway accepts an HTTPProxy's Gateway by hand: the Gateway
// controller is not running.
func acceptTestGateway(t *testing.T, ctx context.Context, c client.Client, ns, name string) {
	var gateway gatewayv1.Gateway
	eventually(t, name+"'s Gateway", func() bool {
		return c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &gateway) == nil
	})
	apimeta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gatewayv1.GatewayReasonAccepted),
		ObservedGeneration: gateway.Generation,
	})
	require.NoError(t, c.Status().Update(ctx, &gateway))
}

// staleTestCondition reads one condition of an HTTPProxy as stored, or nil.
func staleTestCondition(ctx context.Context, c client.Client, proxy *networkingv1alpha.HTTPProxy, conditionType string) *metav1.Condition {
	var stored networkingv1alpha.HTTPProxy
	if err := c.Get(ctx, client.ObjectKeyFromObject(proxy), &stored); err != nil {
		return nil
	}
	return apimeta.FindStatusCondition(stored.Status.Conditions, conditionType)
}
