package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	internalupstreamv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/internal_upstream/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// --- buildProgrammedSet (pure function) ---

// mkWAFRoute builds a route already carrying the datum-gateway metadata and a
// coraza typed_per_filter_config, simulating a route after ApplyTPPRouteConfig.
func mkWAFRoute(t *testing.T, tppNS, tppName string) *routev3.Route {
	t.Helper()
	meta, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"kind":      "TrafficProtectionPolicy",
				"namespace": tppNS,
				"name":      tppName,
				"mode":      "Observe",
			},
		},
	})
	require.NoError(t, err)
	return &routev3.Route{
		Name: "fwd",
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{datumGatewayMetaKey: meta},
		},
	}
}

// mkCorazaHCMListener builds a listener whose HCM has the coraza filter at
// position 0 and a local_reply_config, simulating a fully-mutated listener.
func mkCorazaHCMListener(t *testing.T, name, corazaFilter string, withLocalReply bool) *listenerv3.Listener {
	t.Helper()
	hcm := &hcmv3.HttpConnectionManager{
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{Rds: &hcmv3.Rds{RouteConfigName: name}},
		HttpFilters: []*hcmv3.HttpFilter{
			{Name: corazaFilter, Disabled: true},
			{Name: "envoy.filters.http.router"},
		},
	}
	if withLocalReply {
		hcm.LocalReplyConfig = &hcmv3.LocalReplyConfig{}
	}
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)
	return &listenerv3.Listener{
		Name: name,
		FilterChains: []*listenerv3.FilterChain{{
			Name: "fc",
			Filters: []*listenerv3.Filter{{
				Name:       hcmNetworkFilterName,
				ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
			}},
		}},
	}
}

// mkReplacedConnectorCluster builds a STATIC internal-upstream cluster matching
// what buildConnectorCluster produces.
func mkReplacedConnectorCluster(t *testing.T, name string) *clusterv3.Cluster {
	t.Helper()
	ts, err := anypb.New(&internalupstreamv3.InternalUpstreamTransport{})
	require.NoError(t, err)
	return &clusterv3.Cluster{
		Name:                 name,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
		LoadAssignment:       &endpointv3.ClusterLoadAssignment{ClusterName: name},
		TransportSocket: &corev3.TransportSocket{
			Name:       connectorInternalTransport,
			ConfigType: &corev3.TransportSocket_TypedConfig{TypedConfig: ts},
		},
	}
}

func TestBuildProgrammedSet_AllFamilies(t *testing.T) {
	const corazaFilter = "coraza-waf"

	listeners := []*listenerv3.Listener{
		mkCorazaHCMListener(t, "gw/https", corazaFilter, true),
	}
	routes := []*routev3.RouteConfiguration{{
		Name: "gw/https",
		VirtualHosts: []*routev3.VirtualHost{{
			Name: "vh",
			Routes: []*routev3.Route{
				// online CONNECT route
				{
					Name: "connector-connect-vh",
					Action: &routev3.Route_Route{Route: &routev3.RouteAction{
						UpgradeConfigs: []*routev3.RouteAction_UpgradeConfig{{UpgradeType: "CONNECT"}},
					}},
				},
				// WAF-governed forwarding route
				mkWAFRoute(t, "proj-ns", "test-tpp"),
			},
		}},
	}}
	clusters := []*clusterv3.Cluster{
		mkReplacedConnectorCluster(t, "httproute/proj-ns/proxy/rule/0"),
		{Name: "infra-cluster"}, // not a connector cluster
	}

	ps := buildProgrammedSet(listeners, routes, clusters, corazaFilter, 2, 1, 0)

	assert.Equal(t, []string{wafRouteKey("gw/https", "vh", "fwd", "proj-ns", "test-tpp", "Observe")},
		ps.Keys[FamilyWAFRoute])
	assert.Equal(t, 1, ps.Counts[FamilyWAFRoute])

	assert.Equal(t, []string{listenerChainKey("gw/https", "fc")}, ps.Keys[FamilyWAFHCM])
	assert.Equal(t, []string{listenerChainKey("gw/https", "fc")}, ps.Keys[FamilyLocalReply])

	assert.Equal(t, []string{"httproute/proj-ns/proxy/rule/0"}, ps.Keys[FamilyConnectorCluster])
	assert.Equal(t, []string{connectorRouteKey("gw/https", "vh", "connector-connect-vh")},
		ps.Keys[FamilyConnectorRoute])

	// TLS prune outcome carried directly.
	assert.Equal(t, 2, ps.TLSPrunedChains)
	assert.Equal(t, 1, ps.TLSPrunedSecrets)
	assert.Equal(t, 0, ps.TLSListenersLeftIntact)
	assert.Equal(t, 2, ps.Counts[FamilyTLSPrune])
}

