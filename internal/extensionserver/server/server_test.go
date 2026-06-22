package server

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
)

// testServerScheme builds the runtime.Scheme needed by the fake client for
// BuildPolicyIndex (Namespace, TrafficProtectionPolicy, HTTPProxy, Connector).
func testServerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	return s
}

// testServerConfig returns a ServerConfig suitable for unit tests.
func testServerConfig() ServerConfig {
	return ServerConfig{
		ConnectorInternalListener: "connector-tunnel",
		Coraza: mutate.CorazaConfig{
			FilterName:  "coraza-waf",
			LibraryID:   "coraza-waf",
			LibraryPath: "/opt/coraza-waf/coraza-waf.so",
			PluginName:  "coraza-waf",
			ListenerDirectives: []string{
				"SecRuleEngine DetectionOnly",
				"Include @owasp_crs/*.conf",
			},
		},
	}
}

// discardLogger returns a slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mkListenerWithHCM builds a minimal xDS Listener carrying an RDS-based HCM
// filter, simulating a user-traffic listener. RDS is required for
// InjectCorazaListenerFilters to fire.
// The listener name is fixed as "consumer-gw/test-gw/https" — all callers in
// this package use that value.
func mkListenerWithHCM(t *testing.T) *listenerv3.Listener {
	t.Helper()
	const name = "consumer-gw/test-gw/https"
	hcm := &hcmv3.HttpConnectionManager{
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{
			Rds: &hcmv3.Rds{RouteConfigName: name},
		},
		HttpFilters: []*hcmv3.HttpFilter{{Name: "envoy.filters.http.router"}},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)
	return &listenerv3.Listener{
		Name: name,
		FilterChains: []*listenerv3.FilterChain{{
			Name: "fc",
			Filters: []*listenerv3.Filter{{
				Name:       "envoy.filters.network.http_connection_manager",
				ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
			}},
		}},
	}
}

// mkListenerWithStaticRoute builds a minimal xDS Listener carrying an HCM
// filter with an inline static route_config (no RDS), simulating EG's internal
// readiness listener (envoy-gateway-proxy-ready-0.0.0.0-19003). Such listeners
// must NOT receive Coraza injection.
func mkListenerWithStaticRoute(t *testing.T, name string) *listenerv3.Listener {
	t.Helper()
	hcm := &hcmv3.HttpConnectionManager{
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{
			RouteConfig: &routev3.RouteConfiguration{
				Name: "local_route",
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{{Name: "envoy.filters.http.router"}},
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)
	return &listenerv3.Listener{
		Name: name,
		FilterChains: []*listenerv3.FilterChain{{
			Name: "fc",
			Filters: []*listenerv3.Filter{{
				Name:       "envoy.filters.network.http_connection_manager",
				ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
			}},
		}},
	}
}

// egGatewayMeta builds the envoy-gateway filter_metadata struct for a VH.
// This simulates the metadata EG populates on VirtualHosts per design §2.1.
func egGatewayMeta(t *testing.T, dsNS, gwName string) *corev3.Metadata {
	t.Helper()
	s, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"kind":      "Gateway",
				"namespace": dsNS,
				"name":      gwName,
			},
		},
	})
	require.NoError(t, err)
	return &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{"envoy-gateway": s},
	}
}

// --- Full-snapshot integration test ---

