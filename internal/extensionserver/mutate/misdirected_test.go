package mutate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// withRDS gives the chain an HCM that takes its routes from the named route
// configuration.
func withRDS(t *testing.T, fc *listenerv3.FilterChain, routeConfig string) *listenerv3.FilterChain {
	t.Helper()
	hcmAny, err := anypb.New(&hcmv3.HttpConnectionManager{
		StatPrefix:     routeConfig,
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{Rds: &hcmv3.Rds{RouteConfigName: routeConfig}},
	})
	require.NoError(t, err)
	fc.Filters = append(fc.Filters, &listenerv3.Filter{
		Name:       hcmFilterName,
		ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
	})
	return fc
}

func siteRouteConfig(name, host string) *routev3.RouteConfiguration {
	return &routev3.RouteConfiguration{
		Name:         name,
		VirtualHosts: []*routev3.VirtualHost{{Name: host, Domains: []string{host}}},
	}
}

func findVirtualHost(rc *routev3.RouteConfiguration, name string) *routev3.VirtualHost {
	for _, vh := range rc.GetVirtualHosts() {
		if vh.GetName() == name {
			return vh
		}
	}
	return nil
}

func TestGuardMisdirectedRequests(t *testing.T) {
	quicChain := withRDS(t, &listenerv3.FilterChain{
		Name:             "quic-b",
		FilterChainMatch: &listenerv3.FilterChainMatch{ServerNames: []string{"b.example.net"}},
		TransportSocket:  &corev3.TransportSocket{Name: "envoy.transport_sockets.quic"},
	}, "https-b")
	multiChain := tlsChain(t, "m1.example.net", "secret-m")
	multiChain.FilterChainMatch.ServerNames = []string{"m1.example.net", "m2.example.net"}
	anySNIChain := tlsChain(t, "unused", "secret-any-sni")
	anySNIChain.FilterChainMatch = nil

	listeners := []*listenerv3.Listener{
		{
			Name: "tcp-443",
			FilterChains: []*listenerv3.FilterChain{
				withRDS(t, tlsChain(t, "a.example.net", "secret-a"), "https-a"),
				withRDS(t, tlsChain(t, "empty.example.net", "secret-empty"), "no-routes"),
				withRDS(t, tlsChain(t, "*.w.example.net", "secret-w"), "wildcard-listener"),
				withRDS(t, tlsChain(t, "x.w.example.net", "secret-x"), "under-wildcard"),
				withRDS(t, tlsChain(t, "app.w.example.net", "secret-app"), "routed-under-wildcard"),
				withRDS(t, multiChain, "multi"),
				withRDS(t, tlsChain(t, "app.c.example.net", "secret-c"), "wildcard-route"),
				withRDS(t, tlsChain(t, "shared.example.net", "secret-shared"), "shared"),
				withRDS(t, tlsChain(t, "any.example.net", "secret-any"), "has-catch-all"),
				withRDS(t, tlsChain(t, "s.example.net", "secret-s"), "any-sni"),
				withRDS(t, anySNIChain, "any-sni"),
			},
		},
		{Name: "udp-443", FilterChains: []*listenerv3.FilterChain{quicChain}},
		{
			Name:               "tcp-80",
			FilterChains:       []*listenerv3.FilterChain{withRDS(t, plainChain("shared-plain"), "shared")},
			DefaultFilterChain: withRDS(t, plainChain("http-80"), "http-80"),
		},
	}

	catchAll := siteRouteConfig("has-catch-all", "any.example.net")
	catchAll.VirtualHosts[0].Domains = []string{"*"}
	routes := []*routev3.RouteConfiguration{
		siteRouteConfig("https-a", "a.example.net"),
		siteRouteConfig("https-b", "b.example.net"),
		{Name: "no-routes"},
		siteRouteConfig("wildcard-listener", "app.w.example.net"),
		siteRouteConfig("under-wildcard", "x.w.example.net"),
		siteRouteConfig("routed-under-wildcard", "app.w.example.net"),
		siteRouteConfig("multi", "m1.example.net"),
		siteRouteConfig("wildcard-route", "*.c.example.net"),
		siteRouteConfig("shared", "shared.example.net"),
		catchAll,
		siteRouteConfig("any-sni", "s.example.net"),
		siteRouteConfig("http-80", "plain.example.net"),
	}

	type guard struct{ unrouted, misdirected []string }
	want := map[string]guard{
		"https-a":   {},
		"https-b":   {},
		"no-routes": {unrouted: []string{"empty.example.net"}},
		"wildcard-listener": {
			unrouted:    []string{"*.w.example.net"},
			misdirected: []string{"*", "x.w.example.net"},
		},
		"under-wildcard":        {},
		"routed-under-wildcard": {},
		"multi":                 {unrouted: []string{"m2.example.net"}},
		"wildcard-route":        {},
	}

	require.Equal(t, len(want), GuardMisdirectedRequests(listeners, routes))

	for _, rc := range routes {
		w, guarded := want[rc.GetName()]
		misdirected := findVirtualHost(rc, misdirectedVirtualHostName)
		if !guarded {
			assert.Nil(t, misdirected, "%s is reached in plaintext, from any SNI or already has a catch-all", rc.GetName())
			continue
		}
		require.NotNil(t, misdirected, "%s is only reached over TLS or QUIC and must answer 421", rc.GetName())
		if w.misdirected == nil {
			w.misdirected = []string{"*"}
		}
		assert.Equal(t, w.misdirected, misdirected.GetDomains(), rc.GetName())
		assert.EqualValues(t, misdirectedStatus, misdirected.GetRoutes()[0].GetDirectResponse().GetStatus())

		vh := findVirtualHost(rc, unroutedVirtualHostName)
		if w.unrouted == nil {
			assert.Nil(t, vh, "%s has a virtual host for every server name", rc.GetName())
			continue
		}
		require.NotNil(t, vh, "%s must answer 404 for its own unrouted hostnames", rc.GetName())
		assert.Equal(t, w.unrouted, vh.GetDomains(), rc.GetName())
		assert.EqualValues(t, unroutedStatus, vh.GetRoutes()[0].GetDirectResponse().GetStatus())
	}

	assert.Zero(t, GuardMisdirectedRequests(listeners, routes),
		"a second pass must not add a duplicate \"*\" domain")
}

func TestGuardMisdirectedRequestsSkipsUnreadableHCM(t *testing.T) {
	notAnHCM, err := anypb.New(wrapperspb.String("not an HCM"))
	require.NoError(t, err)
	fc := tlsChain(t, "a.example.net", "secret-a")
	fc.Filters = []*listenerv3.Filter{{
		Name:       hcmFilterName,
		ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: notAnHCM},
	}}
	listeners := []*listenerv3.Listener{{Name: "tcp-443", FilterChains: []*listenerv3.FilterChain{fc}}}
	routes := []*routev3.RouteConfiguration{siteRouteConfig("https-a", "a.example.net")}

	assert.Zero(t, GuardMisdirectedRequests(listeners, routes))
	assert.Len(t, routes[0].GetVirtualHosts(), 1)
}