func TestBuildProgrammedSet_OfflineConnector(t *testing.T) {
	routes := []*routev3.RouteConfiguration{{
		Name: "gw/https",
		VirtualHosts: []*routev3.VirtualHost{{
			Name: "vh",
			Routes: []*routev3.Route{
				{
					Name: "connector-offline-vh",
					Action: &routev3.Route_DirectResponse{DirectResponse: &routev3.DirectResponseAction{
						Status: 503,
						Body:   &corev3.DataSource{Specifier: &corev3.DataSource_InlineString{InlineString: offlineBodyMarker}},
					}},
				},
			},
		}},
	}}

	ps := buildProgrammedSet(nil, routes, nil, "coraza-waf", 0, 0, 0)
	assert.Equal(t, []string{connectorRouteKey("gw/https", "vh", "connector-offline-vh")},
		ps.Keys[FamilyConnectorOffline])
	// A 503 with a different body is NOT an offline connector marker.
	assert.Empty(t, ps.Keys[FamilyConnectorRoute])
}

// TestBuildProgrammedSet_WrongKeyedOracle proves the WAF route key changes when
// the governing TPP changes even though the route count is identical — the
// wrong-keyed class the gate exists to catch.
func TestBuildProgrammedSet_WrongKeyedOracle(t *testing.T) {
	routesRight := []*routev3.RouteConfiguration{{
		Name:         "gw/https",
		VirtualHosts: []*routev3.VirtualHost{{Name: "vh", Routes: []*routev3.Route{mkWAFRoute(t, "proj-a", "tpp-a")}}},
	}}
	routesWrong := []*routev3.RouteConfiguration{{
		Name:         "gw/https",
		VirtualHosts: []*routev3.VirtualHost{{Name: "vh", Routes: []*routev3.Route{mkWAFRoute(t, "proj-b", "tpp-b")}}},
	}}

	right := buildProgrammedSet(nil, routesRight, nil, "coraza-waf", 0, 0, 0)
	wrong := buildProgrammedSet(nil, routesWrong, nil, "coraza-waf", 0, 0, 0)

	assert.Equal(t, right.Counts[FamilyWAFRoute], wrong.Counts[FamilyWAFRoute],
		"counts identical — count-only gate would pass")
	assert.NotEqual(t, right.Keys[FamilyWAFRoute], wrong.Keys[FamilyWAFRoute],
		"key set differs — wrong-keyed must be detectable")
}

func TestBuildProgrammedSet_NoCorazaFilterName(t *testing.T) {
	// With an empty filter name the WAF HCM family must not be recorded even
	// though the HCM happens to carry a position-0 filter.
	listeners := []*listenerv3.Listener{mkCorazaHCMListener(t, "gw/https", "coraza-waf", false)}
	ps := buildProgrammedSet(listeners, nil, nil, "", 0, 0, 0)
	assert.Empty(t, ps.Keys[FamilyWAFHCM])
}

// --- recorder + endpoint ---

func TestProgrammedRecorder_EmptyBeforeFirstBuild(t *testing.T) {
	r := newProgrammedRecorder()
	ps := r.snapshot()
	assert.Equal(t, uint64(0), ps.BuildID)
	assert.NotNil(t, ps.Keys)
	assert.NotNil(t, ps.Counts)
}

func TestProgrammedRecorder_BuildIDIncrements(t *testing.T) {
	r := newProgrammedRecorder()
	r.record(nil, nil, nil, "coraza-waf", 0, 0, 0)
	first := r.snapshot()
	r.record(nil, nil, nil, "coraza-waf", 0, 0, 0)
	second := r.snapshot()
	assert.Equal(t, uint64(1), first.BuildID)
	assert.Equal(t, uint64(2), second.BuildID)
}

func TestProgrammedSetHandler_ServesJSON(t *testing.T) {
	r := newProgrammedRecorder()
	routes := []*routev3.RouteConfiguration{{
		Name:         "gw/https",
		VirtualHosts: []*routev3.VirtualHost{{Name: "vh", Routes: []*routev3.Route{mkWAFRoute(t, "proj-a", "tpp-a")}}},
	}}
	r.record(nil, routes, nil, "coraza-waf", 0, 0, 0)

	req := httptest.NewRequest(http.MethodGet, programmedSetEndpointPath, nil)
	w := httptest.NewRecorder()
	r.programmedSetHandler()(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var got ProgrammedSet
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, uint64(1), got.BuildID)
	assert.Equal(t, []string{wafRouteKey("gw/https", "vh", "fwd", "proj-a", "tpp-a", "Observe")},
		got.Keys[FamilyWAFRoute])
}

