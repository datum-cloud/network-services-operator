// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func weighted(endpoint string, weight int32) networkingv1alpha.HTTPProxyRuleBackend {
	return networkingv1alpha.HTTPProxyRuleBackend{Endpoint: endpoint, Weight: ptr.To(weight)}
}

func TestShareLabels(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"50%", "50%"}, ShareLabels([]networkingv1alpha.HTTPProxyRuleBackend{
		{Endpoint: "https://a.example.com"}, {Endpoint: "https://b.example.com"},
	}))
	assert.Equal(t, []string{"38%", "62%"}, ShareLabels([]networkingv1alpha.HTTPProxyRuleBackend{
		weighted("https://a.example.com", 3), weighted("https://b.example.com", 5),
	}))
	assert.Equal(t, []string{"<1%", "100%"}, ShareLabels([]networkingv1alpha.HTTPProxyRuleBackend{
		weighted("https://a.example.com", 1), weighted("https://b.example.com", 1000),
	}))
	assert.Equal(t, []string{"0%", "100%"}, ShareLabels([]networkingv1alpha.HTTPProxyRuleBackend{
		weighted("https://a.example.com", 0), {Endpoint: "https://b.example.com"},
	}))
	assert.Equal(t, []string{"0%"}, ShareLabels([]networkingv1alpha.HTTPProxyRuleBackend{weighted("https://a.example.com", 0)}))
}

func TestAddRouteBackendSuggestsWeight(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://a.example.com"))
	updated, err := AddRouteBackend(proxy, "/", urlBackend("https://b.example.com"))
	require.NoError(t, err)
	assert.Nil(t, UserRoutes(updated)[0].Backends[1].Weight, "no weights set: stay on the API default")

	idx := routeIndex(proxy, "/")
	proxy.Spec.Rules[idx].Backends = []networkingv1alpha.HTTPProxyRuleBackend{
		weighted("https://a.example.com", 50), weighted("https://b.example.com", 50),
	}
	updated, err = AddRouteBackend(proxy, "/", urlBackend("https://c.example.com"))
	require.NoError(t, err)
	assert.Equal(t, int32(50), *UserRoutes(updated)[0].Backends[2].Weight)

	updated, err = AddRouteBackend(proxy, "/", BackendInput{Endpoint: "https://d.example.com", Weight: ptr.To[int32](5)})
	require.NoError(t, err)
	assert.Equal(t, int32(5), *UserRoutes(updated)[0].Backends[2].Weight)
}

func TestReplaceRouteBackendsKeepsSettingsOfRemainingOrigins(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://a.example.com"))
	idx := routeIndex(proxy, "/")
	proxy.Spec.Rules[idx].Backends = []networkingv1alpha.HTTPProxyRuleBackend{
		{
			Endpoint: "https://a.example.com/",
			Weight:   ptr.To[int32](70),
			TLS:      &networkingv1alpha.HTTPProxyBackendTLS{Hostname: ptr.To("a.internal")},
		},
		weighted("https://b.example.com", 30),
	}

	updated, err := ReplaceRouteBackends(proxy, "/", []BackendInput{
		urlBackend("https://a.example.com"), urlBackend("https://c.example.com"),
	})
	require.NoError(t, err)
	backends := UserRoutes(updated)[0].Backends
	require.Len(t, backends, 2)
	assert.Equal(t, int32(70), *backends[0].Weight)
	assert.Equal(t, "a.internal", *backends[0].TLS.Hostname)
	assert.Nil(t, backends[1].Weight)
}

