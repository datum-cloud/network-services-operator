// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

type fakeBackendDNS struct {
	addrs map[string][]string
	calls int
}

func (f *fakeBackendDNS) observer(now *time.Time) *backendAddressObserver {
	return &backendAddressObserver{
		lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			f.calls++
			ips, ok := f.addrs[host]
			if !ok {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			out := make([]net.IPAddr, 0, len(ips))
			for _, ip := range ips {
				out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
			}
			return out, nil
		},
		now:   func() time.Time { return *now },
		cache: map[string]backendAddressObservation{},
	}
}

func programmedTrue() metav1.Condition {
	return metav1.Condition{
		Type:    networkingv1alpha.HTTPProxyConditionProgrammed,
		Status:  metav1.ConditionTrue,
		Reason:  networkingv1alpha.HTTPProxyReasonProgrammed,
		Message: "The HTTPProxy has been programmed",
	}
}

func proxyWithBackends(backends ...networkingv1alpha.HTTPProxyRuleBackend) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{Spec: networkingv1alpha.HTTPProxySpec{
		Rules: []networkingv1alpha.HTTPProxyRule{{Backends: backends}},
	}}
}

func TestApplyBackendAddressNotPublic(t *testing.T) {
	dns := map[string][]string{
		"inward.example.com.":   {"8.8.8.8", "10.0.0.5"},
		"public.example.com.":   {"93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"},
		"mapped.example.com.":   {"::ffff:192.168.1.1"},
		"docs.example.com.":     {"203.0.113.7"},
		"connector.example.com": {"10.0.0.9"},
	}

	tests := []struct {
		name        string
		proxy       *networkingv1alpha.HTTPProxy
		condition   metav1.Condition
		wantRecheck bool
		wantReason  string
		wantMessage []string
	}{
		{
			name:        "name resolving to a private address is flagged",
			proxy:       proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://inward.example.com"}),
			condition:   programmedTrue(),
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic,
			wantMessage: []string{"inward.example.com", "10.0.0.5", "private range 10.0.0.0/8", "Backend 0 in rule 0", "as resolved from the control plane"},
		},
		{
			name:        "IPv4 answers returned in mapped form are judged as IPv4",
			proxy:       proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "http://mapped.example.com:8080"}),
			condition:   programmedTrue(),
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic,
			wantMessage: []string{"192.168.1.1", "private range 192.168.0.0/16"},
		},
		{
			name:        "documentation ranges are flagged",
			proxy:       proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://docs.example.com"}),
			condition:   programmedTrue(),
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic,
			wantMessage: []string{"documentation range 203.0.113.0/24"},
		},
		{
			name: "second backend is named",
			proxy: proxyWithBackends(
				networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://public.example.com"},
				networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://inward.example.com"},
			),
			condition:   programmedTrue(),
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic,
			wantMessage: []string{"Backend 1 in rule 0"},
		},
		{
			name:        "public answers leave the condition alone but keep watching",
			proxy:       proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://public.example.com"}),
			condition:   programmedTrue(),
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:        "lookup failures leave the condition alone",
			proxy:       proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://missing.example.com"}),
			condition:   programmedTrue(),
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name: "connector backends are not resolved",
			proxy: proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{
				Endpoint:  "http://connector.example.com",
				Connector: &networkingv1alpha.ConnectorReference{Name: "c"},
			}),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:       "address literals are not resolved",
			proxy:      proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "http://8.8.8.8"}),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:  "an earlier failure is not replaced",
			proxy: proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://inward.example.com"}),
			condition: metav1.Condition{
				Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
				Status: metav1.ConditionFalse,
				Reason: networkingv1alpha.HTTPProxyReasonConflict,
			},
			wantRecheck: true,
			wantReason:  networkingv1alpha.HTTPProxyReasonConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			r := &HTTPProxyReconciler{backendAddresses: (&fakeBackendDNS{addrs: dns}).observer(&now)}
			condition := tt.condition
			recheck := r.applyBackendAddressNotPublic(context.Background(), tt.proxy, &condition)
			assert.Equal(t, tt.wantRecheck, recheck)
			assert.Equal(t, tt.wantReason, condition.Reason)
			if tt.wantReason == networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic {
				assert.Equal(t, metav1.ConditionFalse, condition.Status)
			}
			for _, want := range tt.wantMessage {
				assert.Contains(t, condition.Message, want)
			}
		})
	}
}

func TestApplyBackendAddressNotPublicWithoutObserver(t *testing.T) {
	r := &HTTPProxyReconciler{}
	condition := programmedTrue()
	proxy := proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://inward.example.com"})
	assert.False(t, r.applyBackendAddressNotPublic(context.Background(), proxy, &condition))
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
}

func TestBackendAddressObserverCachesForAMinute(t *testing.T) {
	now := time.Now()
	dns := &fakeBackendDNS{addrs: map[string][]string{"inward.example.com.": {"10.0.0.5"}}}
	o := dns.observer(&now)

	_, nonPublic := o.nonPublicAnswer(context.Background(), "inward.example.com")
	assert.True(t, nonPublic)
	_, nonPublic = o.nonPublicAnswer(context.Background(), "Inward.Example.com.")
	assert.True(t, nonPublic)
	assert.Equal(t, 1, dns.calls)

	dns.addrs["inward.example.com."] = []string{"8.8.8.8"}
	now = now.Add(backendAddressObservationTTL)
	_, nonPublic = o.nonPublicAnswer(context.Background(), "inward.example.com")
	assert.False(t, nonPublic, "the condition clears once the name resolves publicly")
	assert.Equal(t, 2, dns.calls)
}

