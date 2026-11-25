package mutate

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	jwtauthnv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/jwt_authn/v3"
	oauth2v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/oauth2/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	tenantJWKSCluster    = "127-0-0-1_nip_io_19000"
	tenantTokenCluster   = "login_example_com_443"
	platformJWKSCluster  = "auth_platform_example_443"
	tenantExtAuthCluster = "securitypolicy/" + testDSNS + "/sp/extauth/0"
)

func signInFixture(t *testing.T) ([]*listenerv3.Listener, []*routev3.RouteConfiguration) {
	t.Helper()
	jwks := func(cluster string) *jwtauthnv3.JwtProvider {
		return &jwtauthnv3.JwtProvider{JwksSourceSpecifier: &jwtauthnv3.JwtProvider_RemoteJwks{RemoteJwks: &jwtauthnv3.RemoteJwks{
			HttpUri: &corev3.HttpUri{Uri: "https://" + cluster + "/keys", HttpUpstreamType: &corev3.HttpUri_Cluster{Cluster: cluster}},
		}}}
	}
	jwtCfg, err := anypb.New(&jwtauthnv3.JwtAuthentication{
		Providers: map[string]*jwtauthnv3.JwtProvider{
			"tenant-a":   jwks(tenantJWKSCluster),
			"tenant-b":   jwks("unused_by_route_443"),
			"platform-a": jwks(platformJWKSCluster),
		},
		RequirementMap: map[string]*jwtauthnv3.JwtRequirement{
			"tenant-req": {RequiresType: &jwtauthnv3.JwtRequirement_RequiresAny{RequiresAny: &jwtauthnv3.JwtRequirementOrList{
				Requirements: []*jwtauthnv3.JwtRequirement{
					{RequiresType: &jwtauthnv3.JwtRequirement_ProviderName{ProviderName: "tenant-a"}},
					{RequiresType: &jwtauthnv3.JwtRequirement_AllowMissing{}},
				},
			}}},
			"platform-req": {RequiresType: &jwtauthnv3.JwtRequirement_ProviderName{ProviderName: "platform-a"}},
		},
	})
	require.NoError(t, err)
	hcm, err := anypb.New(&hcmv3.HttpConnectionManager{
		StatPrefix:     "https",
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{Rds: &hcmv3.Rds{RouteConfigName: "https"}},
		HttpFilters: []*hcmv3.HttpFilter{
			{Name: jwtAuthnFilterName, ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: jwtCfg}},
		},
	})
	require.NoError(t, err)
	listeners := []*listenerv3.Listener{{
		Name: "default/gw/https",
		FilterChains: []*listenerv3.FilterChain{{Filters: []*listenerv3.Filter{{
			Name: hcmFilterName, ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcm},
		}}}},
	}}

	jwtRoute := func(req string) *anypb.Any {
		a, err := anypb.New(&jwtauthnv3.PerRouteConfig{RequirementSpecifier: &jwtauthnv3.PerRouteConfig_RequirementName{RequirementName: req}})
		require.NoError(t, err)
		return a
	}
	oauth, err := anypb.New(&oauth2v3.OAuth2PerRoute{Config: &oauth2v3.OAuth2Config{
		TokenEndpoint: &corev3.HttpUri{Uri: "https://login.example.com/token", HttpUpstreamType: &corev3.HttpUri_Cluster{Cluster: tenantTokenCluster}},
	}})
	require.NoError(t, err)

	routes := []*routev3.RouteConfiguration{{
		Name: "https",
		VirtualHosts: []*routev3.VirtualHost{{
			Name: "vh",
			Routes: []*routev3.Route{
				{
					Name:                 "httproute/" + testDSNS + "/api/rule/0/match/0/api_example_com",
					TypedPerFilterConfig: map[string]*anypb.Any{jwtAuthnFilterName: jwtRoute("tenant-req"), oauth2FilterName: oauth},
				},
				{
					Name:                 "httproute/datum-downstream-gateway/canary/rule/0/match/0/canary",
					TypedPerFilterConfig: map[string]*anypb.Any{jwtAuthnFilterName: jwtRoute("platform-req")},
				},
			},
		}},
	}}
	return listeners, routes
}

func signInClusters() []*clusterv3.Cluster {
	strict := func(name string) *clusterv3.Cluster {
		return &clusterv3.Cluster{Name: name, ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STRICT_DNS}}
	}
	return []*clusterv3.Cluster{
		guardDNSCluster(tenantJWKSCluster),
		guardDNSCluster(tenantTokenCluster),
		guardDNSCluster(tenantExtAuthCluster),
		guardDNSCluster(platformJWKSCluster),
		guardDNSCluster("unused_by_route_443"),
		guardDNSCluster("securitypolicy/datum-downstream-gateway/sp/extauth/0"),
		strict("xds_cluster"),
		strict("ratelimit_cluster"),
		strict("otel_metric_sink_0"),
		strict("accesslog_als_0"),
	}
}