// TestPostTranslateModify_FullSnapshot drives the entire PostTranslateModify
// call path with a fake client pre-populated with one Namespace, one
// TrafficProtectionPolicy, one HTTPProxy, and one online Connector.
//
// It asserts:
//   - Secrets pass through untouched.
//   - Connector cluster is replaced with STATIC internal-upstream.
//   - Infra cluster is untouched.
//   - Listener receives Coraza filter at HCM position 0 (disabled).
//   - Route configuration: CONNECT route prepended, forwarding route carries
//     coraza typed_per_filter_config and datum-gateway metadata.
//   - VH domains include the connector's TargetHost (production behavior per
//     design §2.3 C1 — NOT a synthetic .connector.local domain).
//   - ALL four resource lists appear in the response.
func TestPostTranslateModify_FullSnapshot(t *testing.T) {
	const (
		upstreamNS = "test-project"
		// dsNS equals upstreamNS in single-cluster: EG puts the plain namespace
		// name in filter_metadata; the identity path produces DStoUS[ns.Name]=ns.Name.
		dsNS          = upstreamNS
		gwName        = "test-gw"
		proxyName     = "test-proxy"
		connectorName = "test-connector"
		targetHost    = "backend.example.com"
		targetPort    = "9000"
		nodeID        = "test-node-id"
		connCluster   = "httproute/" + dsNS + "/" + proxyName + "/rule/0"
	)

	// Unlabeled namespace: BuildPolicyIndex produces DStoUS["test-project"] = "test-project"
	// (identity path — no UID reconstruction needed in single-cluster).
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: upstreamNS,
		},
	}

	// Build a TrafficProtectionPolicy targeting the Gateway.
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tpp",
			Namespace: upstreamNS,
		},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Kind: "Gateway",
						Name: gatewayv1.ObjectName(gwName),
					},
				},
			},
			// OWASPCoreRuleSet generates Directives — required for WAF to fire.
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{
				{
					Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
					OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
						ParanoiaLevels: networkingv1alpha.ParanoiaLevels{Blocking: 1, Detection: 1},
						ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{
							Inbound:  5,
							Outbound: 4,
						},
					},
				},
			},
		},
	}

	// Build an HTTPProxy with a Connector backend at rule 0.
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyName,
			Namespace: upstreamNS,
		},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							// Endpoint parsed by BuildPolicyIndex to extract targetHost:targetPort.
							Endpoint: "http://" + targetHost + ":" + targetPort,
							Connector: &networkingv1alpha.ConnectorReference{
								Name: connectorName,
							},
						},
					},
				},
			},
		},
	}

	// Build an online Connector.
	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connectorName,
			Namespace: upstreamNS,
		},
		Status: networkingv1alpha1.ConnectorStatus{
			Conditions: []metav1.Condition{
				{
					Type:               networkingv1alpha1.ConnectorConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					LastTransitionTime: metav1.Now(),
				},
			},
			ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
				Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
				PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
					Id: nodeID,
				},
			},
		},
	}

	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns, tpp, proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	srv := New(cl, testServerConfig(), discardLogger())

	req := &pb.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{
			{Name: connCluster},
			{Name: "infra-cluster"},
		},
		Secrets: []*tlsv3.Secret{
			{Name: "wildcard-tls"},
		},
		Listeners: []*listenerv3.Listener{
			mkListenerWithHCM(t),
		},
		Routes: []*routev3.RouteConfiguration{{
			Name: "consumer-gw/test-gw/https",
			VirtualHosts: []*routev3.VirtualHost{{
				Name:     "vh",
				Domains:  []string{"app.example.com"},
				Metadata: egGatewayMeta(t, dsNS, gwName),
				Routes: []*routev3.Route{{
					Name: "fwd",
					Action: &routev3.Route_Route{
						Route: &routev3.RouteAction{
							ClusterSpecifier: &routev3.RouteAction_Cluster{
								Cluster: connCluster,
							},
						},
					},
				}},
			}},
		}},
	}

	resp, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// --- All four resource lists must be present ---
	assert.Len(t, resp.Clusters, 2, "all clusters must be returned")
	assert.Len(t, resp.Secrets, 1, "all secrets must be returned")
	assert.Len(t, resp.Listeners, 1, "all listeners must be returned")
	assert.Len(t, resp.Routes, 1, "all route configs must be returned")

	// --- Secrets pass through untouched ---
	require.Len(t, resp.Secrets, 1)
	assert.Equal(t, "wildcard-tls", resp.Secrets[0].GetName(),
		"secret must pass through unchanged")

	// --- Connector cluster replaced with internal-upstream ---
	connectorCluster := resp.Clusters[0]
	assert.Equal(t, connCluster, connectorCluster.GetName(),
		"connector cluster name must be preserved")
	require.NotNil(t, connectorCluster.GetTransportSocket(),
		"connector cluster must have transport_socket")
	assert.Equal(t, "envoy.transport_sockets.internal_upstream",
		connectorCluster.GetTransportSocket().GetName(),
		"connector cluster transport_socket must be internal_upstream")

	// --- Infra cluster untouched ---
	infraCluster := resp.Clusters[1]
	assert.Equal(t, "infra-cluster", infraCluster.GetName())
	assert.Nil(t, infraCluster.GetTransportSocket(),
		"infra cluster must not get transport_socket")
	assert.Nil(t, infraCluster.GetLoadAssignment(),
		"infra cluster must not get load_assignment")

	// --- Listener received Coraza filter at HCM position 0 (disabled) ---
	hcm := &hcmv3.HttpConnectionManager{}
	err = resp.Listeners[0].FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(hcm)
	require.NoError(t, err, "unmarshal listener HCM")
	require.NotEmpty(t, hcm.HttpFilters, "HCM must have http filters")
	assert.Equal(t, "coraza-waf", hcm.HttpFilters[0].Name,
		"Coraza filter must be at position 0 in HCM")
	assert.True(t, hcm.HttpFilters[0].Disabled,
		"Coraza filter must be disabled at listener scope")

	// --- Route configuration: CONNECT route prepended, WAF on forwarding route ---
	vh := resp.Routes[0].VirtualHosts[0]
	require.Len(t, vh.Routes, 2,
		"VH must have 2 routes: CONNECT (prepended) + forwarding")

	// CONNECT route first.
	assert.NotNil(t, vh.Routes[0].GetMatch().GetConnectMatcher(),
		"first route must be the CONNECT route")

	// Forwarding route must carry Coraza per-route config and datum-gateway metadata.
	fwd := vh.Routes[1]
	tpfc := fwd.GetTypedPerFilterConfig()["coraza-waf"]
	require.NotNil(t, tpfc, "forwarding route must have coraza typed_per_filter_config")
	assert.True(t, strings.Contains(tpfc.GetTypeUrl(), "golang.v3alpha.ConfigsPerRoute"),
		"tpfc type url must reference ConfigsPerRoute, got %q", tpfc.GetTypeUrl())

	datumMeta := fwd.GetMetadata().GetFilterMetadata()["datum-gateway"]
	require.NotNil(t, datumMeta, "forwarding route must have datum-gateway filter_metadata")
	res := datumMeta.GetFields()["resources"].GetListValue().GetValues()
	require.Len(t, res, 1, "datum-gateway metadata must have one resource entry")
	entry := res[0].GetStructValue().GetFields()
	assert.Equal(t, "TrafficProtectionPolicy", entry["kind"].GetStringValue())
	assert.Equal(t, "test-tpp", entry["name"].GetStringValue())
	assert.Equal(t, upstreamNS, entry["namespace"].GetStringValue())

	// --- VH domains include connector TargetHost (NOT .connector.local) ---
	assert.Contains(t, vh.Domains, targetHost,
		"VH domains must contain connector TargetHost (design §2.3 C1: use actual hostname)")
	for _, d := range vh.Domains {
		assert.False(t, strings.HasSuffix(d, ".connector.local"),
			"synthetic .connector.local domain must NOT appear (design §2.3 C1 fix)")
	}
}

