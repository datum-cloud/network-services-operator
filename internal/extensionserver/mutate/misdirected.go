package mutate

import (
	"slices"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	misdirectedVirtualHostName = "datum-misdirected-request"
	misdirectedStatus          = 421
)

// GuardMisdirectedRequests answers 421 instead of 404 when a browser reuses one
// site's connection for another, so it retries on a connection of its own.
func GuardMisdirectedRequests(listeners []*listenerv3.Listener, routes []*routev3.RouteConfiguration) int {
	secure, plaintext := sets.New[string](), sets.New[string]()
	for _, l := range listeners {
		chains := append([]*listenerv3.FilterChain{}, l.GetFilterChains()...)
		if dfc := l.GetDefaultFilterChain(); dfc != nil {
			chains = append(chains, dfc)
		}
		for _, fc := range chains {
			for _, name := range rdsRouteConfigNames(fc) {
				if fc.GetTransportSocket() != nil {
					secure.Insert(name)
				} else {
					plaintext.Insert(name)
				}
			}
		}
	}

	guarded := 0
	for _, rc := range routes {
		name := rc.GetName()
		// A second "*" domain would make Envoy reject the whole update.
		if !secure.Has(name) || plaintext.Has(name) || hasCatchAllDomain(rc) {
			continue
		}
		rc.VirtualHosts = append(rc.VirtualHosts, misdirectedVirtualHost())
		guarded++
	}
	return guarded
}

func rdsRouteConfigNames(fc *listenerv3.FilterChain) []string {
	var names []string
	for _, f := range fc.GetFilters() {
		if f.GetName() != hcmFilterName || f.GetTypedConfig() == nil {
			continue
		}
		hcm := &hcmv3.HttpConnectionManager{}
		if err := f.GetTypedConfig().UnmarshalTo(hcm); err != nil {
			continue
		}
		if name := hcm.GetRds().GetRouteConfigName(); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func hasCatchAllDomain(rc *routev3.RouteConfiguration) bool {
	for _, vh := range rc.GetVirtualHosts() {
		if slices.Contains(vh.GetDomains(), "*") {
			return true
		}
	}
	return false
}

func misdirectedVirtualHost() *routev3.VirtualHost {
	return &routev3.VirtualHost{
		Name:    misdirectedVirtualHostName,
		Domains: []string{"*"},
		Routes: []*routev3.Route{{
			Name: misdirectedVirtualHostName,
			Match: &routev3.RouteMatch{
				PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
			},
			Action: &routev3.Route_DirectResponse{
				DirectResponse: &routev3.DirectResponseAction{Status: misdirectedStatus},
			},
		}},
	}
}
