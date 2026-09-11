// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
)

func urlBackend(endpoint string) BackendInput {
	return BackendInput{Endpoint: endpoint}
}

func storefrontBackend() BackendInput {
	return BackendInput{NetworkService: "storefront", Port: "http"}
}

func newProxy(t *testing.T, backends ...BackendInput) *networkingv1alpha.HTTPProxy {
	t.Helper()
	proxy, err := BuildHTTPProxy(CreateInput{Name: "my-app", Backends: backends, ForceHTTPS: true})
	require.NoError(t, err)
	return proxy
}

func TestBuildHTTPProxy(t *testing.T) {
	t.Parallel()

	proxy, err := BuildHTTPProxy(CreateInput{
		Name:        "my-app",
		DisplayName: "My App",
		Backends:    []BackendInput{urlBackend("https://origin.example.com")},
		HostHeader:  "origin.example.com",
		Hostnames:   []string{"app.example.com"},
		ForceHTTPS:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, "my-app", proxy.Name)
	assert.Equal(t, "My App", proxy.Annotations[DisplayNameAnnotation])
	assert.NotContains(t, proxy.Annotations, display.AnnotationChosenName)
	assert.Equal(t, []gatewayv1.Hostname{"app.example.com"}, proxy.Spec.Hostnames)
	require.Len(t, proxy.Spec.Rules, 2)
	assert.True(t, isForceHTTPSRedirectRule(proxy.Spec.Rules[0]))
	assert.Nil(t, proxy.Spec.Rules[1].Matches)
	assert.Equal(t, "https://origin.example.com", proxy.Spec.Rules[1].Backends[0].Endpoint)
	assert.Equal(t, "origin.example.com", HostHeader(proxy))
	assert.True(t, ForceHTTPS(proxy))
	assert.Equal(t, "https://origin.example.com", OriginSummary(proxy))
}

func TestBuildHTTPProxyNetworkServiceDefaultRoute(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, storefrontBackend())
	backend := proxy.Spec.Rules[1].Backends[0]
	require.NotNil(t, backend.NetworkService)
	assert.Equal(t, "storefront", backend.NetworkService.Name)
	assert.Equal(t, "http", backend.NetworkService.Port)
	assert.Empty(t, backend.Endpoint)
	assert.Nil(t, backend.TLS)
	assert.Equal(t, "storefront:http", OriginSummary(proxy))
}

