package mutate

import (
	"fmt"
	"strconv"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/encoding/protojson"

	// Blank imports register the message types referenced via "@type" Anys in
	// the cluster JSON below so protojson can resolve them during Unmarshal.
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/internal_upstream/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/raw_buffer/v3"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// ReplaceConnectorClusters iterates the xDS cluster list, identifies clusters
// whose names match the "httproute/<dsNS>/<proxyName>/rule/<idx>" pattern and
// have a ConnectorInfo in idx.Connectors, then:
//   - Online connectors: replaces the cluster in-place with a STATIC
//     internal-upstream cluster pointing at internalListener.
//   - Offline connectors: leaves the cluster unchanged, records in offline map.
//
// Returns (replaced, offline) maps from cluster name to ConnectorInfo.
// The maps are used by ApplyConnectorRoutes to wire CONNECT routes and domains.
func ReplaceConnectorClusters(
	clusters []*clusterv3.Cluster,
	idx *extcache.PolicyIndex,
	internalListener string,
) (replaced, offline map[string]*extcache.ConnectorInfo, err error) {
	replaced = make(map[string]*extcache.ConnectorInfo)
	offline = make(map[string]*extcache.ConnectorInfo)

	for i, cl := range clusters {
		name := cl.GetName()
		dsNS, proxyName, ruleIndex, ok := parseConnectorClusterName(name)
		if !ok {
			continue
		}

		upstreamNS, ok := idx.DStoUS[dsNS]
		if !ok {
			continue
		}

		key := extcache.ConnectorKey{
			UpstreamNS:    upstreamNS,
			HTTPProxyName: proxyName,
			RuleIndex:     ruleIndex,
		}
		info, ok := idx.Connectors[key]
		if !ok {
			continue
		}

		if !info.Online {
			offline[name] = &info
			continue
		}

		nc, err := buildConnectorCluster(name, &info, internalListener)
		if err != nil {
			return nil, nil, fmt.Errorf("build connector cluster %q: %w", name, err)
		}
		clusters[i] = nc
		replaced[name] = &info
	}
	return replaced, offline, nil
}

// ApplyConnectorRoutes applies connector route mutations to a RouteConfiguration:
//   - Online (replaced) connector found in a VH: prepend a CONNECT upgrade route
//     targeting the replaced cluster and append info.TargetHost to VH domains.
//   - Offline connector found in a VH: prepend a direct_response 503 CONNECT
//     route (for CONNECT/tunnel-control clients) AND rewrite the user-facing
//     forwarding routes that target the (endpoint-less) offline connector
//     cluster into an immediate direct_response 503. Without the latter, a
//     normal user GET falls through to the prefix:"/" route, which still targets
//     the zero-endpoint cluster — Envoy then returns a generic 503
//     no_healthy_upstream (UH) instead of a deterministic tunnel-offline
//     response, and the empty cluster triggers retries/cluster-stat noise.
//
// Returns the number of VirtualHosts mutated and the number of forwarding
// routes converted to a tunnel-offline direct_response.
func ApplyConnectorRoutes(
	rc *routev3.RouteConfiguration,
	idx *extcache.PolicyIndex,
	replaced, offline map[string]*extcache.ConnectorInfo,
) (mutated, converted int, err error) {
	for _, vh := range rc.GetVirtualHosts() {
		// Find any connector cluster referenced by routes in this VH.
		var connectorCluster string
		for _, rt := range vh.GetRoutes() {
			cl := routeCluster(rt)
			if _, ok := replaced[cl]; ok {
				connectorCluster = cl
				break
			}
			if _, ok := offline[cl]; ok {
				connectorCluster = cl
				break
			}
		}
		if connectorCluster == "" {
			continue
		}

		var newRoute *routev3.Route

		if info, ok := replaced[connectorCluster]; ok {
			// Online: CONNECT route targeting the replaced cluster.
			newRoute, err = buildConnectRoute(
				"connector-connect-"+sanitizeID(vh.GetName()),
				connectorCluster,
			)
			if err != nil {
				return mutated, converted, fmt.Errorf("build connect route for %q: %w", vh.GetName(), err)
			}
			// Prepend the CONNECT route (NSO inserts at /virtual_hosts/0/routes/0).
			vh.Routes = append([]*routev3.Route{newRoute}, vh.Routes...)
			// Append the connector's target host to VH domains.
			// Production uses the actual backend hostname (conflict C1 in design plan),
			// NOT a synthetic .connector.local domain.
			vh.Domains = appendUnique(vh.Domains, info.TargetHost)
		} else {
			// Offline: prepend the direct_response 503 connect_matcher route so
			// CONNECT (tunnel-control) clients still get "Tunnel not online".
			newRoute, err = buildOfflineRoute("connector-offline-" + sanitizeID(vh.GetName()))
			if err != nil {
				return mutated, converted, fmt.Errorf("build offline route for %q: %w", vh.GetName(), err)
			}
			vh.Routes = append([]*routev3.Route{newRoute}, vh.Routes...)
			// No domain appended for offline connectors (no live tunnel).

			// Rewrite the user-facing forwarding route(s) that target the
			// endpoint-less offline connector cluster into an immediate 503
			// direct_response. Otherwise a normal GET falls through to the
			// prefix:"/" route, hits the zero-endpoint cluster, and Envoy
			// returns a generic 503 no_healthy_upstream (UH) plus retry/stat
			// noise. We only replace the route's Action oneof, so each route's
			// match, metadata, and typed_per_filter_config are preserved.
			//
			// Idempotent: the prepended connect_matcher route and any already
			// converted forwarding route are direct_responses, so routeCluster
			// returns "" for them and they never re-match connectorCluster.
			for _, rt := range vh.GetRoutes() {
				if routeCluster(rt) != connectorCluster {
					continue
				}
				if derr := setRouteDirectResponse(rt, 503, offlineResponseBody); derr != nil {
					return mutated, converted, fmt.Errorf("convert offline forward route for %q: %w", vh.GetName(), derr)
				}
				converted++
			}
		}
		mutated++
	}
	return mutated, converted, nil
}

// --- Internal helpers ---

// parseConnectorClusterName parses the EG-assigned connector cluster name:
//
//	"httproute/<dsNS>/<httpProxyName>/rule/<ruleIndex>"
//
// Returns the components and true if the name matches the expected pattern.
func parseConnectorClusterName(name string) (dsNS, proxyName string, ruleIndex int, ok bool) {
	parts := strings.Split(name, "/")
	if len(parts) != 5 || parts[0] != "httproute" || parts[3] != "rule" {
		return
	}
	idx, err := strconv.Atoi(parts[4])
	if err != nil {
		return
	}
	return parts[1], parts[2], idx, true
}

// routeCluster returns the upstream cluster name a forwarding route targets.
func routeCluster(rt *routev3.Route) string {
	if ra := rt.GetRoute(); ra != nil {
		return ra.GetCluster()
	}
	return ""
}

// buildConnectorCluster constructs the STATIC internal-upstream cluster for an
// online connector. It mirrors buildConnectorInternalListenerClusterJSON in
// internal/controller/connector_routing_compiler.go but operates on proto types
// via protojson rather than raw JSON patches.
//
// The cluster keeps the original EG-assigned name so it transparently replaces
// the backend cluster without requiring route changes.
func buildConnectorCluster(
	name string,
	info *extcache.ConnectorInfo,
	internalListener string,
) (*clusterv3.Cluster, error) {
	tunnelAddress := fmt.Sprintf("%s:%d", info.TargetHost, info.TargetPort)

	clusterJSON := fmt.Sprintf(`{
  "name": %q,
  "type": "STATIC",
  "connect_timeout": "5s",
  "load_assignment": {
    "cluster_name": %q,
    "endpoints": [{
      "lb_endpoints": [{
        "endpoint": {
          "address": {
            "envoy_internal_address": { "server_listener_name": %q }
          }
        },
        "metadata": {
          "filter_metadata": {
            "tunnel": { "address": %q, "endpoint_id": %q }
          }
        }
      }]
    }]
  },
  "transport_socket": {
    "name": "envoy.transport_sockets.internal_upstream",
    "typed_config": {
      "@type": "type.googleapis.com/envoy.extensions.transport_sockets.internal_upstream.v3.InternalUpstreamTransport",
      "passthrough_metadata": [{ "kind": { "host": {} }, "name": "tunnel" }],
      "transport_socket": {
        "name": "envoy.transport_sockets.raw_buffer",
        "typed_config": { "@type": "type.googleapis.com/envoy.extensions.transport_sockets.raw_buffer.v3.RawBuffer" }
      }
    }
  }
}`, name, name, internalListener, tunnelAddress, info.NodeID)

	cl := &clusterv3.Cluster{}
	if err := protojson.Unmarshal([]byte(clusterJSON), cl); err != nil {
		return nil, fmt.Errorf("unmarshal connector cluster JSON: %w", err)
	}
	return cl, nil
}

// buildConnectRoute builds an online CONNECT route (connect_matcher + CONNECT
// upgrade) pointed at the given connector cluster. Mirrors buildConnectRoute in
// test/perf/extserver/internal/mutate/connector.go.
func buildConnectRoute(name, cluster string) (*routev3.Route, error) {
	j := fmt.Sprintf(`{
  "name": %q,
  "match": { "connect_matcher": {} },
  "route": {
    "cluster": %q,
    "upgrade_configs": [{ "upgrade_type": "CONNECT", "connect_config": {} }]
  }
}`, name, cluster)
	rt := &routev3.Route{}
	if err := protojson.Unmarshal([]byte(j), rt); err != nil {
		return nil, fmt.Errorf("unmarshal CONNECT route JSON: %w", err)
	}
	return rt, nil
}

// offlineResponseBody is the inline body returned for tunnel-offline 503
// responses. Shared by the connect_matcher offline route (buildOfflineRoute)
// and the user-facing forwarding-route conversion (setRouteDirectResponse) so
// both paths return an identical body. The exact string is part of the
// STATE.md metadata contract.
const offlineResponseBody = "Tunnel not online"

// buildOfflineRoute builds the offline CONNECT route (direct_response 503
// "Tunnel not online"). Mirrors buildOfflineRoute in the seed.
func buildOfflineRoute(name string) (*routev3.Route, error) {
	j := fmt.Sprintf(`{
  "name": %q,
  "match": { "connect_matcher": {} },
  "direct_response": { "status": 503, "body": { "inline_string": %q } }
}`, name, offlineResponseBody)
	rt := &routev3.Route{}
	if err := protojson.Unmarshal([]byte(j), rt); err != nil {
		return nil, fmt.Errorf("unmarshal offline route JSON: %w", err)
	}
	return rt, nil
}

// setRouteDirectResponse rewrites a route's action to an immediate
// direct_response with the given status and inline body. Only the Action oneof
// is replaced, so the route's match, metadata, and typed_per_filter_config are
// preserved. Used to convert a forwarding route that targets an endpoint-less
// offline connector cluster into a deterministic tunnel-offline response.
func setRouteDirectResponse(rt *routev3.Route, status uint32, body string) error {
	j := fmt.Sprintf(`{ "status": %d, "body": { "inline_string": %q } }`, status, body)
	dr := &routev3.DirectResponseAction{}
	if err := protojson.Unmarshal([]byte(j), dr); err != nil {
		return fmt.Errorf("unmarshal direct_response action JSON: %w", err)
	}
	rt.Action = &routev3.Route_DirectResponse{DirectResponse: dr}
	return nil
}
