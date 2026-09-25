package mutate

import (
	"fmt"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/encoding/protojson"
)

// The endpoint-less clusters user traffic is pointed at when its backend cannot
// serve. One shared cluster per reason rather than one per backend or per
// connector: an endpoint-less cluster carries ~108 resident stats in the data
// plane, so per-backend clusters would scale that by the number of idle
// services, while these two keep it constant.
//
// The two reasons stay apart so the data-plane stats say which one a request
// hit, and so the parity scanner can tell the families apart.
const (
	// OfflineBackendClusterName serves a backend with no ready endpoints.
	OfflineBackendClusterName = "datum-offline-backend"
	// OfflineTunnelClusterName serves an offline connector tunnel.
	OfflineTunnelClusterName = "datum-offline-tunnel"
)

// emptyBackendStatus is the status Envoy Gateway collapses a route to when the
// backend Service exists but has no ready endpoints. It is the only collapse
// case EG answers with 503 — every other one (filter error, invalid
// destination, all-zero weights) uses 500 — which is what makes the status a
// safe discriminator. See EG internal/gatewayapi/route.go, "return 503 if no
// ready endpoints exist".
const emptyBackendStatus = 503

// EnsureOfflineCluster appends the named shared endpoint-less cluster to the
// xDS cluster set if it is not already present, returning the (possibly
// extended) set and whether it added one.
//
// The cluster is STATIC with an empty endpoint list. A request routed to it
// fails at host selection, so Envoy answers 503 with the UH (no healthy
// upstream) response flag and never attempts a connection. UH is what the
// branded offline page keys on; see buildLocalReplyConfig in localreply.go.
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

// RouteEmptyBackendsToOfflineCluster rewrites every route Envoy Gateway
// collapsed to a bodiless 503 direct_response so it forwards to the shared
// endpoint-less cluster instead.
//
// A direct_response short-circuits before the router filter runs, so such a
// route produces no response flag and no upstream attempt. That makes the
// offline page unreachable: local_reply_config mappers can only select on the
// request and on Envoy's own response metadata, and a direct_response supplies
// neither a UH flag nor any route-supplied header (route-level
// request_headers_to_add is applied by the router filter, which never runs).
// A direct_response body is no use either — local_reply_config overrides it.
// Forwarding to an endpoint-less cluster restores the UH flag, which is the
// only signal the mapper can actually match.
//
// Only the Action oneof is replaced, so each route's match, metadata and
// typed_per_filter_config survive — a route that already carries Coraza
// per-route config keeps it. No retry policy is attached: with no hosts there
// is nothing to retry onto, and Envoy fails fast at host selection.
//
// Idempotent: a rewritten route is no longer a direct_response, so a second
// pass does not match it.
//
// Returns the number of routes rewritten.
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

// isEmptyBackendDirectResponse reports whether a route is EG's "backend has no
// ready endpoints" collapse, as opposed to any other direct_response.
//
// The body check is what separates it from NSO's own connector-offline routes,
// which carry an explicit body, and from a Gateway API filter's direct
// response. EG sets no body on the collapse.
func isEmptyBackendDirectResponse(rt *routev3.Route) bool {
	dr := rt.GetDirectResponse()
	if dr == nil || dr.GetStatus() != emptyBackendStatus {
		return false
	}
	return dr.GetBody() == nil
}