func TestBuildHTTPProxyRequiresBackend(t *testing.T) {
	t.Parallel()
	_, err := BuildHTTPProxy(CreateInput{Name: "my-app"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one backend is required")
}

func TestBuildHTTPProxyRejectsPath(t *testing.T) {
	t.Parallel()
	_, err := BuildHTTPProxy(CreateInput{
		Name:     "my-app",
		Backends: []BackendInput{urlBackend("https://origin.example.com/api")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestDisplayNameFallsBackToPortalAnnotation(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))
	assert.Equal(t, "my-app", DisplayName(proxy))

	proxy.Annotations = map[string]string{display.AnnotationChosenName: "Portal Name"}
	assert.Equal(t, "Portal Name", DisplayName(proxy))

	name := "CLI Name"
	updated, err := ApplyHTTPProxyUpdate(proxy, UpdateInput{DisplayName: &name})
	require.NoError(t, err)
	assert.Equal(t, "CLI Name", DisplayName(updated))
	assert.Equal(t, "Portal Name", updated.Annotations[display.AnnotationChosenName])

	long := strings.Repeat("x", MaxDisplayNameLength+1)
	_, err = ApplyHTTPProxyUpdate(proxy, UpdateInput{DisplayName: &long})
	require.Error(t, err)
}

func TestApplyHTTPProxyUpdateLeavesConnectorAndRoutesAlone(t *testing.T) {
	t.Parallel()

	current := newProxy(t, urlBackend("https://origin.example.com"))
	current.Spec.Rules[1].Backends[0].Connector = &networkingv1alpha.ConnectorReference{Name: "edge"}
	current, err := SetRequestHeader(current, "X-Debug", "1")
	require.NoError(t, err)
	current, err = AddRoute(current, "/api", []BackendInput{urlBackend("https://api.example.com")})
	require.NoError(t, err)

	name := "Production"
	updated, err := ApplyHTTPProxyUpdate(current, UpdateInput{DisplayName: &name})
	require.NoError(t, err)
	assert.Equal(t, "edge", ConnectorName(updated))
	assert.Equal(t, current.Spec.Rules, updated.Spec.Rules)

	off := false
	updated, err = ApplyHTTPProxyUpdate(current, UpdateInput{ForceHTTPS: &off})
	require.NoError(t, err)
	assert.False(t, ForceHTTPS(updated))
	assert.Len(t, UserRoutes(updated), 2)
	assert.Equal(t, "edge", ConnectorName(updated))

	on := true
	updated, err = ApplyHTTPProxyUpdate(updated, UpdateInput{ForceHTTPS: &on})
	require.NoError(t, err)
	assert.True(t, isForceHTTPSRedirectRule(updated.Spec.Rules[0]))
	assert.Len(t, updated.Spec.Rules, 3)
}

func TestAddRoute(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))

	proxy, err := AddRoute(proxy, "/api", []BackendInput{urlBackend("https://api.example.com")})
	require.NoError(t, err)
	routes := UserRoutes(proxy)
	require.Len(t, routes, 2)
	assert.Equal(t, "/", routes[0].Path)
	assert.Equal(t, "/api", routes[1].Path)
	assert.True(t, ForceHTTPS(proxy))
	assert.Equal(t, gatewayv1.PathMatchPathPrefix, *proxy.Spec.Rules[2].Matches[0].Path.Type)

	_, err = AddRoute(proxy, "/api/", []BackendInput{urlBackend("https://api.example.com")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	_, err = AddRoute(proxy, "", []BackendInput{urlBackend("https://api.example.com")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--path is required")

	_, err = AddRoute(proxy, "api", []BackendInput{urlBackend("https://api.example.com")})
	require.Error(t, err)

	_, err = AddRoute(proxy, "/v2", nil)
	require.Error(t, err)
}

func TestAddRouteDefaultsToRootWhenMissing(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))
	proxy, err := RemoveRoute(proxy, "/", false)
	require.NoError(t, err)
	assert.Empty(t, UserRoutes(proxy))

	proxy, err = AddRoute(proxy, "", []BackendInput{
		storefrontBackend(),
		urlBackend("https://fallback.example.com"),
	})
	require.NoError(t, err)
	routes := UserRoutes(proxy)
	require.Len(t, routes, 1)
	assert.Equal(t, "/", routes[0].Path)
	require.Len(t, routes[0].Backends, 2)
	assert.Equal(t, "storefront:http", FormatBackend(routes[0].Backends[0]))
	assert.Equal(t, "storefront:http +1", OriginSummary(proxy))
}

func TestRemoveRoute(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))
	proxy, err := AddRoute(proxy, "/api", []BackendInput{urlBackend("https://api.example.com")})
	require.NoError(t, err)

	_, err = RemoveRoute(proxy, "/", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default route")
	assert.Equal(t, util.ExitUsage, exitCode(t, err))

	forced, err := RemoveRoute(proxy, "/", true)
	require.NoError(t, err)
	assert.Len(t, UserRoutes(forced), 1)

	_, err = RemoveRoute(proxy, "/missing", false)
	require.Error(t, err)
	assert.Equal(t, util.ExitNotFound, exitCode(t, err))

	proxy, err = RemoveRoute(proxy, "/api", false)
	require.NoError(t, err)
	assert.Len(t, UserRoutes(proxy), 1)
	assert.True(t, ForceHTTPS(proxy))

	onlyRedirect, err := RemoveRoute(proxy, "/", false)
	require.NoError(t, err)
	assert.Empty(t, UserRoutes(onlyRedirect))
	_, err = RemoveRoute(onlyRedirect, "/", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Force HTTPS redirect")
}

func TestRemoveRouteRefusesLastRule(t *testing.T) {
	t.Parallel()

	proxy, err := BuildHTTPProxy(CreateInput{Name: "my-app", Backends: []BackendInput{urlBackend("https://origin.example.com")}})
	require.NoError(t, err)
	require.Len(t, proxy.Spec.Rules, 1)

	_, err = RemoveRoute(proxy, "/", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last route")
	assert.Equal(t, util.ExitUsage, exitCode(t, err))
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"/":       "/",
		"/api":    "/api",
		"/api/":   "/api",
		" /api/ ": "/api",
	} {
		got, err := NormalizePath(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "api", "//", "/a//b", "/a b", "/a?x=1", "/a#b"} {
		_, err := NormalizePath(bad)
		require.Error(t, err, bad)
		assert.Equal(t, util.ExitUsage, exitCode(t, err), bad)
	}
}

func TestAdvancedRulesAreNotAddressable(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))
	exact := networkingv1alpha.HTTPProxyRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchExact), Value: ptr.To("/exact")},
		}},
		Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "https://exact.example.com"}},
	}
	headerOnly := networkingv1alpha.HTTPProxyRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Headers: []gatewayv1.HTTPHeaderMatch{{Name: "x-tenant", Value: "a"}},
		}},
		Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "https://tenant-a.example.com"}},
	}
	multi := networkingv1alpha.HTTPProxyRule{
		Matches: []gatewayv1.HTTPRouteMatch{
			{Path: &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchPathPrefix), Value: ptr.To("/a")}},
			{Path: &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchPathPrefix), Value: ptr.To("/b")}},
		},
		Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "https://ab.example.com"}},
	}
	proxy.Spec.Rules = append(proxy.Spec.Rules, exact, headerOnly, multi)

	routes := UserRoutes(proxy)
	require.Len(t, routes, 4)
	assert.False(t, routes[0].Advanced)
	for _, r := range routes[1:] {
		assert.True(t, r.Advanced, r.Path)
	}
	assert.Equal(t, "exact /exact", routes[1].Path)
	assert.Equal(t, "/ (+conditions)", routes[2].Path)
	assert.Equal(t, "/a | /b", routes[3].Path)

	updated, err := ReplaceRouteBackends(proxy, "/", []BackendInput{storefrontBackend()})
	require.NoError(t, err)
	assert.Equal(t, "https://tenant-a.example.com", updated.Spec.Rules[3].Backends[0].Endpoint)
	assert.Equal(t, "storefront:http", OriginSummary(updated))

	_, err = RemoveRoute(proxy, "/exact", false)
	require.Error(t, err)
	assert.Equal(t, util.ExitNotFound, exitCode(t, err))
}

