package server

import (
	"sort"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

// These read-only scanners extract an identity for each change the build made.
// They mirror, in reverse, how the configuration is written, and they define
// the exact identity format the parity test must reproduce. The "##" separator
// is used because it cannot appear in any of the names it joins.

const keySep = "##"

// wafRouteKey is the identity of a route protected by a firewall:
//
//	<routeConfig>##<virtualHost>##<routeName>##<policyNamespace>/<policyName>/<mode>
//
// The governing policy is part of the identity, so a route protected by the
// wrong policy produces a different identity and is caught as a mismatch.
func wafRouteKey(rc, vh, rt, tppNS, tppName, mode string) string {
	return strings.Join([]string{rc, vh, rt, tppNS + "/" + tppName + "/" + mode}, keySep)
}

// connectorRouteKey is the identity of a connector route (online or offline):
//
//	<routeConfig>##<virtualHost>##<routeName>
func connectorRouteKey(rc, vh, rt string) string {
	return strings.Join([]string{rc, vh, rt}, keySep)
}

// listenerChainKey is the identity of a changed listener filter chain:
//
//	<listenerName>##<filterChainName>
func listenerChainKey(listener, chain string) string {
	return strings.Join([]string{listener, chain}, keySep)
}

// datumGatewayTPP returns the protection policy governing a route (namespace,
// name, mode). ok is false when the route carries no such marker, meaning it is
// not protected.
func datumGatewayTPP(rt *routev3.Route) (ns, name, mode string, ok bool) {
	md := rt.GetMetadata()
	if md == nil {
		return "", "", "", false
	}
	dg := md.GetFilterMetadata()[datumGatewayMetaKey]
	if dg == nil {
		return "", "", "", false
	}
	res := dg.GetFields()["resources"].GetListValue()
	if res == nil || len(res.GetValues()) == 0 {
		return "", "", "", false
	}
	f := res.GetValues()[0].GetStructValue().GetFields()
	return f["namespace"].GetStringValue(),
		f["name"].GetStringValue(),
		f["mode"].GetStringValue(),
		true
}

// isConnectRoute reports whether a route is an online connector tunnel route.
func isConnectRoute(rt *routev3.Route) bool {
	ra := rt.GetRoute()
	if ra == nil {
		return false
	}
	for _, uc := range ra.GetUpgradeConfigs() {
		if strings.EqualFold(uc.GetUpgradeType(), "CONNECT") {
			return true
		}
	}
	return false
}

// isOfflineDirectResponse reports whether a route directly returns the
// tunnel-offline 503 response, covering both the dedicated offline route and
// user-facing routes rewritten to it.
func isOfflineDirectResponse(rt *routev3.Route) bool {
	dr := rt.GetDirectResponse()
	if dr == nil {
		return false
	}
	if dr.GetStatus() != 503 {
		return false
	}
	return dr.GetBody().GetInlineString() == offlineBodyMarker
}

// offlineBodyMarker is the response body the connector offline path writes,
// duplicated here so the scanner needs no import dependency.
const offlineBodyMarker = "Tunnel not online"

// isReplacedConnectorCluster reports whether a connector cluster has been
// replaced with its tunnel form. A cluster that has not been replaced means the
// substitution failed.
func isReplacedConnectorCluster(cl *clusterv3.Cluster) bool {
	if cl.GetType() != clusterv3.Cluster_STATIC {
		return false
	}
	return cl.GetTransportSocket().GetName() == connectorInternalTransport
}

// eachHCM invokes fn for every connection manager across all of the listener's
// filter chains, including the default chain. One that can't be decoded is
// skipped, since recording must never fail the build.
func eachHCM(l *listenerv3.Listener, fn func(chainName string, hcm *hcmv3.HttpConnectionManager)) {
	chains := make([]*listenerv3.FilterChain, 0, len(l.GetFilterChains())+1)
	chains = append(chains, l.GetFilterChains()...)
	if dfc := l.GetDefaultFilterChain(); dfc != nil {
		chains = append(chains, dfc)
	}
	for _, fc := range chains {
		for _, f := range fc.GetFilters() {
			if f.GetName() != hcmNetworkFilterName {
				continue
			}
			tc := f.GetTypedConfig()
			if tc == nil {
				continue
			}
			hcm := &hcmv3.HttpConnectionManager{}
			if err := tc.UnmarshalTo(hcm); err != nil {
				continue
			}
			fn(fc.GetName(), hcm)
		}
	}
}

// hcmHasFilterAtZero reports whether the first filter is named filterName. The
// firewall is always inserted first, so checking the first position is the
// precise signal that it was injected.
func hcmHasFilterAtZero(hcm *hcmv3.HttpConnectionManager, filterName string) bool {
	fs := hcm.GetHttpFilters()
	return len(fs) > 0 && fs[0].GetName() == filterName
}

// sortDedup returns a sorted, de-duplicated copy of in.
func sortDedup(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
