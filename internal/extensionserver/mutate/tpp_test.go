package mutate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// testCorazaConfig returns a CorazaConfig with reasonable test defaults.
func testCorazaConfig() *CorazaConfig {
	return &CorazaConfig{
		FilterName:  "coraza-waf",
		LibraryID:   "coraza-waf",
		LibraryPath: "/opt/coraza-waf/coraza-waf.so",
		PluginName:  "coraza-waf",
		ListenerDirectives: []string{
			"SecRuleEngine DetectionOnly",
			"Include @owasp_crs/*.conf",
		},
	}
}

// listenerWithHCM builds a minimal Listener containing an RDS-based HCM filter,
// simulating a user-traffic listener. The RDS route_specifier is required for
// InjectCorazaListenerFilters to fire — EG's internal listeners use inline
// static route_config instead and must be skipped.
func listenerWithHCM(t *testing.T, chainName string) *listenerv3.Listener {
	t.Helper()
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: "test",
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{
			Rds: &hcmv3.Rds{RouteConfigName: "test-route-config"},
		},
		HttpFilters: []*hcmv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
		},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err, "marshal HCM")
	return &listenerv3.Listener{
		Name: "test-listener",
		FilterChains: []*listenerv3.FilterChain{
			{
				Name: chainName,
				Filters: []*listenerv3.Filter{
					{
						Name:       hcmFilterName,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
					},
				},
			},
		},
	}
}

// buildEGMetadataStruct constructs the filter_metadata struct that EG populates
// on VirtualHosts (Kind=Gateway) and Routes (Kind=HTTPRoute). Format per
// design plan §2.1 metadata contract.
func buildEGMetadataStruct(kind, namespace, name string) *structpb.Struct {
	s, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"kind":      kind,
				"namespace": namespace,
				"name":      name,
			},
		},
	})
	if err != nil {
		panic("buildEGMetadataStruct: " + err.Error())
	}
	return s
}

// buildVHWithGatewayMeta builds a VirtualHost with envoy-gateway filter_metadata
// carrying a Gateway resource reference. Used to simulate EG-populated metadata.
// All contextual values are fixed: name="vh", dsNS="ns-abc-123", gwName="smoke-gw"
// — all callers in this package use those values.
func buildVHWithGatewayMeta(routes ...*routev3.Route) *routev3.VirtualHost {
	return &routev3.VirtualHost{
		Name: "vh",
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				envoyGatewayMetadataKey: buildEGMetadataStruct("Gateway", "ns-abc-123", "smoke-gw"),
			},
		},
		Routes: routes,
	}
}

// tppTargetingGateway returns a TPPInfo that targets the named Gateway.
// The upstream namespace is fixed as "test-project" — all callers in this package use that value.
func tppTargetingGateway(tppName, gwName string) extcache.TPPInfo {
	return extcache.TPPInfo{
		Namespace: "test-project",
		Name:      tppName,
		Mode:      networkingv1alpha.TrafficProtectionPolicyObserve,
		TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
			{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: gatewayv1.ObjectName(gwName),
				},
			},
		},
		// Non-empty Directives are required for applyRouteWAFConfig to fire.
		Directives: []string{"SecRuleEngine DetectionOnly", "Include @owasp_crs/*.conf"},
	}
}

// tppTargetingHTTPRoute returns a TPPInfo that targets the named HTTPRoute.
func tppTargetingHTTPRoute(upstreamNS, tppName, routeName string) extcache.TPPInfo {
	return extcache.TPPInfo{
		Namespace: upstreamNS,
		Name:      tppName,
		Mode:      networkingv1alpha.TrafficProtectionPolicyEnforce,
		TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
			{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "HTTPRoute",
					Name: gatewayv1.ObjectName(routeName),
				},
			},
		},
		Directives: []string{"SecRuleEngine On", "Include @owasp_crs/*.conf"},
	}
}

