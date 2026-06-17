package mutate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

const (
	testInternalListener = "connector-tunnel"
	testDSNS             = "ns-test-uid"
	testUpstreamNS       = "test-project"
	testProxyName        = "smoke-proxy"
	testTargetHost       = "backend.example.com"
	testTargetPort       = 9000
	testNodeID           = "test-node-id-abc123"
)

// testClusterName returns the EG-assigned connector cluster name.
func testClusterName() string {
	return "httproute/" + testDSNS + "/" + testProxyName + "/rule/0"
}

// connectorPolicyIndex builds a PolicyIndex with a single connector entry.
func connectorPolicyIndex(online bool) *extcache.PolicyIndex {
	idx := &extcache.PolicyIndex{
		DStoUS: map[string]string{testDSNS: testUpstreamNS},
		TPPs:   make(map[string][]extcache.TPPInfo),
		Connectors: map[extcache.ConnectorKey]extcache.ConnectorInfo{
			{
				UpstreamNS:    testUpstreamNS,
				HTTPProxyName: testProxyName,
				RuleIndex:     0,
			}: {
				Online:     online,
				TargetHost: testTargetHost,
				TargetPort: testTargetPort,
				NodeID:     testNodeID,
			},
		},
	}
	return idx
}

// routeTargeting builds a Route that forwards to the given cluster.
func routeTargeting(cluster string) *routev3.Route {
	return &routev3.Route{
		Name: "fwd",
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{
					Cluster: cluster,
				},
			},
		},
	}
}

// --- ReplaceConnectorClusters tests ---

func TestReplaceConnectorClusters_OnlineConnector_ReplacesCluster(t *testing.T) {
	idx := connectorPolicyIndex(true)
	clusterName := testClusterName()

	clusters := []*clusterv3.Cluster{
		{Name: clusterName},
		{Name: "infra-cluster"}, // non-connector: must be untouched
	}

	replaced, offline, err := ReplaceConnectorClusters(clusters, idx, testInternalListener)
	require.NoError(t, err)

	// Only the connector cluster should be in the replaced map.
	assert.Len(t, replaced, 1, "one cluster should be replaced")
	assert.Contains(t, replaced, clusterName, "replaced map must contain connector cluster")
	assert.Empty(t, offline, "offline map must be empty for online connector")

	got := clusters[0]
	assert.Equal(t, clusterName, got.GetName(), "cluster name must be preserved after replacement")
	assert.Equal(t, clusterv3.Cluster_STATIC, got.GetType(), "replaced cluster must be STATIC type")

	// Verify internal-upstream endpoint wiring.
	eps := got.GetLoadAssignment().GetEndpoints()
	require.Len(t, eps, 1, "must have one endpoint locality")
	require.Len(t, eps[0].GetLbEndpoints(), 1, "must have one lb endpoint")

	addr := eps[0].GetLbEndpoints()[0].GetEndpoint().GetAddress().GetEnvoyInternalAddress()
	assert.Equal(t, testInternalListener, addr.GetServerListenerName(),
		"internal address must point at connector-tunnel listener")

	// Verify tunnel filter_metadata.
	tunnelMeta := eps[0].GetLbEndpoints()[0].GetMetadata().GetFilterMetadata()["tunnel"]
	require.NotNil(t, tunnelMeta, "lb endpoint must have tunnel filter_metadata")
	expectedAddr := testTargetHost + ":9000"
	assert.Equal(t, expectedAddr, tunnelMeta.GetFields()["address"].GetStringValue(),
		"tunnel address must be <host>:<port>")
	assert.Equal(t, testNodeID, tunnelMeta.GetFields()["endpoint_id"].GetStringValue(),
		"endpoint_id must match connector NodeID")

	// Verify transport socket.
	ts := got.GetTransportSocket()
	require.NotNil(t, ts, "replaced cluster must have transport_socket")
	assert.Equal(t, "envoy.transport_sockets.internal_upstream", ts.GetName(),
		"transport_socket must be internal_upstream")
	assert.NotNil(t, ts.GetTypedConfig(),
		"internal_upstream transport_socket must have typed_config")

	// Infra cluster must be completely untouched.
	assert.Equal(t, "infra-cluster", clusters[1].GetName())
	assert.Nil(t, clusters[1].GetTransportSocket(), "non-connector cluster must not get transport_socket")
	assert.Nil(t, clusters[1].GetLoadAssignment(), "non-connector cluster must not get load_assignment")
}

