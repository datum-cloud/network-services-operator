package parity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	internalupstreamv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/internal_upstream/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func mustAny(t *testing.T, m proto.Message) *anypb.Any {
	t.Helper()
	a, err := anypb.New(m)
	require.NoError(t, err)
	return a
}

// buildSyntheticDump builds a proxy configuration that looks like the real
// thing, so parsing and scanning can be exercised without a live cluster.
func buildSyntheticDump(t *testing.T, corazaFilter string) []byte {
	t.Helper()

	// --- WAF-governed route + CONNECT route ---
	wafMeta, err := structpb.NewStruct(map[string]any{
		"resources": []any{map[string]any{
			"kind": "TrafficProtectionPolicy", "namespace": "proj-ns", "name": "test-tpp", "mode": "Observe",
		}},
	})
	require.NoError(t, err)
	rc := &routev3.RouteConfiguration{
		Name: "gw/https",
		VirtualHosts: []*routev3.VirtualHost{{
			Name:    "vh",
			Domains: []string{"app.example.com", "vh.connector.internal"},
			Routes: []*routev3.Route{
				{
					Name: "connector-connect-vh",
					Action: &routev3.Route_Route{Route: &routev3.RouteAction{
						UpgradeConfigs: []*routev3.RouteAction_UpgradeConfig{{UpgradeType: "CONNECT"}},
					}},
				},
				{
					Name:     "fwd",
					Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{datumGatewayMetaKey: wafMeta}},
				},
			},
		}},
	}

	// --- replaced connector cluster ---
	ts := mustAny(t, &internalupstreamv3.InternalUpstreamTransport{})
	cl := &clusterv3.Cluster{
		Name:                 "httproute/proj-ns/proxy/rule/0",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
		LoadAssignment:       &endpointv3.ClusterLoadAssignment{ClusterName: "httproute/proj-ns/proxy/rule/0"},
		TransportSocket: &corev3.TransportSocket{
			Name:       connectorInternalTransport,
			ConfigType: &corev3.TransportSocket_TypedConfig{TypedConfig: ts},
		},
	}
	infraCl := &clusterv3.Cluster{
		Name:                 "infra",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS},
	}

	// --- listener with the firewall and a custom error page ---
	hcm := &hcmv3.HttpConnectionManager{
		RouteSpecifier:   &hcmv3.HttpConnectionManager_Rds{Rds: &hcmv3.Rds{RouteConfigName: "gw/https"}},
		HttpFilters:      []*hcmv3.HttpFilter{{Name: corazaFilter, Disabled: true}, {Name: "envoy.filters.http.router"}},
		LocalReplyConfig: &hcmv3.LocalReplyConfig{},
	}
	l := &listenerv3.Listener{
		Name: "gw/https",
		FilterChains: []*listenerv3.FilterChain{{
			Name: "fc",
			Filters: []*listenerv3.Filter{{
				Name:       hcmNetworkFilterName,
				ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustAny(t, hcm)},
			}},
		}},
	}

	dump := &adminv3.ConfigDump{
		Configs: []*anypb.Any{
			mustAny(t, &adminv3.ClustersConfigDump{
				DynamicActiveClusters: []*adminv3.ClustersConfigDump_DynamicCluster{
					{Cluster: mustAny(t, cl)},
					{Cluster: mustAny(t, infraCl)},
				},
			}),
			mustAny(t, &adminv3.RoutesConfigDump{
				DynamicRouteConfigs: []*adminv3.RoutesConfigDump_DynamicRouteConfig{{RouteConfig: mustAny(t, rc)}},
			}),
			mustAny(t, &adminv3.ListenersConfigDump{
				DynamicListeners: []*adminv3.ListenersConfigDump_DynamicListener{
					{Name: "gw/https", ActiveState: &adminv3.ListenersConfigDump_DynamicListenerState{Listener: mustAny(t, l)}},
				},
			}),
			mustAny(t, &adminv3.SecretsConfigDump{
				DynamicActiveSecrets: []*adminv3.SecretsConfigDump_DynamicSecret{{Name: "good-cert"}},
			}),
		},
	}

	raw, err := protojson.Marshal(dump)
	require.NoError(t, err)
	return raw
}

func TestParseAndScan_EndToEnd(t *testing.T) {
	const corazaFilter = "coraza-waf"
	raw := buildSyntheticDump(t, corazaFilter)

	dump, err := ParseConfigDump(raw)
	require.NoError(t, err)

	assert.Len(t, dump.Clusters, 2)
	assert.Len(t, dump.Routes, 1)
	assert.Len(t, dump.Listeners, 1)
	assert.Equal(t, []string{"good-cert"}, dump.SecretNames)

	act := ScanActual(dump, corazaFilter, nil)
	assert.Equal(t, []string{wafRouteKey("gw/https", "vh", "fwd", "proj-ns", "test-tpp", "Observe")},
		normalize(act.Keys[FamilyWAFRoute]))
	assert.Equal(t, []string{"httproute/proj-ns/proxy/rule/0"}, normalize(act.Keys[FamilyConnectorCluster]))
	assert.Equal(t, []string{connectorRouteKey("gw/https", "vh", "connector-connect-vh")},
		normalize(act.Keys[FamilyConnectorRoute]))
	assert.Equal(t, []string{listenerChainKey("gw/https", "fc")}, normalize(act.Keys[FamilyWAFHCM]))
	assert.Equal(t, []string{listenerChainKey("gw/https", "fc")}, normalize(act.Keys[FamilyLocalReply]))
}

// TestParseAndScan_FullParity is the integration shape: the same key formats are
// produced by the (simulated) ext-server side and the dump side, so Compare
// returns OK — proving the two sides are in lockstep.
func TestParseAndScan_FullParity(t *testing.T) {
	const corazaFilter = "coraza-waf"
	raw := buildSyntheticDump(t, corazaFilter)
	dump, err := ParseConfigDump(raw)
	require.NoError(t, err)
	act := ScanActual(dump, corazaFilter, nil)

	// Expected derived from the same artifact set (as the programmed-set endpoint
	// would report it).
	expected := exp(map[Family][]string{
		FamilyWAFRoute:         {wafRouteKey("gw/https", "vh", "fwd", "proj-ns", "test-tpp", "Observe")},
		FamilyConnectorCluster: {"httproute/proj-ns/proxy/rule/0"},
		FamilyConnectorRoute:   {connectorRouteKey("gw/https", "vh", "connector-connect-vh")},
		FamilyWAFHCM:           {listenerChainKey("gw/https", "fc")},
		FamilyLocalReply:       {listenerChainKey("gw/https", "fc")},
	})

	rep := Compare(expected, act)
	rep.AssertOK(t)
}

// TestParseConfigDump_NACKErrorState covers a single resource the proxy
// rejected while keeping the rest.
func TestParseConfigDump_NACKErrorState(t *testing.T) {
	dump := &adminv3.ConfigDump{Configs: []*anypb.Any{
		mustAny(t, &adminv3.ListenersConfigDump{
			DynamicListeners: []*adminv3.ListenersConfigDump_DynamicListener{
				{Name: "bad", ErrorState: &adminv3.UpdateFailureState{Details: "KEY_VALUES_MISMATCH"}},
			},
		}),
	}}
	raw, err := protojson.Marshal(dump)
	require.NoError(t, err)

	parsed, err := ParseConfigDump(raw)
	require.NoError(t, err)
	require.Contains(t, parsed.ErrorStates, "listener")
	assert.Contains(t, parsed.ErrorStates["listener"][0], "KEY_VALUES_MISMATCH")
}