// policyIndex builds a minimal PolicyIndex for TPP tests.
// Both the downstream namespace ("ns-abc-123") and upstream namespace
// ("test-project") are fixed — all callers in this package use those values.
func policyIndex(tpps ...extcache.TPPInfo) *extcache.PolicyIndex {
	return &extcache.PolicyIndex{
		DStoUS:       map[string]string{"ns-abc-123": "test-project"},
		ProjectNames: map[string]string{"ns-abc-123": "test-project"},
		TPPs:         map[string][]extcache.TPPInfo{"test-project": tpps},
		Connectors:   make(map[extcache.ConnectorKey]extcache.ConnectorInfo),
	}
}

// --- InjectCorazaListenerFilters tests ---

func TestInjectCorazaListenerFilters_InjectsAtPositionZeroDisabled(t *testing.T) {
	cfg := testCorazaConfig()
	l := listenerWithHCM(t, "consumer-gw/smoke-gw/https")

	n, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "expected 1 HCM mutated")

	// Unmarshal and inspect the HCM.
	f := l.FilterChains[0].Filters[0]
	hcm := &hcmv3.HttpConnectionManager{}
	require.NoError(t, f.GetTypedConfig().UnmarshalTo(hcm))

	require.Len(t, hcm.HttpFilters, 2, "want 2 http filters after injection")

	// Coraza filter must be at position 0.
	first := hcm.HttpFilters[0]
	assert.Equal(t, cfg.FilterName, first.Name, "coraza filter must be first")
	assert.True(t, first.Disabled, "coraza filter must be disabled at listener scope")

	// TypedConfig must carry the golang.v3alpha.Config type URL.
	tc := first.GetTypedConfig()
	require.NotNil(t, tc, "coraza filter must have typed_config")
	assert.Contains(t, tc.GetTypeUrl(), "golang.v3alpha.Config",
		"typed_config type url should reference golang v3alpha Config")

	// Router filter must remain last.
	assert.Equal(t, "envoy.filters.http.router", hcm.HttpFilters[1].Name,
		"router filter must stay last")
}

func TestInjectCorazaListenerFilters_Idempotent(t *testing.T) {
	cfg := testCorazaConfig()
	l := listenerWithHCM(t, "consumer-gw/smoke-gw/https")

	n1, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	require.Equal(t, 1, n1, "first injection should mutate 1 HCM")

	// Second injection must be a no-op.
	n2, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n2, "re-injection must be a no-op")

	// Filter count must remain 2 (coraza + router).
	hcm := &hcmv3.HttpConnectionManager{}
	require.NoError(t, l.FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(hcm))
	assert.Len(t, hcm.HttpFilters, 2, "filter count must not grow on re-injection")
}

func TestInjectCorazaListenerFilters_DefaultFilterChain(t *testing.T) {
	cfg := testCorazaConfig()

	// Use RDS-based HCM to simulate a real user-traffic listener that happens
	// to use a DefaultFilterChain instead of named chains.
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: "default",
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{
			Rds: &hcmv3.Rds{RouteConfigName: "default-route-config"},
		},
		HttpFilters: []*hcmv3.HttpFilter{{Name: "envoy.filters.http.router"}},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)

	// Listener with ONLY a DefaultFilterChain (no regular filter chains).
	l := &listenerv3.Listener{
		Name: "test-listener",
		DefaultFilterChain: &listenerv3.FilterChain{
			Name: "default",
			Filters: []*listenerv3.Filter{
				{
					Name:       hcmFilterName,
					ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
				},
			},
		},
	}

	n, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "default filter chain HCM should be mutated")

	injectedHCM := &hcmv3.HttpConnectionManager{}
	require.NoError(t, l.DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(injectedHCM))
	assert.Equal(t, cfg.FilterName, injectedHCM.HttpFilters[0].Name,
		"coraza filter must be injected into default filter chain")
}

func TestInjectCorazaListenerFilters_NoHCM_NoOp(t *testing.T) {
	cfg := testCorazaConfig()
	// Listener with a non-HCM filter.
	l := &listenerv3.Listener{
		Name: "test-listener",
		FilterChains: []*listenerv3.FilterChain{
			{
				Name: "tcp",
				Filters: []*listenerv3.Filter{
					{Name: "envoy.filters.network.tcp_proxy"},
				},
			},
		},
	}

	n, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "non-HCM listener must not be mutated")
}