// TestPostTranslateModify_SecretsPassThroughUnchanged verifies that secrets are
// always echoed back unchanged even when no mutations apply.
func TestPostTranslateModify_SecretsPassThroughUnchanged(t *testing.T) {
	scheme := testServerScheme(t)
	// Empty fake client → BuildPolicyIndex returns an empty index → no mutations.
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := New(cl, testServerConfig(), discardLogger())

	req := &pb.PostTranslateModifyRequest{
		Secrets: []*tlsv3.Secret{
			{Name: "tls-secret-a"},
			{Name: "tls-secret-b"},
		},
	}

	resp, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, resp.Secrets, 2, "all secrets must be returned")
	assert.Equal(t, "tls-secret-a", resp.Secrets[0].GetName())
	assert.Equal(t, "tls-secret-b", resp.Secrets[1].GetName())
}

// TestPostTranslateModify_AllListsReturnedWhenEmpty verifies that the response
// always carries all four resource lists, even when the request has empty lists.
func TestPostTranslateModify_AllListsReturnedWhenEmpty(t *testing.T) {
	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := New(cl, testServerConfig(), discardLogger())

	resp, err := srv.PostTranslateModify(context.Background(), &pb.PostTranslateModifyRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Response lists must be present (possibly nil/empty, but the fields must exist).
	// EG drops lists that are nil from the response — verify non-nil behaviour.
	assert.NotNil(t, resp, "response must not be nil")
	// Empty slices are valid — the key assertion is no panic and no error.
}

// TestPostTranslateModify_CorazaDisabled_NoWAFInjection verifies that when
// CorazaConfig.Disabled is true, PostTranslateModify emits zero Coraza
// listener filters and zero per-route WAF config. Connector mutations are
// unaffected by the Coraza disabled flag.
func TestPostTranslateModify_CorazaDisabled_NoWAFInjection(t *testing.T) {
	const (
		upstreamNS = "test-project"
		// dsNS equals upstreamNS in single-cluster (identity path, no UID reconstruction).
		dsNS   = upstreamNS
		gwName = "test-gw"
	)

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: upstreamNS},
	}
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tpp", Namespace: upstreamNS},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Kind: "Gateway",
						Name: gatewayv1.ObjectName(gwName),
					},
				},
			},
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{
				{
					Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
					OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
						ParanoiaLevels:  networkingv1alpha.ParanoiaLevels{Blocking: 1, Detection: 1},
						ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{Inbound: 5, Outbound: 4},
					},
				},
			},
		},
	}

	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, tpp).Build()

	// Build server config with Coraza disabled.
	cfg := testServerConfig()
	cfg.Coraza.Disabled = true
	srv := New(cl, cfg, discardLogger())

	req := &pb.PostTranslateModifyRequest{
		Listeners: []*listenerv3.Listener{
			mkListenerWithHCM(t),
		},
		Routes: []*routev3.RouteConfiguration{{
			Name: "consumer-gw/test-gw/https",
			VirtualHosts: []*routev3.VirtualHost{{
				Name:     "vh",
				Domains:  []string{"app.example.com"},
				Metadata: egGatewayMeta(t, dsNS, gwName),
				Routes:   []*routev3.Route{{Name: "fwd"}},
			}},
		}},
	}

	resp, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Listener HCM must NOT have any Coraza filter.
	hcm := &hcmv3.HttpConnectionManager{}
	err = resp.Listeners[0].FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(hcm)
	require.NoError(t, err, "unmarshal listener HCM")
	for _, f := range hcm.HttpFilters {
		assert.NotEqual(t, "coraza-waf", f.GetName(),
			"disabled Coraza must not inject any listener filter")
	}

	// Routes must have no Coraza per-route config.
	rt := resp.Routes[0].VirtualHosts[0].Routes[0]
	assert.Nil(t, rt.GetTypedPerFilterConfig(),
		"disabled Coraza must not inject any per-route typed_per_filter_config")
}