func TestUserRedirectRuleIsNotForceHTTPS(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))
	legacy := networkingv1alpha.HTTPProxyRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchPathPrefix), Value: ptr.To("/legacy")},
		}},
		Filters: []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
				Scheme:   ptr.To("https"),
				Hostname: ptr.To(gatewayv1.PreciseHostname("new.example.com")),
			},
		}},
	}
	proxy.Spec.Rules = append(proxy.Spec.Rules, legacy)

	assert.True(t, ForceHTTPS(proxy))
	off := SetForceHTTPS(proxy, false)
	assert.False(t, ForceHTTPS(off))
	require.Len(t, off.Spec.Rules, 2)
	assert.Equal(t, "/legacy", *off.Spec.Rules[1].Matches[0].Path.Value)

	routes := UserRoutes(proxy)
	require.Len(t, routes, 2)
	assert.Equal(t, "/legacy", routes[1].Path)
	assert.Empty(t, routes[1].Backends)
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var cliErr *util.CLIError
	require.ErrorAs(t, err, &cliErr)
	return cliErr.Code()
}

func TestReplaceRouteBackendsLeavesSiblingsAlone(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://origin.example.com"))
	proxy, err := SetRequestHeader(proxy, "Host", "origin.internal")
	require.NoError(t, err)
	proxy, err = AddRoute(proxy, "/api", []BackendInput{urlBackend("https://api.example.com")})
	require.NoError(t, err)

	proxy, err = ReplaceRouteBackends(proxy, "/api", []BackendInput{urlBackend("https://api-new.example.com")})
	require.NoError(t, err)
	routes := UserRoutes(proxy)
	assert.Equal(t, "https://origin.example.com", FormatBackend(routes[0].Backends[0]))
	assert.Equal(t, "https://api-new.example.com", FormatBackend(routes[1].Backends[0]))
	assert.Equal(t, "origin.internal", HostHeader(proxy))

	proxy, err = ReplaceRouteBackends(proxy, "/", []BackendInput{storefrontBackend()})
	require.NoError(t, err)
	assert.Equal(t, "storefront:http", OriginSummary(proxy))
	assert.Equal(t, "origin.internal", HostHeader(proxy))

	_, err = ReplaceRouteBackends(proxy, "/api", nil)
	require.Error(t, err)
}