// --- ApplyTPPRouteConfig tests ---

func TestApplyTPPRouteConfig_GoverningGatewayTPP_AnnotatesRoutes(t *testing.T) {
	cfg := testCorazaConfig()
	const (
		dsNS       = "ns-abc-123"
		upstreamNS = "test-project"
		gwName     = "smoke-gw"
	)
	idx := policyIndex(tppTargetingGateway("test-tpp", gwName))

	vh := buildVHWithGatewayMeta(
		&routev3.Route{Name: "r0"},
		&routev3.Route{Name: "r1"},
	)
	rc := &routev3.RouteConfiguration{
		Name:         "http-80",
		VirtualHosts: []*routev3.VirtualHost{vh},
	}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "both routes should be mutated")

	for _, rt := range vh.Routes {
		// datum-gateway filter_metadata must be present.
		md := rt.GetMetadata().GetFilterMetadata()[datumGatewayMetadataKey]
		require.NotNilf(t, md, "route %q missing datum-gateway metadata", rt.Name)

		res := md.GetFields()["resources"].GetListValue().GetValues()
		require.Lenf(t, res, 1, "route %q: want 1 resource entry", rt.Name)

		entry := res[0].GetStructValue().GetFields()
		assert.Equal(t, "TrafficProtectionPolicy", entry["kind"].GetStringValue())
		assert.Equal(t, "test-tpp", entry["name"].GetStringValue())
		assert.Equal(t, upstreamNS, entry["namespace"].GetStringValue())
		assert.Equal(t, string(networkingv1alpha.TrafficProtectionPolicyObserve), entry["mode"].GetStringValue())

		// Coraza typed_per_filter_config must be present.
		tpfc := rt.GetTypedPerFilterConfig()[cfg.FilterName]
		require.NotNilf(t, tpfc, "route %q missing coraza typed_per_filter_config", rt.Name)
		assert.Truef(t,
			strings.Contains(tpfc.GetTypeUrl(), "golang.v3alpha.ConfigsPerRoute"),
			"route %q tpfc type url = %q", rt.Name, tpfc.GetTypeUrl())
	}
}

func TestApplyTPPRouteConfig_NoEGMetadata_Skipped(t *testing.T) {
	cfg := testCorazaConfig()
	idx := policyIndex(
		tppTargetingGateway("test-tpp", "smoke-gw"),
	)

	// VH has no EG metadata at all.
	rc := &routev3.RouteConfiguration{
		Name: "http-80",
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:   "bare-vhost",
				Routes: []*routev3.Route{{Name: "r0"}},
			},
		},
	}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "VH without EG metadata must be skipped")

	// Route must be untouched.
	rt := rc.VirtualHosts[0].Routes[0]
	assert.Nil(t, rt.GetMetadata().GetFilterMetadata()[datumGatewayMetadataKey],
		"route must not have datum-gateway metadata when VH lacks EG metadata")
	assert.Nil(t, rt.GetTypedPerFilterConfig(),
		"route must not have typed_per_filter_config when VH lacks EG metadata")
}

func TestApplyTPPRouteConfig_UnknownDSNamespace_Skipped(t *testing.T) {
	cfg := testCorazaConfig()
	// idx does NOT contain a mapping for dsNS.
	idx := &extcache.PolicyIndex{
		DStoUS:       map[string]string{"ns-other": "other-project"},
		ProjectNames: make(map[string]string),
		TPPs:         make(map[string][]extcache.TPPInfo),
		Connectors:   make(map[extcache.ConnectorKey]extcache.ConnectorInfo),
	}

	vh := buildVHWithGatewayMeta(
		&routev3.Route{Name: "r0"},
	)
	rc := &routev3.RouteConfiguration{VirtualHosts: []*routev3.VirtualHost{vh}}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "VH with unknown dsNS must be skipped")
}