func TestReplaceConnectorClusters_OfflineConnector_ClusterUnchanged(t *testing.T) {
	idx := connectorPolicyIndex(false) // offline
	clusterName := testClusterName()

	original := &clusterv3.Cluster{Name: clusterName}
	clusters := []*clusterv3.Cluster{original}

	replaced, offline, err := ReplaceConnectorClusters(clusters, idx, testInternalListener)
	require.NoError(t, err)

	// Offline connector: goes into offline map, cluster is NOT replaced.
	assert.Empty(t, replaced, "replaced map must be empty for offline connector")
	assert.Len(t, offline, 1, "offline map must contain the connector cluster")
	assert.Contains(t, offline, clusterName)
	assert.Equal(t, testTargetHost, offline[clusterName].TargetHost,
		"offline ConnectorInfo must carry the target host")

	// The cluster itself must be unchanged (still the original object).
	assert.Equal(t, clusterName, clusters[0].GetName())
	assert.Equal(t, clusterv3.Cluster_STATIC, clusters[0].GetType(),
		"offline cluster type: proto zero-value is STATIC (0), the cluster was not replaced")
}

func TestReplaceConnectorClusters_NonConnectorCluster_Untouched(t *testing.T) {
	idx := connectorPolicyIndex(true)

	// This cluster does NOT match the httproute/<dsNS>/... pattern.
	clusters := []*clusterv3.Cluster{
		{Name: "grpc-backend"},
		{Name: "grpc-backend-2"},
	}

	replaced, offline, err := ReplaceConnectorClusters(clusters, idx, testInternalListener)
	require.NoError(t, err)

	assert.Empty(t, replaced, "non-connector clusters must not appear in replaced")
	assert.Empty(t, offline, "non-connector clusters must not appear in offline")
	assert.Equal(t, "grpc-backend", clusters[0].GetName())
	assert.Equal(t, "grpc-backend-2", clusters[1].GetName())
}

func TestReplaceConnectorClusters_ClusterNotInPolicyIndex_Skipped(t *testing.T) {
	// Cluster name matches the httproute pattern, but there is no ConnectorKey in idx.
	idx := &extcache.PolicyIndex{
		DStoUS:     map[string]string{testDSNS: testUpstreamNS},
		TPPs:       make(map[string][]extcache.TPPInfo),
		Connectors: make(map[extcache.ConnectorKey]extcache.ConnectorInfo), // empty
	}

	clusters := []*clusterv3.Cluster{
		{Name: testClusterName()}, // name matches pattern but no index entry
	}

	replaced, offline, err := ReplaceConnectorClusters(clusters, idx, testInternalListener)
	require.NoError(t, err)

	assert.Empty(t, replaced)
	assert.Empty(t, offline)
	// Cluster must not be mutated — it has no transport socket from an empty Cluster{}.
	assert.Nil(t, clusters[0].GetTransportSocket())
}

// --- ApplyConnectorRoutes tests ---

func TestApplyConnectorRoutes_Online_PrependsCONNECTRouteAndAppendsTargetHost(t *testing.T) {
	idx := connectorPolicyIndex(true)
	clusterName := testClusterName()

	// Simulate what ReplaceConnectorClusters produces for an online connector.
	info := &extcache.ConnectorInfo{
		Online:     true,
		TargetHost: testTargetHost,
		TargetPort: testTargetPort,
		NodeID:     testNodeID,
	}
	replaced := map[string]*extcache.ConnectorInfo{clusterName: info}
	offline := map[string]*extcache.ConnectorInfo{}

	rc := &routev3.RouteConfiguration{
		Name: "consumer-gw/smoke-gw/https",
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "vh",
				Domains: []string{"app.local.test"},
				Routes:  []*routev3.Route{routeTargeting(clusterName)},
			},
		},
	}

	n, err := ApplyConnectorRoutes(rc, idx, replaced, offline)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "one VH should be mutated")

	vh := rc.VirtualHosts[0]
	require.Len(t, vh.Routes, 2, "CONNECT route must be prepended; want 2 routes total")

	// First route must be the CONNECT upgrade route.
	connectRoute := vh.Routes[0]
	assert.NotNil(t, connectRoute.GetMatch().GetConnectMatcher(),
		"first route must have connect_matcher")
	ra := connectRoute.GetRoute()
	require.NotNil(t, ra, "CONNECT route must have route action")
	assert.Equal(t, clusterName, ra.GetCluster(),
		"CONNECT route must target the connector cluster")
	require.Len(t, ra.GetUpgradeConfigs(), 1, "CONNECT route must have one upgrade_config")
	assert.Equal(t, "CONNECT", ra.GetUpgradeConfigs()[0].GetUpgradeType(),
		"upgrade type must be CONNECT")

	// Original forwarding route must remain second.
	assert.Equal(t, "fwd", vh.Routes[1].GetName())

	// TargetHost (actual backend host) must be appended to VH domains.
	// Production uses info.TargetHost, NOT a synthetic .connector.local domain (design §2.3 C1).
	assert.Contains(t, vh.Domains, testTargetHost,
		"TargetHost must be appended to VH domains (not a .connector.local synthetic domain)")
}

