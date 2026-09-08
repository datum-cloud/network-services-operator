// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

func TestBuildHTTPProxy(t *testing.T) {
	t.Parallel()

	proxy, err := BuildHTTPProxy(CreateInput{
		Name:        "my-app",
		DisplayName: "My App",
		Endpoint:    "https://origin.example.com",
		HostHeader:  "origin.example.com",
		Hostnames:   []string{"app.example.com"},
		ForceHTTPS:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, "my-app", proxy.Name)
	assert.Equal(t, "My App", proxy.Annotations[display.AnnotationChosenName])
	assert.Equal(t, []gatewayv1.Hostname{"app.example.com"}, proxy.Spec.Hostnames)
	require.Len(t, proxy.Spec.Rules, 2)
	assert.True(t, isForceHTTPSRedirectRule(proxy.Spec.Rules[0]))
	assert.Equal(t, "https://origin.example.com", proxy.Spec.Rules[1].Backends[0].Endpoint)
	assert.Equal(t, "origin.example.com", HostHeader(proxy))
	assert.True(t, ForceHTTPS(proxy))
}

func TestBuildHTTPProxyRequiresEndpoint(t *testing.T) {
	t.Parallel()
	_, err := BuildHTTPProxy(CreateInput{Name: "my-app"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint is required")
}

func TestBuildHTTPProxyRejectsPath(t *testing.T) {
	t.Parallel()
	_, err := BuildHTTPProxy(CreateInput{
		Name:     "my-app",
		Endpoint: "https://origin.example.com/api",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestApplyHTTPProxyUpdatePreservesConnectorAndHeaders(t *testing.T) {
	t.Parallel()

	current, err := BuildHTTPProxy(CreateInput{
		Name:       "my-app",
		Endpoint:   "https://origin.example.com",
		HostHeader: "origin.example.com",
		ForceHTTPS: true,
	})
	require.NoError(t, err)
	current.Spec.Rules[1].Backends[0].Connector = &networkingv1alpha.ConnectorReference{Name: "edge"}

	updated, err := SetRequestHeader(current, "X-Debug", "1")
	require.NoError(t, err)

	endpoint := "https://other.example.com"
	updated, err = ApplyHTTPProxyUpdate(updated, UpdateInput{Endpoint: &endpoint})
	require.NoError(t, err)
	assert.Equal(t, "https://other.example.com", Endpoint(updated))
	assert.Equal(t, "edge", ConnectorName(updated))
	assert.Equal(t, "origin.example.com", HostHeader(updated))
	set, _, _ := ListRequestHeaders(updated)
	require.GreaterOrEqual(t, len(set), 2)
}

func TestHostnameAddRemove(t *testing.T) {
	t.Parallel()

	proxy, err := BuildHTTPProxy(CreateInput{
		Name:     "my-app",
		Endpoint: "https://origin.example.com",
	})
	require.NoError(t, err)

	proxy, err = AddHostname(proxy, "app.example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"app.example.com"}, Hostnames(proxy))

	_, err = AddHostname(proxy, "app.example.com")
	require.Error(t, err)

	proxy, err = RemoveHostname(proxy, "app.example.com")
	require.NoError(t, err)
	assert.Empty(t, Hostnames(proxy))
}

func TestRequestHeaders(t *testing.T) {
	t.Parallel()

	proxy, err := BuildHTTPProxy(CreateInput{
		Name:     "my-app",
		Endpoint: "https://origin.example.com",
	})
	require.NoError(t, err)

	proxy, err = SetRequestHeader(proxy, "Host", "origin.internal")
	require.NoError(t, err)
	assert.Equal(t, "origin.internal", HostHeader(proxy))

	proxy, err = SetRequestHeader(proxy, "X-Request-Id", "abc")
	require.NoError(t, err)
	set, _, _ := ListRequestHeaders(proxy)
	assert.Len(t, set, 2)

	proxy, err = UnsetRequestHeader(proxy, "X-Request-Id")
	require.NoError(t, err)
	assert.Equal(t, "origin.internal", HostHeader(proxy))
	set, _, _ = ListRequestHeaders(proxy)
	assert.Len(t, set, 1)
}

func TestBuildTPP(t *testing.T) {
	t.Parallel()

	policy, err := BuildTPP(WAFInput{
		ProxyName: "my-app",
		Mode:      networkingv1alpha.TrafficProtectionPolicyEnforce,
		Paranoia:  2,
	})
	require.NoError(t, err)
	assert.Equal(t, "my-app", policy.Name)
	assert.Equal(t, networkingv1alpha.TrafficProtectionPolicyEnforce, policy.Spec.Mode)
	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("my-app"), policy.Spec.TargetRefs[0].Name)
	assert.True(t, TPPTargetsProxy(policy, "my-app"))
	assert.Equal(t, 2, TPPParanoia(policy))
}

func TestGenerateHtpasswd(t *testing.T) {
	t.Parallel()

	content, err := GenerateHtpasswd([]BasicAuthUser{{Username: "admin", Password: "secret"}})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(content, "admin:{SHA}"))
	assert.True(t, strings.HasSuffix(content, "\n"))

	secret, err := BuildBasicAuthSecret("my-app", []BasicAuthUser{{Username: "admin", Password: "secret"}})
	require.NoError(t, err)
	assert.Equal(t, "my-app-basic-auth", secret.Name)
	assert.Equal(t, []string{"admin"}, ParseHtpasswdUsernames(secret))

	policy := BuildSecurityPolicy("my-app", "My App")
	assert.Equal(t, "my-app", policy.Name)
	require.NotNil(t, policy.Spec.BasicAuth)
	assert.Equal(t, gatewayv1.ObjectName("my-app-basic-auth"), policy.Spec.BasicAuth.Users.Name)
}

func TestParseHeaderArg(t *testing.T) {
	t.Parallel()
	name, value, err := ParseHeaderArg("Host=origin.example.com")
	require.NoError(t, err)
	assert.Equal(t, "Host", name)
	assert.Equal(t, "origin.example.com", value)

	_, _, err = ParseHeaderArg("Host")
	require.Error(t, err)
}

func TestParseWAFMode(t *testing.T) {
	t.Parallel()
	mode, err := ParseWAFMode("observe")
	require.NoError(t, err)
	assert.Equal(t, networkingv1alpha.TrafficProtectionPolicyObserve, mode)

	_, err = ParseWAFMode("block")
	require.Error(t, err)
}
