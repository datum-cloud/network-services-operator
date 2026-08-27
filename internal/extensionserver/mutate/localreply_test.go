package mutate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"

	"go.datum.net/network-services-operator/internal/extensionserver/assets"
)

const (
	testBodyHTML    = "<!DOCTYPE html><html><body>temporarily unavailable %RESPONSE_CODE%</body></html>"
	testContentType = "text/html; charset=UTF-8"
	testRuntimeKey  = "local_reply_5xx"
)

// testLocalReplyConfig returns a LocalReplyConfig with reasonable test defaults.
func testLocalReplyConfig() *LocalReplyConfig {
	return &LocalReplyConfig{
		MinStatusCode: 500,
		RuntimeKey:    testRuntimeKey,
		BodyHTML:      testBodyHTML,
		ContentType:   testContentType,
	}
}

// hcmFromFilter unmarshals the HCM typed_config from a network filter.
func hcmFromFilter(t *testing.T, f *listenerv3.Filter) *hcmv3.HttpConnectionManager {
	t.Helper()
	hcm := &hcmv3.HttpConnectionManager{}
	require.NoError(t, f.GetTypedConfig().UnmarshalTo(hcm))
	return hcm
}

// listenerWithStaticHCM builds a listener whose HCM uses an inline static
// route_config (no RDS), simulating EG's internal readiness listener. These
// must never be branded.
func listenerWithStaticHCM(t *testing.T) *listenerv3.Listener {
	t.Helper()
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: "ready",
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{
			RouteConfig: &routev3.RouteConfiguration{
				Name: "local_route",
				VirtualHosts: []*routev3.VirtualHost{
					{Name: "ready_route", Domains: []string{"*"}},
				},
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
		},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err, "marshal static HCM")
	return &listenerv3.Listener{
		Name: "envoy-gateway-proxy-ready",
		FilterChains: []*listenerv3.FilterChain{
			{
				Name: "ready-chain",
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

func TestInjectLocalReplyConfig_RDSHCM_Branded(t *testing.T) {
	cfg := testLocalReplyConfig()
	l := listenerWithHCM(t, "user-chain")

	n, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "expected 1 HCM branded")

	hcm := hcmFromFilter(t, l.FilterChains[0].Filters[0])
	lrc := hcm.GetLocalReplyConfig()
	require.NotNil(t, lrc, "HCM must have local_reply_config")
	require.Len(t, lrc.GetMappers(), 1, "want exactly one response mapper")

	mapper := lrc.GetMappers()[0]

	// Status-code filter: GE comparison against minStatusCode with runtime key.
	scf := mapper.GetFilter().GetStatusCodeFilter()
	require.NotNil(t, scf, "mapper must use a status_code_filter")
	cmp := scf.GetComparison()
	require.NotNil(t, cmp)
	assert.Equal(t, accesslogv3.ComparisonFilter_GE, cmp.GetOp(), "comparison must be >=")
	assert.Equal(t, uint32(500), cmp.GetValue().GetDefaultValue(), "threshold must be minStatusCode")
	assert.Equal(t, testRuntimeKey, cmp.GetValue().GetRuntimeKey(), "runtime key must be set")

	// Body + content-type override; status code preserved (StatusCode nil).
	assert.Nil(t, mapper.GetStatusCode(), "status code must be preserved (mapper StatusCode nil)")
	bfo := mapper.GetBodyFormatOverride()
	require.NotNil(t, bfo, "mapper must override body format")
	assert.Equal(t, testContentType, bfo.GetContentType())
	assert.Equal(t, testBodyHTML, bfo.GetTextFormatSource().GetInlineString(), "body must be the branded HTML")

	// Original routing untouched: HCM still RDS-based with the same route config.
	require.NotNil(t, hcm.GetRds(), "HCM must remain RDS-based")
	assert.Equal(t, "test-route-config", hcm.GetRds().GetRouteConfigName())
	require.Len(t, hcm.GetHttpFilters(), 1, "http_filters must be unchanged")
	assert.Equal(t, "envoy.filters.http.router", hcm.GetHttpFilters()[0].GetName())
}

func TestInjectLocalReplyConfig_StaticHCM_Skipped(t *testing.T) {
	cfg := testLocalReplyConfig()
	l := listenerWithStaticHCM(t)

	n, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "static (non-RDS) HCM must not be branded")

	hcm := hcmFromFilter(t, l.FilterChains[0].Filters[0])
	assert.Nil(t, hcm.GetLocalReplyConfig(), "readiness listener must not get a local_reply_config")
}

func TestInjectLocalReplyConfig_Idempotent(t *testing.T) {
	cfg := testLocalReplyConfig()
	l := listenerWithHCM(t, "user-chain")

	n1, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	require.Equal(t, 1, n1, "first pass should brand 1 HCM")

	n2, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n2, "second pass must be a no-op")

	hcm := hcmFromFilter(t, l.FilterChains[0].Filters[0])
	lrc := hcm.GetLocalReplyConfig()
	require.NotNil(t, lrc)
	assert.Len(t, lrc.GetMappers(), 1, "mappers must not grow on re-injection")
}

func TestInjectLocalReplyConfig_DefaultFilterChain(t *testing.T) {
	cfg := testLocalReplyConfig()

	// Build a listener whose HCM lives only in the default filter chain.
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: "test",
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{
			Rds: &hcmv3.Rds{RouteConfigName: "default-rc"},
		},
		HttpFilters: []*hcmv3.HttpFilter{{Name: "envoy.filters.http.router"}},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)
	l := &listenerv3.Listener{
		Name: "test-listener",
		DefaultFilterChain: &listenerv3.FilterChain{
			Name: "default-chain",
			Filters: []*listenerv3.Filter{
				{Name: hcmFilterName, ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny}},
			},
		},
	}

	n, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "default filter chain HCM should be branded")

	got := hcmFromFilter(t, l.DefaultFilterChain.Filters[0])
	assert.NotNil(t, got.GetLocalReplyConfig(), "default filter chain HCM must get local_reply_config")
}