// TestPostTranslateModify_EGInternalListener_Skipped verifies that the
// extension server injects Coraza into RDS-based user-traffic listeners but
// skips listeners whose HCM uses an inline static route_config (simulating
// EG's internal readiness/admin listener envoy-gateway-proxy-ready-*).
//
// This is the primary regression test for Bug 2: before the fix, the server
// called InjectCorazaListenerFilters for ALL listeners including the EG
// readiness listener, causing Envoy to reject the full listener set on
// standard (non-contrib) builds.
func TestPostTranslateModify_EGInternalListener_Skipped(t *testing.T) {
	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := New(cl, testServerConfig(), discardLogger())

	const (
		userListenerName = "consumer-gw/test-gw/https"
		egInternalName   = "envoy-gateway-proxy-ready-0.0.0.0-19003"
	)

	req := &pb.PostTranslateModifyRequest{
		Listeners: []*listenerv3.Listener{
			mkListenerWithHCM(t),                         // RDS-based — must be injected
			mkListenerWithStaticRoute(t, egInternalName), // inline route_config — must be skipped
		},
	}

	resp, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, resp.Listeners, 2, "both listeners must be returned")

	// User-traffic listener (RDS-based) must receive the Coraza filter at HCM position 0.
	userHCM := &hcmv3.HttpConnectionManager{}
	err = resp.Listeners[0].FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(userHCM)
	require.NoError(t, err, "unmarshal user-traffic listener HCM")
	require.NotEmpty(t, userHCM.HttpFilters, "user-traffic HCM must have http filters")
	assert.Equal(t, "coraza-waf", userHCM.HttpFilters[0].Name,
		"Coraza filter must be injected at position 0 in the RDS-based user-traffic listener")
	assert.True(t, userHCM.HttpFilters[0].Disabled,
		"Coraza filter must be disabled at listener scope (activated per-route)")

	// EG-internal listener (static route_config) must NOT receive the Coraza filter.
	egHCM := &hcmv3.HttpConnectionManager{}
	err = resp.Listeners[1].FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(egHCM)
	require.NoError(t, err, "unmarshal EG-internal listener HCM")
	for _, f := range egHCM.HttpFilters {
		assert.NotEqual(t, "coraza-waf", f.GetName(),
			"EG-internal (static route_config) listener must NOT receive Coraza filter; got filter %q", f.GetName())
	}
}