func TestSetRouteBackendWeight(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://a.example.com"), storefrontBackend())
	updated, err := SetRouteBackendWeight(proxy, "/", storefrontBackend(), 3)
	require.NoError(t, err)
	backends := UserRoutes(updated)[0].Backends
	assert.Nil(t, backends[0].Weight)
	assert.Equal(t, int32(3), *backends[1].Weight)

	updated, err = SetRouteBackendWeight(updated, "/", urlBackend("https://a.example.com"), 0)
	require.NoError(t, err)
	assert.Equal(t, int32(0), *UserRoutes(updated)[0].Backends[0].Weight)

	_, err = SetRouteBackendWeight(updated, "/", storefrontBackend(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "serve no traffic")

	_, err = SetRouteBackendWeight(proxy, "/", urlBackend("https://missing.example.com"), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not on route")

	_, err = SetRouteBackendWeight(proxy, "/", storefrontBackend(), MaxBackendWeight+1)
	require.Error(t, err)
}

func TestRemoveRouteBackendRefusesDrainedPool(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://a.example.com"))
	idx := routeIndex(proxy, "/")
	proxy.Spec.Rules[idx].Backends = []networkingv1alpha.HTTPProxyRuleBackend{
		weighted("https://a.example.com", 0), weighted("https://b.example.com", 1),
	}
	_, err := RemoveRouteBackend(proxy, "/", urlBackend("https://b.example.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "serve no traffic")
}

func TestBuildLoadBalancer(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"RoundRobin", "round-robin", "round_robin", "ROUNDROBIN"} {
		lb, err := BuildLoadBalancer(LoadBalancerInput{Algorithm: in})
		require.NoError(t, err, in)
		assert.Equal(t, networkingv1alpha.HTTPProxyLoadBalancerTypeRoundRobin, lb.Type, in)
		assert.Nil(t, lb.ConsistentHash, in)
	}

	lb, err := BuildLoadBalancer(LoadBalancerInput{Algorithm: "consistent-hash"})
	require.NoError(t, err)
	assert.Equal(t, networkingv1alpha.HTTPProxyConsistentHashTypeSourceIP, lb.ConsistentHash.Type)

	lb, err = BuildLoadBalancer(LoadBalancerInput{HashHeader: "X-User-ID"})
	require.NoError(t, err)
	assert.Equal(t, networkingv1alpha.HTTPProxyLoadBalancerTypeConsistentHash, lb.Type)
	assert.Equal(t, networkingv1alpha.HTTPProxyConsistentHashTypeHeader, lb.ConsistentHash.Type)
	assert.Equal(t, "X-User-ID", *lb.ConsistentHash.Header)

	lb, err = BuildLoadBalancer(LoadBalancerInput{Algorithm: "default"})
	require.NoError(t, err)
	assert.Nil(t, lb)

	_, err = BuildLoadBalancer(LoadBalancerInput{Algorithm: "random", HashHeader: "X-User-ID"})
	require.Error(t, err)
	_, err = BuildLoadBalancer(LoadBalancerInput{Algorithm: "fastest"})
	require.Error(t, err)
	assert.Equal(t, 2, exitCode(t, err))
}

func TestApplyHealthCheck(t *testing.T) {
	t.Parallel()

	hc, err := ApplyHealthCheck(nil, HealthCheckInput{Enabled: ptr.To(true)})
	require.NoError(t, err)
	require.NotNil(t, hc.Passive)
	assert.Nil(t, hc.Passive.Consecutive5xxErrors, "left for the API default")

	hc, err = ApplyHealthCheck(hc, HealthCheckInput{Consecutive5xxErrors: ptr.To[int32](3)})
	require.NoError(t, err)
	hc, err = ApplyHealthCheck(hc, HealthCheckInput{BaseEjectionTime: ptr.To("1m30s")})
	require.NoError(t, err)
	assert.Equal(t, int32(3), *hc.Passive.Consecutive5xxErrors, "earlier tuning is kept")
	assert.Equal(t, gatewayv1.Duration("1m30s"), *hc.Passive.BaseEjectionTime)

	off, err := ApplyHealthCheck(hc, HealthCheckInput{Enabled: ptr.To(false)})
	require.NoError(t, err)
	assert.Nil(t, off)

	for _, in := range []HealthCheckInput{
		{Enabled: ptr.To(false), MaxEjectionPercent: ptr.To[int32](10)},
		{Consecutive5xxErrors: ptr.To[int32](0)},
		{BaseEjectionTime: ptr.To("30 seconds")},
		{MaxEjectionPercent: ptr.To[int32](0)},
		{MaxEjectionPercent: ptr.To[int32](101)},
	} {
		_, err := ApplyHealthCheck(nil, in)
		require.Error(t, err)
		assert.Equal(t, 2, exitCode(t, err))
	}
}

func TestTrafficSummaries(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://a.example.com"))
	assert.Equal(t, "least request (default)", LoadBalancingSummary(proxy))
	assert.Equal(t, "off", HealthCheckSummary(proxy))

	proxy.Spec.LoadBalancer = &networkingv1alpha.HTTPProxyLoadBalancer{
		Type: networkingv1alpha.HTTPProxyLoadBalancerTypeConsistentHash,
		ConsistentHash: &networkingv1alpha.HTTPProxyConsistentHash{
			Type: networkingv1alpha.HTTPProxyConsistentHashTypeHeader, Header: ptr.To("X-User-ID"),
		},
	}
	assert.Equal(t, "consistent hash on header X-User-ID", LoadBalancingSummary(proxy))

	proxy.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
		Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{Consecutive5xxErrors: ptr.To[int32](3)},
	}
	assert.Equal(t, "passive: eject after 3 consecutive 5xx for 30s, max 50% ejected", HealthCheckSummary(proxy))
}