func TestApplySignInUpstreamGuard_SelectsTenantSignInClusters(t *testing.T) {
	listeners, routes := signInFixture(t)
	clusters := signInClusters()

	gotListeners, gotClusters, n, err := ApplySignInUpstreamGuard(listeners, routes, clusters, guardTestIndex())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Len(t, gotListeners, 4)
	assert.Len(t, gotClusters, len(clusters)+3)

	byName := map[string]*clusterv3.Cluster{}
	for _, c := range gotClusters {
		byName[c.GetName()] = c
	}
	listenerNames := map[string]bool{}
	for _, l := range gotListeners {
		listenerNames[l.GetName()] = true
	}

	tests := []struct {
		cluster string
		guarded bool
	}{
		{tenantJWKSCluster, true},
		{tenantTokenCluster, true},
		{tenantExtAuthCluster, true},
		{platformJWKSCluster, false},
		{"unused_by_route_443", false},
		{"securitypolicy/datum-downstream-gateway/sp/extauth/0", false},
		{"xds_cluster", false},
		{"ratelimit_cluster", false},
		{"otel_metric_sink_0", false},
		{"accesslog_als_0", false},
	}
	for _, tc := range tests {
		t.Run(tc.cluster, func(t *testing.T) {
			outer := byName[tc.cluster]
			require.NotNil(t, outer)
			inner, hasInner := byName[tc.cluster+signInGuardClusterSuffix]
			assert.Equal(t, tc.guarded, hasInner)
			assert.Equal(t, tc.guarded, listenerNames[SignInGuardListenerPrefix+tc.cluster])
			if !tc.guarded {
				assert.Nil(t, guardFilterNames(t, outer))
				assert.True(t, resolvesNames(outer))
				return
			}
			addr := outer.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint().GetAddress()
			assert.Equal(t, SignInGuardListenerPrefix+tc.cluster, addr.GetEnvoyInternalAddress().GetServerListenerName())
			assert.Equal(t, clusterv3.Cluster_STATIC, outer.GetType())
			assert.Equal(t, []string{UpstreamAddressGuardFilterName, upstreamCodecFilterName}, guardFilterNames(t, inner))
			assert.Equal(t, "envoy.cluster.dns", inner.GetClusterType().GetName())
		})
	}
}

func TestApplySignInUpstreamGuard_HairpinRoutesToGuardedCluster(t *testing.T) {
	listeners, routes := signInFixture(t)
	gotListeners, _, _, err := ApplySignInUpstreamGuard(listeners, routes, signInClusters(), guardTestIndex())
	require.NoError(t, err)

	var hairpin *listenerv3.Listener
	for _, l := range gotListeners {
		if l.GetName() == SignInGuardListenerPrefix+tenantJWKSCluster {
			hairpin = l
		}
	}
	require.NotNil(t, hairpin)
	require.NotNil(t, hairpin.GetInternalListener())
	hcm := &hcmv3.HttpConnectionManager{}
	require.NoError(t, hairpin.GetFilterChains()[0].GetFilters()[0].GetTypedConfig().UnmarshalTo(hcm))
	route := hcm.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()[0]
	assert.Equal(t, tenantJWKSCluster+signInGuardClusterSuffix, route.GetRoute().GetCluster())
	require.NoError(t, hcm.ValidateAll())
}

func TestApplySignInUpstreamGuard_Idempotent(t *testing.T) {
	listeners, routes := signInFixture(t)
	l1, c1, n1, err := ApplySignInUpstreamGuard(listeners, routes, signInClusters(), guardTestIndex())
	require.NoError(t, err)
	l2, c2, n2, err := ApplySignInUpstreamGuard(l1, routes, c1, guardTestIndex())
	require.NoError(t, err)
	assert.Equal(t, 3, n1)
	assert.Zero(t, n2)
	assert.Len(t, l2, len(l1))
	assert.Len(t, c2, len(c1))
}

func TestApplySignInUpstreamGuard_NoSignInConfig(t *testing.T) {
	clusters := []*clusterv3.Cluster{guardDNSCluster("xds_cluster"), guardDNSCluster(tenantJWKSCluster)}
	l, c, n, err := ApplySignInUpstreamGuard(nil, nil, clusters, guardTestIndex())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, l)
	assert.Len(t, c, 2)
}