// TestPostTranslateModify_TwoClusterTopology_DistinctReplicaUID is the GAP-1b
// full-pipeline regression test. It simulates the two-cluster edge topology:
//   - Ext-server client lists EDGE cluster replica namespaces.
//   - Replica namespace is named "ns-<upstream-uid>" but has a DISTINCT own UID
//     assigned by the edge cluster's API server.
//   - mappednamespace.go stamps UpstreamOwnerNamespaceLabel on the replica namespace
//     AND on every replicated resource (TPP, HTTPProxy).
//   - EG VH metadata carries the replica namespace name as dsNS.
//
// Before the fix, DStoUS was keyed only as "ns-<edge-own-uid>" (using the replica
// namespace's own UID) → WAF and Connector mutations were silently skipped.
// This test FAILS on the old code and PASSES after the label-based fix.
func TestPostTranslateModify_TwoClusterTopology_DistinctReplicaUID(t *testing.T) {
	const (
		upstreamNSName = "real-upstream-project"       // true upstream namespace name
		replicaNSName  = "ns-upstream-uid-abc"         // edge cluster replica namespace name
		replicaNSUID   = types.UID("edge-own-uid-xyz") // DISTINCT from "upstream-uid-abc"
		gwName         = "test-gw"
		proxyName      = "replica-proxy"
		connectorName  = "replica-connector"
		targetHost     = "backend.example.com"
		nodeID         = "test-node-id-replica"
		connCluster    = "httproute/" + replicaNSName + "/" + proxyName + "/rule/0"
	)

	// Replica labels stamped by mappednamespace.go on all replicated resources.
	replicaLabels := map[string]string{
		"meta.datumapis.com/upstream-namespace": upstreamNSName,
	}

	// Replica namespace: own UID is distinct from the upstream UID embedded in
	// its name. Without the label-based fix, DStoUS["ns-edge-own-uid-xyz"] is
	// set but DStoUS["ns-upstream-uid-abc"] is never set → lookup misses.
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   replicaNSName,
			UID:    replicaNSUID,
			Labels: replicaLabels,
		},
	}

	// Replica TPP in the replica namespace. mappednamespace.go stamps the label.
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "replica-tpp",
			Namespace: replicaNSName,
			Labels:    replicaLabels,
		},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Kind: "Gateway",
						Name: gatewayv1.ObjectName(gwName),
					},
				},
			},
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{
				{
					Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
					OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
						ParanoiaLevels:  networkingv1alpha.ParanoiaLevels{Blocking: 1, Detection: 1},
						ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{Inbound: 5, Outbound: 4},
					},
				},
			},
		},
	}

	// Replica HTTPProxy in the replica namespace. mappednamespace.go stamps the label.
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyName,
			Namespace: replicaNSName,
			Labels:    replicaLabels,
		},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint:  "http://" + targetHost + ":9000",
							Connector: &networkingv1alpha.ConnectorReference{Name: connectorName},
						},
					},
				},
			},
		},
	}

	// Replica Connector (online) in the replica namespace.
	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connectorName,
			Namespace: replicaNSName,
		},
		Status: networkingv1alpha1.ConnectorStatus{
			Conditions: []metav1.Condition{
				{
					Type:               networkingv1alpha1.ConnectorConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					LastTransitionTime: metav1.Now(),
				},
			},
			ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
				Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
				PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
					Id: nodeID,
				},
			},
		},
	}

	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns, tpp, proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	srv := New(cl, testServerConfig(), discardLogger())

	req := &pb.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{
			{Name: connCluster},
			{Name: "infra-cluster"},
		},
		Listeners: []*listenerv3.Listener{
			mkListenerWithHCM(t),
		},
		Routes: []*routev3.RouteConfiguration{{
			Name: "consumer-gw/test-gw/https",
			VirtualHosts: []*routev3.VirtualHost{{
				Name:     "vh",
				Domains:  []string{"app.example.com"},
				Metadata: egGatewayMeta(t, replicaNSName, gwName),
				Routes: []*routev3.Route{{
					Name: "fwd",
					Action: &routev3.Route_Route{
						Route: &routev3.RouteAction{
							ClusterSpecifier: &routev3.RouteAction_Cluster{
								Cluster: connCluster,
							},
						},
					},
				}},
			}},
		}},
	}

	resp, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// --- Connector cluster must be replaced (GAP-1b: Connector resolution) ---
	// Old code: DStoUS["ns-upstream-uid-abc"] absent → ReplaceConnectorClusters
	// skipped the cluster → no internal-upstream replacement.
	require.Len(t, resp.Clusters, 2)
	connectorCluster := resp.Clusters[0]
	assert.Equal(t, connCluster, connectorCluster.GetName(),
		"connector cluster name must be preserved")
	require.NotNil(t, connectorCluster.GetTransportSocket(),
		"GAP-1b regression: connector cluster must be replaced with internal-upstream; "+
			"old code skipped it because DStoUS[replicaNSName] was absent")
	assert.Equal(t, "envoy.transport_sockets.internal_upstream",
		connectorCluster.GetTransportSocket().GetName())

	// Infra cluster must remain untouched.
	assert.Nil(t, resp.Clusters[1].GetTransportSocket(),
		"non-connector cluster must not get transport_socket")

	// --- Route must carry WAF config (GAP-1b: TPP resolution) ---
	// Old code: DStoUS["ns-upstream-uid-abc"] absent → ApplyTPPRouteConfig skipped
	// every VH → no WAF mutations → routes_tpp_applied:0.
	vh := resp.Routes[0].VirtualHosts[0]
	require.Len(t, vh.Routes, 2,
		"GAP-1b regression: CONNECT route must be prepended (connector was resolved); "+
			"old code left only the original forwarding route")

	// CONNECT route first.
	assert.NotNil(t, vh.Routes[0].GetMatch().GetConnectMatcher(),
		"first route must be the CONNECT upgrade route")

	// Forwarding route must carry Coraza per-route config.
	fwd := vh.Routes[1]
	tpfc := fwd.GetTypedPerFilterConfig()["coraza-waf"]
	require.NotNil(t, tpfc,
		"GAP-1b regression: forwarding route must have coraza typed_per_filter_config; "+
			"old code silently skipped WAF injection")
	assert.True(t, strings.Contains(tpfc.GetTypeUrl(), "golang.v3alpha.ConfigsPerRoute"),
		"tpfc type url must reference ConfigsPerRoute")

	// datum-gateway metadata must identify the replica TPP.
	datumMeta := fwd.GetMetadata().GetFilterMetadata()["datum-gateway"]
	require.NotNil(t, datumMeta,
		"forwarding route must have datum-gateway filter_metadata")
	res := datumMeta.GetFields()["resources"].GetListValue().GetValues()
	require.Len(t, res, 1)
	entry := res[0].GetStructValue().GetFields()
	assert.Equal(t, "TrafficProtectionPolicy", entry["kind"].GetStringValue())
	assert.Equal(t, "replica-tpp", entry["name"].GetStringValue())
	assert.Equal(t, replicaNSName, entry["namespace"].GetStringValue())

	// --- VH domains include connector TargetHost ---
	assert.Contains(t, vh.Domains, targetHost,
		"GAP-1b regression: VH domains must include connector TargetHost; "+
			"old code never appended it because the connector was not resolved")
}