func TestInjectLocalReplyConfig_DefaultsMinStatusCode(t *testing.T) {
	cfg := testLocalReplyConfig()
	cfg.MinStatusCode = 0 // should default to 500
	l := listenerWithHCM(t, "user-chain")

	n, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	hcm := hcmFromFilter(t, l.FilterChains[0].Filters[0])
	cmp := hcm.GetLocalReplyConfig().GetMappers()[0].GetFilter().GetStatusCodeFilter().GetComparison()
	assert.Equal(t, uint32(500), cmp.GetValue().GetDefaultValue(), "zero MinStatusCode must default to 500")
}

func TestInjectLocalReplyConfig_NoOp(t *testing.T) {
	tests := []struct {
		name string
		cfg  *LocalReplyConfig
	}{
		{
			name: "disabled",
			cfg: &LocalReplyConfig{
				Disabled: true,
				BodyHTML: testBodyHTML,
			},
		},
		{
			name: "empty body",
			cfg: &LocalReplyConfig{
				BodyHTML: "",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := listenerWithHCM(t, "user-chain")
			n, err := InjectLocalReplyConfig(l, tc.cfg)
			require.NoError(t, err)
			assert.Equal(t, 0, n, "must be a no-op")

			hcm := hcmFromFilter(t, l.FilterChains[0].Filters[0])
			assert.Nil(t, hcm.GetLocalReplyConfig(), "no local_reply_config must be set on no-op")
		})
	}
}

func TestEscapeEnvoyFormatLiterals(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no percent", "<html>plain</html>", "<html>plain</html>"},
		{"bare percent in css", "height: 100%;", "height: 100%%;"},
		{"multiple bare percents", "120% 50% 0%", "120%% 50%% 0%%"},
		{"preserve allowlisted command", "code %RESPONSE_CODE% end", "code %RESPONSE_CODE% end"},
		{"preserve allowlisted response code details", "details: %RESPONSE_CODE_DETAILS%", "details: %RESPONSE_CODE_DETAILS%"},
		{"preserve allowlisted start time", "at %START_TIME(%Y-%m-%d %H:%M:%S UTC)%", "at %START_TIME(%Y-%m-%d %H:%M:%S UTC)%"},
		{"escape non-allowlisted command", "%REQ(x-header)%", "%%REQ(x-header)%%"},
		{"escape non-allowlisted command with length", "%REQ(x-header):10%", "%%REQ(x-header):10%%"},
		{"escape command-shaped literal", "progress %COMPLETE% now", "progress %%COMPLETE%% now"},
		{"already escaped stays escaped", "width: 100%%;", "width: 100%%;"},
		{"mixed command and literal", "%RESPONSE_CODE% at 100%", "%RESPONSE_CODE% at 100%%"},
		{"trailing bare percent", "50%", "50%%"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeEnvoyFormatLiterals(tc.in)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, got, escapeEnvoyFormatLiterals(got), "must be idempotent")
		})
	}
}