func TestBackendAddressObserverTimesOutLookups(t *testing.T) {
	now := time.Now()
	o := &backendAddressObserver{
		lookupIP: func(ctx context.Context, _ string) ([]net.IPAddr, error) {
			deadline, ok := ctx.Deadline()
			assert.True(t, ok)
			assert.WithinDuration(t, time.Now().Add(backendAddressLookupTimeout), deadline, time.Second)
			return nil, errors.New("timeout")
		},
		now:   func() time.Time { return now },
		cache: map[string]backendAddressObservation{},
	}
	_, nonPublic := o.nonPublicAnswer(context.Background(), "slow.example.com")
	assert.False(t, nonPublic)
}

func TestApplyBackendAddressNotPublicSkipsLookupsWhileUnprogrammed(t *testing.T) {
	now := time.Now()
	dns := &fakeBackendDNS{addrs: map[string][]string{"inward.example.com.": {"10.0.0.5"}}}
	r := &HTTPProxyReconciler{backendAddresses: dns.observer(&now)}
	condition := metav1.Condition{Status: metav1.ConditionFalse, Reason: networkingv1alpha.HTTPProxyReasonPending}
	proxy := proxyWithBackends(networkingv1alpha.HTTPProxyRuleBackend{Endpoint: "https://inward.example.com"})
	assert.True(t, r.applyBackendAddressNotPublic(context.Background(), proxy, &condition))
	assert.Zero(t, dns.calls)
}

func TestBackendAddressLookupsShareOneBudget(t *testing.T) {
	now := time.Now()
	var mu sync.Mutex
	calls := 0
	o := &backendAddressObserver{
		lookupIP: func(ctx context.Context, _ string) ([]net.IPAddr, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-ctx.Done()
			return nil, ctx.Err()
		},
		now:   func() time.Time { return now },
		cache: map[string]backendAddressObservation{},
	}
	hosts := make([]string, 3*backendAddressLookupWorkers)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("slow-%d.example.com", i)
	}

	start := time.Now()
	found := o.observeAll(context.Background(), hosts)
	elapsed := time.Since(start)

	assert.Empty(t, found)
	assert.Less(t, elapsed, backendAddressLookupBudget+500*time.Millisecond)
	assert.Less(t, calls, len(hosts), "hosts left when the budget runs out are skipped")
	assert.Len(t, o.cache, backendAddressLookupWorkers, "only lookups that ran to their own timeout are cached")
}

func TestApplyRouteRefNotPermitted(t *testing.T) {
	addressRefusal := nonPublicBackendAddressMessage([]string{"10.0.0.5 is in the private range"}, []string{"ns-upstream/pod-slice"})
	labelRefusal := `EndpointSlice refused: ns-upstream/pod-slice: label vpc.datum.net/tenant-id="nodash" is not a tenant identifier`
	route := func(generation, observed int64, status metav1.ConditionStatus, reason, message string) *gatewayv1.HTTPRoute {
		return &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Generation: generation},
			Status: gatewayv1.HTTPRouteStatus{RouteStatus: gatewayv1.RouteStatus{Parents: []gatewayv1.RouteParentStatus{{
				Conditions: []metav1.Condition{{
					Type:               string(gatewayv1.RouteConditionResolvedRefs),
					Status:             status,
					Reason:             reason,
					Message:            message,
					ObservedGeneration: observed,
				}},
			}}}},
		}
	}

	tests := []struct {
		name       string
		route      *gatewayv1.HTTPRoute
		condition  metav1.Condition
		wantReason string
	}{
		{
			name:       "refused reference surfaces on the proxy",
			route:      route(2, 2, metav1.ConditionFalse, string(gatewayv1.RouteReasonRefNotPermitted), addressRefusal),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic,
		},
		{
			name:       "stale route status is ignored",
			route:      route(3, 2, metav1.ConditionFalse, string(gatewayv1.RouteReasonRefNotPermitted), addressRefusal),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:       "a label refusal keeps its own wording on the route",
			route:      route(2, 2, metav1.ConditionFalse, string(gatewayv1.RouteReasonRefNotPermitted), labelRefusal),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:       "other unresolved references are not reported as address refusals",
			route:      route(2, 2, metav1.ConditionFalse, string(gatewayv1.RouteReasonBackendNotFound), "EndpointSlice not found: ns/x"),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:       "resolved references leave the condition alone",
			route:      route(2, 2, metav1.ConditionTrue, string(gatewayv1.RouteReasonResolvedRefs), "Resolved"),
			condition:  programmedTrue(),
			wantReason: networkingv1alpha.HTTPProxyReasonProgrammed,
		},
		{
			name:  "an earlier failure is not replaced",
			route: route(2, 2, metav1.ConditionFalse, string(gatewayv1.RouteReasonRefNotPermitted), addressRefusal),
			condition: metav1.Condition{
				Status: metav1.ConditionFalse,
				Reason: networkingv1alpha.HTTPProxyReasonPending,
			},
			wantReason: networkingv1alpha.HTTPProxyReasonPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			condition := tt.condition
			applyRouteRefNotPermitted(tt.route, &condition)
			assert.Equal(t, tt.wantReason, condition.Reason)
			if tt.wantReason == networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic {
				assert.Equal(t, metav1.ConditionFalse, condition.Status)
				assert.Contains(t, condition.Message, "10.0.0.5 is in the private range")
				assert.NotContains(t, condition.Message, "ns-upstream")
				assert.NotContains(t, condition.Message, "pod-slice")
				assert.NotContains(t, condition.Message, "EndpointSlice")
			}
		})
	}
}