func TestApplyTPPRouteConfig_NoGoverningTPP_RoutesUntouched(t *testing.T) {
	cfg := testCorazaConfig()
	// TPP targets a DIFFERENT gateway — smoke-gw is ungoverned.
	idx := policyIndex(
		tppTargetingGateway("test-tpp", "other-gw"),
	)

	vh := buildVHWithGatewayMeta(
		&routev3.Route{Name: "r0"},
	)
	rc := &routev3.RouteConfiguration{VirtualHosts: []*routev3.VirtualHost{vh}}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "route with no governing TPP must be untouched")
	assert.Nil(t, rc.VirtualHosts[0].Routes[0].GetTypedPerFilterConfig(),
		"ungoverned route must not have typed_per_filter_config")
}

func TestApplyTPPRouteConfig_EmptyDirectives_RoutesUntouched(t *testing.T) {
	cfg := testCorazaConfig()
	const (
		dsNS       = "ns-abc-123"
		upstreamNS = "test-project"
		gwName     = "smoke-gw"
	)
	// TPP targets the correct gateway but has NO directives (e.g. no OWASP CRS).
	tppNoDirectives := extcache.TPPInfo{
		Namespace: upstreamNS,
		Name:      "empty-tpp",
		Mode:      networkingv1alpha.TrafficProtectionPolicyObserve,
		TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
			{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: gatewayv1.ObjectName(gwName),
				},
			},
		},
		Directives: nil, // empty — should NOT fire
	}
	idx := policyIndex(tppNoDirectives)

	vh := buildVHWithGatewayMeta(
		&routev3.Route{Name: "r0"},
	)
	rc := &routev3.RouteConfiguration{VirtualHosts: []*routev3.VirtualHost{vh}}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "TPP with no directives must not annotate routes")
}

// --- Bug-fix regression tests ---

// TestInjectCorazaListenerFilters_Disabled_NoOp verifies that setting
// CorazaConfig.Disabled=true causes InjectCorazaListenerFilters to return 0
// mutations and leave the HCM completely untouched, even when FilterName is set.
func TestInjectCorazaListenerFilters_Disabled_NoOp(t *testing.T) {
	cfg := testCorazaConfig()
	cfg.Disabled = true
	l := listenerWithHCM(t, "consumer-gw/smoke-gw/https")

	n, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "disabled Coraza must not inject any listener filters")

	// HCM must be completely untouched — only the router filter should remain.
	f := l.FilterChains[0].Filters[0]
	hcm := &hcmv3.HttpConnectionManager{}
	require.NoError(t, f.GetTypedConfig().UnmarshalTo(hcm))
	assert.Len(t, hcm.HttpFilters, 1, "HCM must have only the router filter when disabled")
	assert.Equal(t, "envoy.filters.http.router", hcm.HttpFilters[0].Name,
		"only the router filter must remain when disabled")
}