// TestEmbeddedDefaultPageIsEnvoySafe is the regression guard for issue #243:
// the embedded default error page contains literal CSS percent signs
// (e.g. "height: 100%") that Envoy would misparse as command operators and
// reject the listener. After escaping, no bare '%' may remain and the intended
// %RESPONSE_CODE% operator must survive.
func TestEmbeddedDefaultPageIsEnvoySafe(t *testing.T) {
	raw := assets.DefaultError5xxHTML
	require.Contains(t, raw, "height: 100%;", "test premise: raw page has an unescaped percent")

	escaped := escapeEnvoyFormatLiterals(raw)

	assert.Contains(t, escaped, "height: 100%%;", "literal percent must be escaped")
	assert.Equal(t, 1, strings.Count(escaped, "%RESPONSE_CODE%"), "command operator must be preserved exactly once")
	assert.Equal(t, escaped, escapeEnvoyFormatLiterals(escaped), "escaping must be idempotent")

	// The escaped body must satisfy the same invariant the startup validator
	// asserts: no bare '%' Envoy could misparse as a command operator.
	assert.NoError(t, assertEnvoyFormatSafe(escaped), "no bare percent may remain after escaping")
}

// TestValidateLocalReplyConfig covers the startup guard (issue #243): the
// assembled config must validate for the embedded default, reject a body whose
// bare '%' would reach Envoy unescaped, and no-op when injection is disabled.
func TestValidateLocalReplyConfig(t *testing.T) {
	base := testLocalReplyConfig()

	t.Run("embedded default is valid", func(t *testing.T) {
		cfg := *base
		cfg.BodyHTML = assets.DefaultError5xxHTML
		assert.NoError(t, ValidateLocalReplyConfig(&cfg))
	})

	t.Run("escaped body is valid", func(t *testing.T) {
		cfg := *base
		cfg.BodyHTML = "height: 100%;"
		assert.NoError(t, ValidateLocalReplyConfig(&cfg))
	})

	t.Run("disabled is a no-op", func(t *testing.T) {
		cfg := *base
		cfg.Disabled = true
		cfg.BodyHTML = "height: 100%;"
		assert.NoError(t, ValidateLocalReplyConfig(&cfg))
	})

	t.Run("bare percent surviving into the body is rejected", func(t *testing.T) {
		// buildLocalReplyConfig escapes, so this asserts the raw invariant the
		// validator enforces on the injected body.
		assert.Error(t, assertEnvoyFormatSafe("height: 100%;"))
		assert.Error(t, assertEnvoyFormatSafe("%COMPLETE%"))
		assert.NoError(t, assertEnvoyFormatSafe("code %RESPONSE_CODE% ok"))
	})
}

const testOfflineBodyHTML = "<!DOCTYPE html><html><body>service offline %RESPONSE_CODE%</body></html>"

// offlineFlags returns the response flags the mapper at index i matches on, or
// nil when it does not filter on response flags at all.
func offlineFlags(mapper *hcmv3.ResponseMapper) []string {
	for _, f := range mapper.GetFilter().GetAndFilter().GetFilters() {
		if rff := f.GetResponseFlagFilter(); rff != nil {
			return rff.GetFlags()
		}
	}
	return nil
}

// TestInjectLocalReplyConfig_OfflineMapperFirst pins the ordering Envoy depends
// on: it takes the first matching mapper, so the no-healthy-upstream mapper has
// to precede the generic one or the generic page always wins.
func TestInjectLocalReplyConfig_OfflineMapperFirst(t *testing.T) {
	cfg := testLocalReplyConfig()
	cfg.OfflineBodyHTML = testOfflineBodyHTML
	l := listenerWithHCM(t, "user-chain")

	n, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	mappers := hcmFromFilter(t, l.FilterChains[0].Filters[0]).GetLocalReplyConfig().GetMappers()
	require.Len(t, mappers, 2, "want the offline mapper plus the generic fallback")

	offline := mappers[0]
	assert.Equal(t, []string{"UH"}, offlineFlags(offline), "first mapper must match no-healthy-upstream")
	assert.Equal(t, testOfflineBodyHTML, offline.GetBodyFormatOverride().GetTextFormatSource().GetInlineString())
	assert.Nil(t, offline.GetStatusCode(), "offline mapper must preserve the original status code")

	// The offline mapper is also gated on the status-code filter, so the runtime
	// key stays a single kill switch for every branded reply.
	var gated bool
	for _, f := range offline.GetFilter().GetAndFilter().GetFilters() {
		if scf := f.GetStatusCodeFilter(); scf != nil {
			gated = true
			assert.Equal(t, accesslogv3.ComparisonFilter_GE, scf.GetComparison().GetOp())
			assert.Equal(t, uint32(500), scf.GetComparison().GetValue().GetDefaultValue())
			assert.Equal(t, testRuntimeKey, scf.GetComparison().GetValue().GetRuntimeKey())
		}
	}
	assert.True(t, gated, "offline mapper must also carry the status-code filter")

	generic := mappers[1]
	assert.Nil(t, offlineFlags(generic), "generic mapper must not filter on response flags")
	require.NotNil(t, generic.GetFilter().GetStatusCodeFilter(), "generic mapper keeps the plain status-code filter")
	assert.Equal(t, testBodyHTML, generic.GetBodyFormatOverride().GetTextFormatSource().GetInlineString())
}