func TestRouteBackendAddRemove(t *testing.T) {
	t.Parallel()

	proxy := newProxy(t, urlBackend("https://a.example.com"))

	proxy, err := AddRouteBackend(proxy, "/", urlBackend("https://b.example.com"))
	require.NoError(t, err)
	proxy, err = AddRouteBackend(proxy, "/", storefrontBackend())
	require.NoError(t, err)
	require.Len(t, UserRoutes(proxy)[0].Backends, 3)
	assert.True(t, ForceHTTPS(proxy))
	assert.Equal(t, "https://a.example.com +2", OriginSummary(proxy))

	_, err = AddRouteBackend(proxy, "/", urlBackend("https://b.example.com/"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already on route")

	_, err = AddRouteBackend(proxy, "/api", urlBackend("https://c.example.com"))
	require.Error(t, err)

	proxy, err = RemoveRouteBackend(proxy, "/", urlBackend("https://a.example.com"))
	require.NoError(t, err)
	proxy, err = RemoveRouteBackend(proxy, "/", storefrontBackend())
	require.NoError(t, err)
	require.Len(t, UserRoutes(proxy)[0].Backends, 1)

	_, err = RemoveRouteBackend(proxy, "/", urlBackend("https://a.example.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not on route")

	_, err = RemoveRouteBackend(proxy, "/", urlBackend("https://b.example.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last backend")
}

func TestParseBackendFlags(t *testing.T) {
	t.Parallel()

	backends, err := ParseBackendFlags(BackendFlags{
		Endpoints:       []string{"https://a.example.com", "https://203.0.113.10"},
		NetworkServices: []string{"storefront"},
		Ports:           []string{"http"},
		TLSHostname:     "origin.example.com",
	})
	require.NoError(t, err)
	require.Len(t, backends, 3)
	assert.Equal(t, "origin.example.com", backends[0].TLSHostname)
	assert.Equal(t, "storefront", backends[2].NetworkService)
	assert.Empty(t, backends[2].TLSHostname)

	_, err = ParseBackendFlags(BackendFlags{NetworkServices: []string{"storefront"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matching --port")

	_, err = ParseBackendFlags(BackendFlags{Ports: []string{"http"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--port only applies")

	_, err = ParseBackendFlags(BackendFlags{Endpoints: []string{"https://a.example.com", "https://a.example.com/"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than once")

	_, err = ParseBackendFlags(BackendFlags{Endpoints: []string{"origin.example.com"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no scheme")

	_, err = ParseBackendFlags(BackendFlags{NetworkServices: []string{"storefront"}, Ports: []string{"http"}, TLSHostname: "x"})
	require.Error(t, err)

	_, err = ParseBackendFlags(BackendFlags{})
	require.Error(t, err)
}

func TestHostnameAddRemove(t *testing.T) {
	t.Parallel()

	proxy, err := BuildHTTPProxy(CreateInput{
		Name:     "my-app",
		Backends: []BackendInput{urlBackend("https://origin.example.com")},
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
		Backends: []BackendInput{urlBackend("https://origin.example.com")},
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
