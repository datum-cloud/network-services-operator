package mutate

import (
	"slices"
	"strings"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	misdirectedVirtualHostName = "datum-misdirected-request"
	misdirectedStatus          = 421
	unroutedVirtualHostName    = "datum-unrouted-hostname"
	unroutedStatus             = 404
)

// GuardMisdirectedRequests answers 421 instead of 404 when a browser reuses one
// site's connection for another, so it retries on a connection of its own. A
// hostname the connection was opened for still answers 404, since a retry would
// land on the same connection, unless another filter chain on the listener
// claims it more specifically.
func GuardMisdirectedRequests(listeners []*listenerv3.Listener, routes []*routev3.RouteConfiguration) int {
	serverNames := map[string]sets.Set[string]{}
	listenerNames := map[string][]sets.Set[string]{}
	unguarded := sets.New[string]()
	for _, l := range listeners {
		chains := append([]*listenerv3.FilterChain{}, l.GetFilterChains()...)
		if dfc := l.GetDefaultFilterChain(); dfc != nil {
			chains = append(chains, dfc)
		}
		onListener := sets.New[string]()
		for _, fc := range chains {
			onListener.Insert(fc.GetFilterChainMatch().GetServerNames()...)
		}
		for _, fc := range chains {
			names := fc.GetFilterChainMatch().GetServerNames()
			for _, rcName := range rdsRouteConfigNames(fc) {
				if fc.GetTransportSocket() == nil || len(names) == 0 {
					unguarded.Insert(rcName)
					continue
				}
				addNames(serverNames, rcName, names...)
				listenerNames[rcName] = append(listenerNames[rcName], onListener)
			}
		}
	}

	guarded := 0
	for _, rc := range routes {
		names, ok := serverNames[rc.GetName()]
		// A second "*" domain would make Envoy reject the whole update.
		if !ok || unguarded.Has(rc.GetName()) || hasCatchAllDomain(rc) {
			continue
		}
		misdirected := []string{"*"}
		if unrouted := unroutedNames(rc, names); len(unrouted) > 0 {
			misdirected = append(misdirected, claimedElsewhere(rc, unrouted, names, listenerNames[rc.GetName()])...)
			rc.VirtualHosts = append(rc.VirtualHosts, directResponseVirtualHost(unroutedVirtualHostName, unrouted, unroutedStatus))
		}
		rc.VirtualHosts = append(rc.VirtualHosts, directResponseVirtualHost(misdirectedVirtualHostName, misdirected, misdirectedStatus))
		guarded++
	}
	return guarded
}

func addNames(m map[string]sets.Set[string], key string, names ...string) {
	if m[key] == nil {
		m[key] = sets.New[string]()
	}
	m[key].Insert(names...)
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

func unroutedNames(rc *routev3.RouteConfiguration, names sets.Set[string]) []string {
	var domains []string
	for _, vh := range rc.GetVirtualHosts() {
		domains = append(domains, vh.GetDomains()...)
	}
	var unrouted []string
	for _, name := range sets.List(names) {
		if !slices.ContainsFunc(domains, func(d string) bool { return domainMatches(d, name) }) {
			unrouted = append(unrouted, name)
		}
	}
	return unrouted
}

func claimedElsewhere(rc *routev3.RouteConfiguration, unrouted []string, own sets.Set[string], listenerNames []sets.Set[string]) []string {
	var wildcards []string
	for _, name := range unrouted {
		if strings.HasPrefix(name, "*") {
			wildcards = append(wildcards, name)
		}
	}
	if len(wildcards) == 0 {
		return nil
	}
	claimed := sets.New[string]()
	for _, names := range listenerNames {
		for name := range names {
			if !own.Has(name) && slices.ContainsFunc(wildcards, func(w string) bool { return domainMatches(w, name) }) {
				claimed.Insert(name)
			}
		}
	}
	return unroutedNames(rc, claimed)
}

func domainMatches(domain, name string) bool {
	if domain == name {
		return true
	}
	suffix, wildcard := strings.CutPrefix(domain, "*")
	return wildcard && name != suffix && strings.HasSuffix(name, suffix)
}

func directResponseVirtualHost(name string, domains []string, status uint32) *routev3.VirtualHost {
	return &routev3.VirtualHost{
		Name:    name,
		Domains: domains,
		Routes: []*routev3.Route{{
			Name: name,
			Match: &routev3.RouteMatch{
				PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
			},
			Action: &routev3.Route_DirectResponse{
				DirectResponse: &routev3.DirectResponseAction{Status: status},
			},
		}},
	}
}