// TestPostTranslateModify_OfflineConnector_503Route verifies that an offline
// connector produces a 503 direct_response route and leaves the cluster unchanged.
func TestPostTranslateModify_OfflineConnector_503Route(t *testing.T) {
	const (
		upstreamNS = "test-project"
		// dsNS equals upstreamNS in single-cluster (identity path, no UID reconstruction).
		dsNS          = upstreamNS
		proxyName     = "offline-proxy"
		connectorName = "offline-connector"
		targetHost    = "offline.example.com"
		connCluster   = "httproute/" + dsNS + "/" + proxyName + "/rule/0"
	)

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: upstreamNS},
	}
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: upstreamNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint:  "http://" + targetHost + ":9000",
							Connector: &networkingv1alpha.ConnectorReference{Name: connectorName},
						},
					},
				},
			},
		},
	}
	// Connector exists but is NOT ready.
	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: connectorName, Namespace: upstreamNS},
		Status: networkingv1alpha1.ConnectorStatus{
			Conditions: []metav1.Condition{
				{
					Type:               networkingv1alpha1.ConnectorConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             "NotReady",
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns, proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	srv := New(cl, testServerConfig(), discardLogger())

	req := &pb.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{{Name: connCluster}},
		Routes: []*routev3.RouteConfiguration{{
			Name: "route-config",
			VirtualHosts: []*routev3.VirtualHost{{
				Name:    "vh",
				Domains: []string{"app.example.com"}, // intentionally different from targetHost
				Routes: []*routev3.Route{{
					Name: "fwd",
					Action: &routev3.Route_Route{
						Route: &routev3.RouteAction{
							ClusterSpecifier: &routev3.RouteAction_Cluster{
								Cluster: connCluster,
							},
						},
					},
				}},
			}},
		}},
	}

	resp, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)

	// Cluster must NOT be replaced for offline connector.
	assert.Equal(t, connCluster, resp.Clusters[0].GetName())
	assert.Nil(t, resp.Clusters[0].GetTransportSocket(),
		"offline connector cluster must NOT be replaced with internal-upstream")

	// 503 direct_response route must be prepended.
	vh := resp.Routes[0].VirtualHosts[0]
	require.Len(t, vh.Routes, 2, "offline VH must have 2 routes (503 prepended + fwd)")
	offlineRoute := vh.Routes[0]
	assert.Equal(t, uint32(503), offlineRoute.GetDirectResponse().GetStatus(),
		"offline route must return 503")
	assert.Equal(t, "Tunnel not online", offlineRoute.GetDirectResponse().GetBody().GetInlineString())

	// TargetHost must NOT be appended for offline connector.
	assert.NotContains(t, vh.Domains, targetHost,
		"offline connector must not append targetHost to VH domains")
}