// TestPostTranslateModify_RecordsProgrammedSet drives the full hook with the
// same fixture as TestPostTranslateModify_FullSnapshot and asserts the
// programmed-set endpoint reflects the build (1 WAF route, 1 WAF HCM, 1
// connector cluster, 1 connector route).
func TestPostTranslateModify_RecordsProgrammedSet(t *testing.T) {
	const (
		upstreamNS    = "test-project"
		dsNS          = upstreamNS
		gwName        = "pset-gw"
		proxyName     = "test-proxy"
		connectorName = "test-connector"
		nodeID        = "test-node-id"
		connCluster   = "httproute/" + dsNS + "/" + proxyName + "/rule/0"
	)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: upstreamNS}}
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tpp", Namespace: upstreamNS},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Kind: "Gateway", Name: gatewayv1.ObjectName(gwName),
				},
			}},
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
					ParanoiaLevels:  networkingv1alpha.ParanoiaLevels{Blocking: 1, Detection: 1},
					ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{Inbound: 5, Outbound: 4},
				},
			}},
		},
	}
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: upstreamNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					Endpoint:  "http://backend.example.com:9000",
					Connector: &networkingv1alpha.ConnectorReference{Name: connectorName},
				}},
			}},
		},
	}
	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: connectorName, Namespace: upstreamNS},
		Status: networkingv1alpha1.ConnectorStatus{
			Conditions: []metav1.Condition{{
				Type: networkingv1alpha1.ConnectorConditionReady, Status: metav1.ConditionTrue,
				Reason: "Ready", LastTransitionTime: metav1.Now(),
			}},
			ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
				Type:      networkingv1alpha1.PublicKeyConnectorConnectionType,
				PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{Id: nodeID},
			},
		},
	}

	scheme := testServerScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(ns, tpp, proxy).
		WithStatusSubresource(connector).WithObjects(connector).Build()
	// Recording only happens when the test-environment endpoint is enabled.
	cfg := testServerConfig()
	cfg.EnableProgrammedSet = true
	srv := New(cl, cfg, discardLogger())

	req := &pb.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{{Name: connCluster}, {Name: "infra-cluster"}},
		Listeners: []*listenerv3.Listener{
			mkListenerWithHCM(t),
			mkListenerWithStaticRoute(t, "envoy-gateway-proxy-ready-0.0.0.0-19003"),
		},
		Routes: []*routev3.RouteConfiguration{{
			Name: "consumer-gw/pset-gw/https",
			VirtualHosts: []*routev3.VirtualHost{{
				Name: "vh", Domains: []string{"app.example.com"}, Metadata: egGatewayMeta(t, dsNS, gwName),
				Routes: []*routev3.Route{{
					Name: "fwd",
					Action: &routev3.Route_Route{Route: &routev3.RouteAction{
						ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: connCluster},
					}},
				}},
			}},
		}},
	}

	_, err := srv.PostTranslateModify(context.Background(), req)
	require.NoError(t, err)

	ps := srv.programmed.snapshot()
	assert.Equal(t, uint64(1), ps.BuildID)
	assert.Equal(t, 1, ps.Counts[FamilyWAFRoute], "one WAF-governed route")
	assert.Equal(t, 1, ps.Counts[FamilyWAFHCM], "coraza injected into the one RDS HCM, not the readiness listener")
	assert.Equal(t, 1, ps.Counts[FamilyConnectorCluster], "one connector cluster replaced")
	assert.Equal(t, 1, ps.Counts[FamilyConnectorRoute], "one CONNECT route prepended")

	// The WAF route key must name the governing TPP (wrong-keyed oracle).
	require.Len(t, ps.Keys[FamilyWAFRoute], 1)
	assert.Contains(t, ps.Keys[FamilyWAFRoute][0], "test-project/test-tpp/Observe")
}

// TestPostTranslateModify_NoRecordingWhenDisabled proves the production default:
// with the test-only endpoint off, a build records nothing, so the snapshot
// stays empty and no per-build work is done.
func TestPostTranslateModify_NoRecordingWhenDisabled(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(testServerScheme(t)).Build()
	// testServerConfig() leaves EnableProgrammedSet at its false default.
	srv := New(cl, testServerConfig(), discardLogger())

	_, err := srv.PostTranslateModify(context.Background(), &pb.PostTranslateModifyRequest{})
	require.NoError(t, err)

	ps := srv.programmed.snapshot()
	assert.Equal(t, uint64(0), ps.BuildID, "no build was recorded")
	assert.Empty(t, ps.Keys, "nothing recorded while the endpoint is disabled")
}
