// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func TestUpdateSetsAndClearsLoadBalancerAndHealthChecks(t *testing.T) {
	c := newRouteTestClient(t)

	_, err := run(t, c, "update", "my-app", "--hash-header", "X-User-ID", "--consecutive-5xx", "3")
	require.NoError(t, err)
	proxy := loadTestProxy(t, c)
	require.NotNil(t, proxy.Spec.LoadBalancer)
	assert.Equal(t, networkingv1alpha.HTTPProxyLoadBalancerTypeConsistentHash, proxy.Spec.LoadBalancer.Type)
	assert.Equal(t, "X-User-ID", *proxy.Spec.LoadBalancer.ConsistentHash.Header)
	require.NotNil(t, proxy.Spec.HealthCheck)
	assert.Equal(t, int32(3), *proxy.Spec.HealthCheck.Passive.Consecutive5xxErrors)

	out, err := run(t, c, "describe", "my-app")
	require.NoError(t, err)
	assert.Contains(t, out, "Load balancing:     consistent hash on header X-User-ID")
	assert.Contains(t, out, "Health checks:      passive: eject after 3 consecutive 5xx for 30s, max 50% ejected")

	_, err = run(t, c, "update", "my-app", "--algorithm", "random")
	require.NoError(t, err)
	proxy = loadTestProxy(t, c)
	assert.Equal(t, networkingv1alpha.HTTPProxyLoadBalancerTypeRandom, proxy.Spec.LoadBalancer.Type)
	assert.Nil(t, proxy.Spec.LoadBalancer.ConsistentHash, "merge patch drops the stale hash settings")

	_, err = run(t, c, "update", "my-app", "--algorithm", "default", "--no-health-checks")
	require.NoError(t, err)
	proxy = loadTestProxy(t, c)
	assert.Nil(t, proxy.Spec.LoadBalancer)
	assert.Nil(t, proxy.Spec.HealthCheck)

	out, err = run(t, c, "describe", "my-app")
	require.NoError(t, err)
	assert.Contains(t, out, "Load balancing:     least request (default)")
	assert.Contains(t, out, "Health checks:      off")
}

// Turning settings off relies on the merge patch sending null for them;
// leaving the key out would keep the value on the server.
func TestClearingSettingsPatchesNull(t *testing.T) {
	original := loadTestProxy(t, newRouteTestClient(t))
	original.Spec.LoadBalancer = &networkingv1alpha.HTTPProxyLoadBalancer{Type: networkingv1alpha.HTTPProxyLoadBalancerTypeRandom}
	original.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{}}

	updated, err := spec.ApplyHTTPProxyUpdate(original, spec.UpdateInput{
		LoadBalancer: spec.LoadBalancerInput{Algorithm: spec.AlgorithmDefault},
		HealthCheck:  spec.HealthCheckInput{Enabled: new(bool)},
	})
	require.NoError(t, err)

	data, err := client.MergeFrom(original).Data(updated)
	require.NoError(t, err)
	var patch map[string]map[string]any
	require.NoError(t, json.Unmarshal(data, &patch))
	assert.Contains(t, patch["spec"], "loadBalancer")
	assert.Nil(t, patch["spec"]["loadBalancer"])
	assert.Contains(t, patch["spec"], "healthCheck")
	assert.Nil(t, patch["spec"]["healthCheck"])
}

func TestUpdateRejectsConflictingTrafficFlags(t *testing.T) {
	c := newRouteTestClient(t)
	for _, args := range [][]string{
		{"update", "my-app", "--health-checks", "--no-health-checks"},
		{"update", "my-app", "--no-health-checks", "--max-ejection-percent", "10"},
		{"update", "my-app", "--algorithm", "round-robin", "--hash-header", "X-User-ID"},
		{"update", "my-app", "--algorithm", "fastest"},
		{"update", "my-app", "--base-ejection-time", "soon"},
	} {
		_, err := run(t, c, args...)
		require.Error(t, err, args)
		assert.Equal(t, 2, exitCodeOf(t, err), args)
	}
}

func TestRouteBackendWeights(t *testing.T) {
	c := newRouteTestClient(t)

	_, err := run(t, c, "route", "backend", "add", "my-app", "--path", "/", "--endpoint", "https://b.example.com", "--weight", "3")
	require.NoError(t, err)
	_, err = run(t, c, "route", "backend", "update", "my-app", "--path", "/", "--endpoint", "https://origin.example.com", "--weight", "1")
	require.NoError(t, err)

	backends := spec.UserRoutes(loadTestProxy(t, c))[0].Backends
	require.Len(t, backends, 2)
	assert.Equal(t, int32(1), *backends[0].Weight)
	assert.Equal(t, int32(3), *backends[1].Weight)

	out, err := run(t, c, "route", "backend", "list", "my-app")
	require.NoError(t, err)
	assert.Contains(t, out, "WEIGHT")
	assert.Regexp(t, `https://origin.example.com\s+url\s+1\s+25%`, out)
	assert.Regexp(t, `https://b.example.com\s+url\s+3\s+75%`, out)

	out, err = run(t, c, "describe", "my-app")
	require.NoError(t, err)
	assert.Contains(t, out, "https://b.example.com (url)  weight=3 (75%)")

	_, err = run(t, c, "route", "update", "my-app", "--path", "/",
		"--endpoint", "https://b.example.com", "--endpoint", "https://c.example.com")
	require.NoError(t, err)
	backends = spec.UserRoutes(loadTestProxy(t, c))[0].Backends
	assert.Equal(t, int32(3), *backends[0].Weight, "route update keeps a remaining origin's weight")
	assert.Nil(t, backends[1].Weight)

	_, err = run(t, c, "route", "backend", "update", "my-app", "--path", "/", "--endpoint", "https://b.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to update")

	_, err = run(t, c, "route", "backend", "add", "my-app", "--path", "/", "--endpoint", "https://d.example.com", "--weight", "-1")
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	var cliErr *util.CLIError
	require.ErrorAs(t, err, &cliErr)
	return cliErr.Code()
}
