package parity

import (
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

// This file scans the proxy's live configuration for each kind of change and
// emits an identity for every one it finds. These identity strings must match
// the ones the extension server produces exactly, or the comparison is
// meaningless; keep both sides in lockstep.

const (
	keySep = "##"

	// Well-known marker strings, mirrored from where the configuration is written.
	datumGatewayMetaKey        = "datum-gateway"
	connectorInternalTransport = "envoy.transport_sockets.internal_upstream"
	hcmNetworkFilterName       = "envoy.filters.network.http_connection_manager"
	offlineBodyMarker          = "Tunnel not online"
)

// ScanActual scans the proxy's live configuration and assembles what it
// actually found. corazaFilterName is the firewall filter name used to spot
// firewall injection; droppedSecrets names the TLS certificates that should have
// been removed.
func ScanActual(dump *ConfigDump, corazaFilterName string, droppedSecrets []string) Actual {
	act := Actual{Keys: map[Family][]string{}}

	for _, rc := range dump.Routes {
		scanRouteConfig(rc, &act)
	}
	for _, cl := range dump.Clusters {
		if isReplacedConnectorCluster(cl) {
			act.Keys[FamilyConnectorCluster] = append(act.Keys[FamilyConnectorCluster], cl.GetName())
		}
	}
	for _, l := range dump.Listeners {
		scanListener(l, corazaFilterName, &act)
	}

	// Flag any certificate that should have been removed but is still present.
	if len(droppedSecrets) > 0 {
		present := make(map[string]struct{}, len(dump.SecretNames))
		for _, n := range dump.SecretNames {
			present[n] = struct{}{}
		}
		for _, dropped := range droppedSecrets {
			if _, ok := present[dropped]; ok {
				act.TLSDroppedSecretsStillPresent = append(act.TLSDroppedSecretsStillPresent, dropped)
			}
		}
	}
	return act
}

func scanRouteConfig(rc *routev3.RouteConfiguration, act *Actual) {
	rcName := rc.GetName()
	for _, vh := range rc.GetVirtualHosts() {
		vhName := vh.GetName()
		for _, rt := range vh.GetRoutes() {
			if ns, name, mode, ok := datumGatewayTPP(rt); ok {
				act.Keys[FamilyWAFRoute] = append(act.Keys[FamilyWAFRoute],
					wafRouteKey(rcName, vhName, rt.GetName(), ns, name, mode))
			}
			if isConnectRoute(rt) {
				act.Keys[FamilyConnectorRoute] = append(act.Keys[FamilyConnectorRoute],
					connectorRouteKey(rcName, vhName, rt.GetName()))
			}
			if isOfflineDirectResponse(rt) {
				act.Keys[FamilyConnectorOffline] = append(act.Keys[FamilyConnectorOffline],
					connectorRouteKey(rcName, vhName, rt.GetName()))
			}
		}
	}
}

func scanListener(l *listenerv3.Listener, corazaFilterName string, act *Actual) {
	lName := l.GetName()
	eachHCM(l, func(chainName string, hcm *hcmv3.HttpConnectionManager) {
		if corazaFilterName != "" && hcmHasFilterAtZero(hcm, corazaFilterName) {
			act.Keys[FamilyWAFHCM] = append(act.Keys[FamilyWAFHCM], listenerChainKey(lName, chainName))
		}
		if hcm.GetLocalReplyConfig() != nil {
			act.Keys[FamilyLocalReply] = append(act.Keys[FamilyLocalReply], listenerChainKey(lName, chainName))
		}
	})
}

// --- identity formats (MUST match the extension server) ---

func wafRouteKey(rc, vh, rt, tppNS, tppName, mode string) string {
	return strings.Join([]string{rc, vh, rt, tppNS + "/" + tppName + "/" + mode}, keySep)
}

func connectorRouteKey(rc, vh, rt string) string {
	return strings.Join([]string{rc, vh, rt}, keySep)
}

func listenerChainKey(listener, chain string) string {
	return strings.Join([]string{listener, chain}, keySep)
}

// --- detectors (mirror, in reverse, how the configuration is written) ---

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

func isReplacedConnectorCluster(cl *clusterv3.Cluster) bool {
	if cl.GetType() != clusterv3.Cluster_STATIC {
		return false
	}
	return cl.GetTransportSocket().GetName() == connectorInternalTransport
}

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

func hcmHasFilterAtZero(hcm *hcmv3.HttpConnectionManager, filterName string) bool {
	fs := hcm.GetHttpFilters()
	return len(fs) > 0 && fs[0].GetName() == filterName
}