func TestApplyConnectorRoutes_Online_TargetHostDeduplicated(t *testing.T) {
	idx := connectorPolicyIndex(true)
	clusterName := testClusterName()
	info := &extcache.ConnectorInfo{Online: true, TargetHost: testTargetHost, TargetPort: testTargetPort}
	replaced := map[string]*extcache.ConnectorInfo{clusterName: info}
	offline := map[string]*extcache.ConnectorInfo{}

	// VH already has the target host in its domains.
	rc := &routev3.RouteConfiguration{
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "vh",
				Domains: []string{"app.local.test", testTargetHost},
				Routes:  []*routev3.Route{routeTargeting(clusterName)},
			},
		},
	}

	_, err := ApplyConnectorRoutes(rc, idx, replaced, offline)
	require.NoError(t, err)

	// Domain must not be duplicated.
	count := 0
	for _, d := range rc.VirtualHosts[0].Domains {
		if d == testTargetHost {
			count++
		}
	}
	assert.Equal(t, 1, count, "target host must appear exactly once in VH domains")
}

func TestApplyConnectorRoutes_Offline_Prepends503Route_NoDomain(t *testing.T) {
	idx := connectorPolicyIndex(false)
	clusterName := testClusterName()

	// Simulate offline: cluster is in the offline map, not replaced.
	offlineInfo := &extcache.ConnectorInfo{
		Online:     false,
		TargetHost: testTargetHost,
		TargetPort: testTargetPort,
	}
	replaced := map[string]*extcache.ConnectorInfo{}
	offline := map[string]*extcache.ConnectorInfo{clusterName: offlineInfo}

	rc := &routev3.RouteConfiguration{
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "vh",
				Domains: []string{"app.local.test"},
				Routes:  []*routev3.Route{routeTargeting(clusterName)},
			},
		},
	}

	n, err := ApplyConnectorRoutes(rc, idx, replaced, offline)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "offline VH must be mutated (503 route prepended)")

	vh := rc.VirtualHosts[0]
	require.Len(t, vh.Routes, 2, "503 direct_response route must be prepended")

	offlineRoute := vh.Routes[0]
	dr := offlineRoute.GetDirectResponse()
	require.NotNil(t, dr, "first route must be a direct_response")
	assert.Equal(t, uint32(503), dr.GetStatus(),
		"offline route must return 503")
	assert.Equal(t, "Tunnel not online", dr.GetBody().GetInlineString(),
		"offline route body must be 'Tunnel not online' per STATE.md contract")

	// No domain must be appended for offline connectors.
	assert.NotContains(t, vh.Domains, testTargetHost,
		"target host must NOT be appended for offline connector")
	assert.Len(t, vh.Domains, 1, "domains must remain unchanged for offline connector")
}

func TestApplyConnectorRoutes_NoConnector_VHUntouched(t *testing.T) {
	idx := connectorPolicyIndex(true)

	// VH routes target an infra cluster, not a connector cluster.
	replaced := map[string]*extcache.ConnectorInfo{}
	offline := map[string]*extcache.ConnectorInfo{}

	rc := &routev3.RouteConfiguration{
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "vh",
				Domains: []string{"infra.local"},
				Routes:  []*routev3.Route{routeTargeting("infra-cluster")},
			},
		},
	}

	n, err := ApplyConnectorRoutes(rc, idx, replaced, offline)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "VH with non-connector cluster must not be mutated")
	assert.Len(t, rc.VirtualHosts[0].Routes, 1, "route count must not change")
	assert.Len(t, rc.VirtualHosts[0].Domains, 1, "domain list must not change")
}

func TestApplyConnectorRoutes_EmptyRouteConfiguration_NoOp(t *testing.T) {
	idx := connectorPolicyIndex(true)
	replaced := map[string]*extcache.ConnectorInfo{testClusterName(): {Online: true, TargetHost: testTargetHost}}
	offline := map[string]*extcache.ConnectorInfo{}

	rc := &routev3.RouteConfiguration{Name: "empty"}

	n, err := ApplyConnectorRoutes(rc, idx, replaced, offline)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
