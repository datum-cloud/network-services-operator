package mutate

import (
	"fmt"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	// OfflineBackendClusterName serves a backend with no ready endpoints.
	OfflineBackendClusterName = "datum-offline-backend"
	// OfflineTunnelClusterName serves an offline connector tunnel.
	OfflineTunnelClusterName = "datum-offline-tunnel"
)

// emptyBackendStatus is the status Envoy Gateway collapses a route to when the
// backend Service exists but has no ready endpoints. It is the only collapse
// case EG answers with 503; every other one uses 500.
const emptyBackendStatus = 503

// EnsureOfflineCluster appends the named endpoint-less cluster if it is absent,
// returning the cluster set and whether it added one. A request routed there
// fails at host selection, so Envoy answers with the UH response flag the
// branded offline page selects on.
func EnsureOfflineCluster(clusters []*clusterv3.Cluster, name string) ([]*clusterv3.Cluster, bool, error) {
	for _, c := range clusters {
		if c.GetName() == name {
			return clusters, false, nil
		}
	}

	j := fmt.Sprintf(`{
  "name": %q,
  "type": "STATIC",
  "connect_timeout": "1s",
  "load_assignment": { "cluster_name": %q, "endpoints": [] }
}`, name, name)

	c := &clusterv3.Cluster{}
	if err := protojson.Unmarshal([]byte(j), c); err != nil {
		return clusters, false, fmt.Errorf("unmarshal offline cluster %q JSON: %w", name, err)
	}
	return append(clusters, c), true, nil
}

// RouteEmptyBackendsToOfflineCluster points every route Envoy Gateway collapsed
// to a bodiless 503 at the shared endpoint-less cluster, returning how many it
// rewrote. A direct_response short-circuits before the router filter, so it
// carries neither the UH flag nor any route-supplied header the offline page
// could select on, and its body is overridden by local_reply_config. Forwarding
// restores the flag.
//
// Replacing only the Action oneof preserves each route's match, metadata and
// per-filter config, and makes a second pass a no-op.
func RouteEmptyBackendsToOfflineCluster(rc *routev3.RouteConfiguration) int {
	rewritten := 0
	for _, vh := range rc.GetVirtualHosts() {
		for _, rt := range vh.GetRoutes() {
			if !isEmptyBackendDirectResponse(rt) {
				continue
			}
			rt.Action = &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{
						Cluster: OfflineBackendClusterName,
					},
				},
			}
			rewritten++
		}
	}
	return rewritten
}

// isEmptyBackendDirectResponse reports whether a route is EG's "no ready
// endpoints" collapse. The absent body is what separates it from NSO's own
// connector-offline routes and from a Gateway API filter's direct response.
func isEmptyBackendDirectResponse(rt *routev3.Route) bool {
	dr := rt.GetDirectResponse()
	if dr == nil || dr.GetStatus() != emptyBackendStatus {
		return false
	}
	return dr.GetBody() == nil
}