// TestInjectCorazaListenerFilters_StaticRouteConfig_Skipped verifies that an
// HCM with an inline static route_config (no RDS) is skipped by
// InjectCorazaListenerFilters. This covers EG's internal readiness listener
// (envoy-gateway-proxy-ready-0.0.0.0-19003), which uses a direct_response
// route inline — NOT RDS. Injecting the golang Coraza filter into that listener
// causes Envoy to reject all listeners on standard (non-contrib) builds.
func TestInjectCorazaListenerFilters_StaticRouteConfig_Skipped(t *testing.T) {
	cfg := testCorazaConfig()

	// Build an HCM with an inline static route_config — the same pattern
	// EG uses for its internal readiness/admin listeners.
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: "ready",
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{
			RouteConfig: &routev3.RouteConfiguration{
				Name: "local_route",
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{{Name: "envoy.filters.http.router"}},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)

	l := &listenerv3.Listener{
		Name: "envoy-gateway-proxy-ready-0.0.0.0-19003",
		FilterChains: []*listenerv3.FilterChain{
			{
				Name: "ready",
				Filters: []*listenerv3.Filter{
					{
						Name:       hcmFilterName,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
					},
				},
			},
		},
	}

	n, err := InjectCorazaListenerFilters(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n,
		"HCM with inline static route_config must be skipped (not RDS-based)")

	// HCM must be completely untouched.
	injectedHCM := &hcmv3.HttpConnectionManager{}
	require.NoError(t, l.FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(injectedHCM))
	assert.Len(t, injectedHCM.HttpFilters, 1,
		"static-route HCM must not receive the Coraza filter")
	assert.Equal(t, "envoy.filters.http.router", injectedHCM.HttpFilters[0].Name,
		"only the router filter must remain after skipping static-route HCM")
}

// TestApplyTPPRouteConfig_Disabled_NoOp verifies that setting
// CorazaConfig.Disabled=true skips WAF per-route config but still stamps
// project_name into the datum-gateway route metadata so the access log can
// emit it even on standard Envoy images (no golang filter).
func TestApplyTPPRouteConfig_Disabled_StampsProjectName(t *testing.T) {
	cfg := testCorazaConfig()
	cfg.Disabled = true

	const (
		dsNS       = "ns-abc-123"
		upstreamNS = "test-project"
		gwName     = "smoke-gw"
	)
	idx := policyIndex(tppTargetingGateway("test-tpp", gwName))

	vh := buildVHWithGatewayMeta(
		&routev3.Route{Name: "r0"},
	)
	rc := &routev3.RouteConfiguration{
		Name:         "http-80",
		VirtualHosts: []*routev3.VirtualHost{vh},
	}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "disabled Coraza must not count any WAF per-route mutations")

	rt := vh.Routes[0]
	assert.Nil(t, rt.GetTypedPerFilterConfig(),
		"route must have no typed_per_filter_config when Coraza is disabled")
	// project_name is stamped unconditionally so access logs always carry it.
	meta := rt.GetMetadata().GetFilterMetadata()[datumGatewayMetadataKey]
	require.NotNil(t, meta, "datum-gateway filter_metadata must be set even when Coraza is disabled")
	assert.Equal(t, upstreamNS, meta.GetFields()["project_name"].GetStringValue(),
		"project_name must be stamped on NSO-owned routes regardless of Coraza.Disabled")
}

func TestApplyTPPRouteConfig_RouteLevelTPPWins(t *testing.T) {
	cfg := testCorazaConfig()
	const (
		dsNS       = "ns-abc-123"
		upstreamNS = "test-project"
		gwName     = "smoke-gw"
		routeName  = "specific-route"
	)

	gwTPP := tppTargetingGateway("gw-tpp", gwName)
	routeTPP := tppTargetingHTTPRoute(upstreamNS, "route-tpp", routeName)
	// Route-level TPP is listed second; it should still win over gateway-level.
	idx := policyIndex(gwTPP, routeTPP)

	// Route has EG metadata carrying Kind=HTTPRoute with the governed route name.
	routeEGMeta, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"kind":      "HTTPRoute",
				"namespace": dsNS,
				"name":      routeName,
			},
		},
	})
	require.NoError(t, err)

	rt := &routev3.Route{
		Name: "r0",
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				envoyGatewayMetadataKey: routeEGMeta,
			},
		},
	}
	vh := buildVHWithGatewayMeta(rt)
	rc := &routev3.RouteConfiguration{VirtualHosts: []*routev3.VirtualHost{vh}}

	n, err := ApplyTPPRouteConfig(rc, idx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// datum-gateway metadata should record the route-level TPP, not the gw-level TPP.
	md := rt.GetMetadata().GetFilterMetadata()[datumGatewayMetadataKey]
	require.NotNil(t, md)
	res := md.GetFields()["resources"].GetListValue().GetValues()
	require.Len(t, res, 1)
	entry := res[0].GetStructValue().GetFields()
	assert.Equal(t, "route-tpp", entry["name"].GetStringValue(),
		"route-level TPP name must appear in datum-gateway metadata")
	// Route-level TPP mode is Enforce.
	assert.Equal(t, string(networkingv1alpha.TrafficProtectionPolicyEnforce), entry["mode"].GetStringValue())
}
