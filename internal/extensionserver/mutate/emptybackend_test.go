package mutate

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/types/known/anypb"
)

func directResponseRoute(name string, status uint32, body string) *routev3.Route {
	dr := &routev3.DirectResponseAction{Status: status}
	if body != "" {
		dr.Body = &corev3.DataSource{
			Specifier: &corev3.DataSource_InlineString{InlineString: body},
		}
	}
	return &routev3.Route{
		Name:   name,
		Match:  &routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}},
		Action: &routev3.Route_DirectResponse{DirectResponse: dr},
	}
}

func routeConfigWith(routes ...*routev3.Route) *routev3.RouteConfiguration {
	return &routev3.RouteConfiguration{
		Name:         "rc",
		VirtualHosts: []*routev3.VirtualHost{{Name: "vh", Routes: routes}},
	}
}

func firstRoute(rc *routev3.RouteConfiguration) *routev3.Route {
	return rc.GetVirtualHosts()[0].GetRoutes()[0]
}

func TestRouteEmptyBackendsToOfflineCluster(t *testing.T) {
	tests := []struct {
		name          string
		route         *routev3.Route
		wantRewritten int
	}{
		{
			name:          "EG collapse for no ready endpoints is rewritten",
			route:         directResponseRoute("empty-backend", 503, ""),
			wantRewritten: 1,
		},
		{
			name:          "connector offline route carries a body and is left alone",
			route:         directResponseRoute("connector-offline", 503, offlineResponseBody),
			wantRewritten: 0,
		},
		{
			name:          "EG collapse for a filter error uses 500 and is left alone",
			route:         directResponseRoute("filter-error", 500, ""),
			wantRewritten: 0,
		},
		{
			name:          "EG collapse for an invalid destination uses 500 and is left alone",
			route:         directResponseRoute("invalid-destination", 500, ""),
			wantRewritten: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := routeConfigWith(tt.route)

			got := RouteEmptyBackendsToOfflineCluster(rc)
			if got != tt.wantRewritten {
				t.Fatalf("rewritten = %d, want %d", got, tt.wantRewritten)
			}

			action := firstRoute(rc).GetRoute()
			if tt.wantRewritten == 0 {
				if action != nil {
					t.Fatalf("route was rewritten to cluster %q, want direct_response preserved", action.GetCluster())
				}
				return
			}
			if action == nil {
				t.Fatal("route still has a direct_response, want a forwarding route")
			}
			if action.GetCluster() != OfflineBackendClusterName {
				t.Fatalf("cluster = %q, want %q", action.GetCluster(), OfflineBackendClusterName)
			}
			if action.GetRetryPolicy() != nil {
				t.Error("rewritten route carries a retry policy; there is nothing to retry onto")
			}
		})
	}
}

// A rewritten route must keep everything the route carried besides its action —
// most importantly the Coraza per-route config a governed route holds, which
// lives in typed_per_filter_config.
func TestRouteEmptyBackendsPreservesRouteState(t *testing.T) {
	perFilter, err := anypb.New(&routev3.FilterConfig{})
	if err != nil {
		t.Fatalf("build per-filter config: %v", err)
	}

	rt := directResponseRoute("empty-backend", 503, "")
	rt.TypedPerFilterConfig = map[string]*anypb.Any{"coraza": perFilter}

	rc := routeConfigWith(rt)
	if got := RouteEmptyBackendsToOfflineCluster(rc); got != 1 {
		t.Fatalf("rewritten = %d, want 1", got)
	}

	out := firstRoute(rc)
	if out.GetName() != "empty-backend" {
		t.Errorf("name = %q, want %q", out.GetName(), "empty-backend")
	}
	if out.GetMatch().GetPrefix() != "/" {
		t.Errorf("match prefix = %q, want %q", out.GetMatch().GetPrefix(), "/")
	}
	if _, ok := out.GetTypedPerFilterConfig()["coraza"]; !ok {
		t.Error("typed_per_filter_config lost; a governed route would lose its WAF config")
	}
}

func TestRouteEmptyBackendsIsIdempotent(t *testing.T) {
	rc := routeConfigWith(directResponseRoute("empty-backend", 503, ""))

	if got := RouteEmptyBackendsToOfflineCluster(rc); got != 1 {
		t.Fatalf("first pass rewritten = %d, want 1", got)
	}
	if got := RouteEmptyBackendsToOfflineCluster(rc); got != 0 {
		t.Fatalf("second pass rewritten = %d, want 0", got)
	}
	if cl := firstRoute(rc).GetRoute().GetCluster(); cl != OfflineBackendClusterName {
		t.Fatalf("cluster = %q, want %q", cl, OfflineBackendClusterName)
	}
}

func TestEnsureOfflineCluster(t *testing.T) {
	clusters, added, err := EnsureOfflineCluster(nil, OfflineBackendClusterName)
	if err != nil {
		t.Fatalf("EnsureOfflineCluster: %v", err)
	}
	if !added {
		t.Fatal("added = false on an empty cluster set, want true")
	}
	if len(clusters) != 1 {
		t.Fatalf("len(clusters) = %d, want 1", len(clusters))
	}

	c := clusters[0]
	if c.GetName() != OfflineBackendClusterName {
		t.Errorf("name = %q, want %q", c.GetName(), OfflineBackendClusterName)
	}
	if c.GetType() != clusterv3.Cluster_STATIC {
		t.Errorf("type = %v, want STATIC", c.GetType())
	}
	// The empty endpoint list is the whole point: it is what makes Envoy fail
	// host selection and set the UH flag the offline page matches on.
	if eps := c.GetLoadAssignment().GetEndpoints(); len(eps) != 0 {
		t.Errorf("len(endpoints) = %d, want 0", len(eps))
	}

	again, added, err := EnsureOfflineCluster(clusters, OfflineBackendClusterName)
	if err != nil {
		t.Fatalf("EnsureOfflineCluster (second call): %v", err)
	}
	if added {
		t.Error("added = true on a set that already has the cluster, want false")
	}
	if len(again) != 1 {
		t.Errorf("len(clusters) = %d after second call, want 1", len(again))
	}
}

// The two sinks must stay distinct. A shared name would make the parity scanner
// count an idle backend as an offline tunnel.
func TestEnsureOfflineClusterKeepsTheTwoSinksApart(t *testing.T) {
	if OfflineBackendClusterName == OfflineTunnelClusterName {
		t.Fatal("the empty-backend and offline-tunnel sinks share a name")
	}

	clusters, _, err := EnsureOfflineCluster(nil, OfflineBackendClusterName)
	if err != nil {
		t.Fatalf("EnsureOfflineCluster: %v", err)
	}
	clusters, added, err := EnsureOfflineCluster(clusters, OfflineTunnelClusterName)
	if err != nil {
		t.Fatalf("EnsureOfflineCluster: %v", err)
	}
	if !added {
		t.Fatal("added = false for the second sink, want true")
	}
	if len(clusters) != 2 {
		t.Fatalf("len(clusters) = %d, want 2", len(clusters))
	}
}