// TestInjectLocalReplyConfig_MissingOfflineBodyFallsThrough covers the safety
// property: with no offline page configured, no-healthy-upstream responses get
// the generic page. A missing body must never be an error — the downstream hook
// runs failOpen:false, so erroring here would stall xDS fleet-wide.
func TestInjectLocalReplyConfig_MissingOfflineBodyFallsThrough(t *testing.T) {
	cfg := testLocalReplyConfig()
	cfg.OfflineBodyHTML = ""
	l := listenerWithHCM(t, "user-chain")

	n, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err, "a missing offline body must never error")
	require.Equal(t, 1, n)

	mappers := hcmFromFilter(t, l.FilterChains[0].Filters[0]).GetLocalReplyConfig().GetMappers()
	require.Len(t, mappers, 1, "no offline body means no offline mapper")
	assert.Nil(t, offlineFlags(mappers[0]))
	body := mappers[0].GetBodyFormatOverride().GetTextFormatSource().GetInlineString()
	assert.Equal(t, testBodyHTML, body, "offline responses fall through to the generic page")
	assert.NotEmpty(t, body, "the response is never empty")

	assert.NoError(t, ValidateLocalReplyConfig(cfg))
}

// TestInjectLocalReplyConfig_OfflineIdempotent re-runs injection over an HCM
// that already carries a config, which happens whenever EG re-translates.
func TestInjectLocalReplyConfig_OfflineIdempotent(t *testing.T) {
	cfg := testLocalReplyConfig()
	cfg.OfflineBodyHTML = testOfflineBodyHTML
	l := listenerWithHCM(t, "user-chain")

	n1, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	require.Equal(t, 1, n1)

	n2, err := InjectLocalReplyConfig(l, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, n2, "second pass must leave the existing config alone")

	mappers := hcmFromFilter(t, l.FilterChains[0].Filters[0]).GetLocalReplyConfig().GetMappers()
	require.Len(t, mappers, 2, "mappers must not accumulate on re-injection")
	assert.Equal(t, []string{"UH"}, offlineFlags(mappers[0]))
}

// TestEmbeddedOfflinePageIsEnvoySafe mirrors the generic page's guard: the
// offline page's literal CSS percents must survive escaping, or the listener is
// rejected fleet-wide.
func TestEmbeddedOfflinePageIsEnvoySafe(t *testing.T) {
	raw := assets.DefaultErrorOfflineHTML
	require.Contains(t, raw, "height: 100%;", "test premise: raw page has an unescaped percent")

	escaped := escapeEnvoyFormatLiterals(raw)
	assert.Contains(t, escaped, "height: 100%%;")
	assert.Equal(t, 1, strings.Count(escaped, "%RESPONSE_CODE%"))
	assert.NoError(t, assertEnvoyFormatSafe(escaped))

	cfg := testLocalReplyConfig()
	cfg.BodyHTML = assets.DefaultError5xxHTML
	cfg.OfflineBodyHTML = assets.DefaultErrorOfflineHTML
	assert.NoError(t, ValidateLocalReplyConfig(cfg))
}

// TestValidateLocalReplyConfig_RejectsUnsafeOfflineBody proves the validator
// inspects every mapper, not just the first.
func TestValidateLocalReplyConfig_RejectsUnsafeOfflineBody(t *testing.T) {
	assert.Error(t, assertEnvoyFormatSafe("%NOT_A_COMMAND%"))

	cfg := testLocalReplyConfig()
	cfg.OfflineBodyHTML = "offline %NOT_A_COMMAND%"
	// Escaping neutralises it, so the assembled config is still safe to push.
	assert.NoError(t, ValidateLocalReplyConfig(cfg))

	lrc := buildLocalReplyConfig(cfg)
	require.Len(t, lrc.GetMappers(), 2)
	assert.Equal(t, "offline %%NOT_A_COMMAND%%",
		lrc.GetMappers()[0].GetBodyFormatOverride().GetTextFormatSource().GetInlineString())
}
