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
		Name:            "quic-b",
		TransportSocket: &corev3.TransportSocket{Name: "envoy.transport_sockets.quic"},
	}, "https-b")

	listeners := []*listenerv3.Listener{
		{
			Name: "tcp-443",
			FilterChains: []*listenerv3.FilterChain{
				withRDS(t, tlsChain(t, "a.example.net", "secret-a"), "https-a"),
				withRDS(t, tlsChain(t, "shared.example.net", "secret-shared"), "shared"),
				withRDS(t, tlsChain(t, "any.example.net", "secret-any"), "has-catch-all"),
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
		siteRouteConfig("shared", "shared.example.net"),
		catchAll,
		siteRouteConfig("http-80", "plain.example.net"),
	}

	require.Equal(t, 2, GuardMisdirectedRequests(listeners, routes))

	for _, rc := range routes[:2] {
		vh := findVirtualHost(rc, misdirectedVirtualHostName)
		require.NotNil(t, vh, "%s is only reached over TLS or QUIC and must answer 421", rc.GetName())
		assert.Equal(t, []string{"*"}, vh.GetDomains())
		assert.EqualValues(t, misdirectedStatus, vh.GetRoutes()[0].GetDirectResponse().GetStatus())
		assert.Equal(t, rc.GetVirtualHosts()[0].GetName(), rc.GetVirtualHosts()[0].GetDomains()[0],
			"the site's own virtual host stays first and unchanged")
	}
	for _, rc := range routes[2:] {
		assert.Nil(t, findVirtualHost(rc, misdirectedVirtualHostName),
			"%s is reached in plaintext or already has a catch-all and must be left alone", rc.GetName())
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
