package mutate

import (
	"fmt"
	"testing"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// builtTargets flattens a BuiltTPPs into "policy -> target[/section]" strings
// with their generation, so a test can compare it with a literal.
func builtTargets(b BuiltTPPs) map[string]int64 {
	out := map[string]int64{}
	for key, entry := range b {
		for _, ref := range entry.Targets {
			target := string(ref.Kind) + "/" + string(ref.Name)
			if ref.SectionName != nil {
				target += "/" + string(*ref.SectionName)
			}
			out[key+" -> "+target] = entry.Generation
		}
	}
	return out
}

func TestApplyTPPRouteConfig_RecordsBuiltTargets(t *testing.T) {
	str := func(s string) *string { return &s }
	enforce := networkingv1alpha.TrafficProtectionPolicyEnforce
	ruleRoute := func(rule int) *routev3.Route {
		return envoyRoute(t,
			fmt.Sprintf("httproute/ns-abc-123/%s/rule/%d/match/0/app_example_com", sectionTestProxy, rule),
			fmt.Sprintf("httproute/ns-abc-123/%s/rule/%d", sectionTestProxy, rule))
	}
	withoutDirectives := func(tpp extcache.TPPInfo) extcache.TPPInfo {
		tpp.Directives = nil
		return tpp
	}
	withGeneration := func(tpp extcache.TPPInfo, generation int64) extcache.TPPInfo {
		tpp.Generation = generation
		return tpp
	}
	withExtraGateway := func(tpp extcache.TPPInfo, gwName string) extcache.TPPInfo {
		tpp.TargetRefs = append(tpp.TargetRefs, gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
			LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{Kind: "Gateway", Name: gatewayv1.ObjectName(gwName)},
		})
		return tpp
	}

	tests := []struct {
		name     string
		tpps     []extcache.TPPInfo
		disabled bool
		want     map[string]int64
	}{
		{
			name: "a gateway policy whose gateway is built, at the generation the build read",
			tpps: []extcache.TPPInfo{withGeneration(tppTargetingGateway("gw", "smoke-gw"), 7)},
			want: map[string]int64{"test-project/gw -> Gateway/smoke-gw": 7},
		},
		{
			name: "a policy the extension server refuses to build (no directives: inverted paranoia levels) is not built",
			tpps: []extcache.TPPInfo{withoutDirectives(tppTargetingGateway("gw", "smoke-gw"))},
			want: map[string]int64{},
		},
		{
			name: "a gateway policy overridden on every route by a route policy is built too",
			tpps: []extcache.TPPInfo{tppTargetingGateway("gw", "smoke-gw"), sectionTPP("route", enforce, nil)},
			want: map[string]int64{
				"test-project/gw -> Gateway/smoke-gw": 1,
				"test-project/route -> HTTPRoute/alb": 1,
			},
		},
		{
			name: "a policy whose gateway is not in the build is not built",
			tpps: []extcache.TPPInfo{tppTargetingGateway("gw", "other-gw")},
			want: map[string]int64{},
		},
		{
			name: "only the targets in the build are recorded",
			tpps: []extcache.TPPInfo{withExtraGateway(tppTargetingGateway("gw", "smoke-gw"), "other-gw")},
			want: map[string]int64{"test-project/gw -> Gateway/smoke-gw": 1},
		},
		{
			name: "a rule-scoped policy on a rule the build resolves",
			tpps: []extcache.TPPInfo{sectionTPP("sec", enforce, str("protected"))},
			want: map[string]int64{"test-project/sec -> HTTPRoute/alb/protected": 1},
		},
		{
			name: "a rule-scoped policy on a rule the build cannot resolve is not built",
			tpps: []extcache.TPPInfo{sectionTPP("sec", enforce, str("missing"))},
			want: map[string]int64{},
		},
		{
			name:     "nothing is built while Coraza is disabled",
			tpps:     []extcache.TPPInfo{tppTargetingGateway("gw", "smoke-gw"), sectionTPP("route", enforce, nil)},
			disabled: true,
			want:     map[string]int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := policyIndex(tt.tpps...)
			idx.HTTPProxyRules = map[extcache.HTTPProxyKey][]string{
				{Namespace: "ns-abc-123", Name: sectionTestProxy}: {"exempt", "protected"},
			}
			rc := &routev3.RouteConfiguration{VirtualHosts: []*routev3.VirtualHost{
				buildVHWithGatewayMeta(ruleRoute(0), ruleRoute(1)),
			}}
			cfg := testCorazaConfig()
			cfg.Disabled = tt.disabled

			built := BuiltTPPs{}
			_, err := ApplyTPPRouteConfig(rc, idx, cfg, built)
			require.NoError(t, err)
			assert.Equal(t, tt.want, builtTargets(built))
		})
	}
}

func TestApplyTPPRouteConfig_NilBuiltRecordsNothing(t *testing.T) {
	idx := policyIndex(tppTargetingGateway("gw", "smoke-gw"))
	rc := &routev3.RouteConfiguration{VirtualHosts: []*routev3.VirtualHost{buildVHWithGatewayMeta(&routev3.Route{Name: "r0"})}}

	_, err := ApplyTPPRouteConfig(rc, idx, testCorazaConfig(), nil)
	require.NoError(t, err)
}
