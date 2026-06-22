package cache

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// indexTestScheme returns a runtime.Scheme with all types used by
// BuildPolicyIndexFromClient registered.
func indexTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	return s
}

// newNS builds a Namespace with the given name and UID.
func newNS(name string, uid types.UID) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid},
	}
}

// newTPP builds a minimal TrafficProtectionPolicy in the given namespace.
func newTPP(ns, name string, opts ...func(*networkingv1alpha.TrafficProtectionPolicy)) *networkingv1alpha.TrafficProtectionPolicy {
	tpp := &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
		},
	}
	for _, opt := range opts {
		opt(tpp)
	}
	return tpp
}

// withCreationTime sets a TPP's CreationTimestamp.
func withCreationTime(t time.Time) func(*networkingv1alpha.TrafficProtectionPolicy) {
	return func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
		tpp.CreationTimestamp = metav1.Time{Time: t}
	}
}

// withOWASPCRS adds a default OWASPCoreRuleSet ruleset to a TPP.
func withOWASPCRS(inbound, outbound, blocking, detection int) func(*networkingv1alpha.TrafficProtectionPolicy) {
	return func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
		tpp.Spec.RuleSets = []networkingv1alpha.TrafficProtectionPolicyRuleSet{
			{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
					ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{
						Inbound:  inbound,
						Outbound: outbound,
					},
					ParanoiaLevels: networkingv1alpha.ParanoiaLevels{
						Blocking:  blocking,
						Detection: detection,
					},
				},
			},
		}
	}
}

// newHTTPProxy builds an HTTPProxy with one rule that has a connector backend.
// The proxy name is fixed as "my-proxy" — all callers in this package use that value.
func newHTTPProxy(ns, endpoint, connectorName string) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "my-proxy", Namespace: ns},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint: endpoint,
							Connector: &networkingv1alpha.ConnectorReference{
								Name: connectorName,
							},
						},
					},
				},
			},
		},
	}
}

// newOnlineConnector builds a Connector with Ready=True and a PublicKey nodeID.
func newOnlineConnector(ns, name, nodeID string) *networkingv1alpha1.Connector {
	return &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
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
}

// newOfflineConnector builds a Connector with Ready=False.
func newOfflineConnector(ns, name string) *networkingv1alpha1.Connector {
	return &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
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
}

// =============================================================================
// NS reverse-map tests
// =============================================================================

func TestBuildPolicyIndexFromClient_NSReverseMap_MultipleNamespaces(t *testing.T) {
	scheme := indexTestScheme(t)

	ns1 := newNS("project-alpha", "uid-alpha-0001")
	ns2 := newNS("project-beta", "uid-beta-0002")
	ns3 := newNS("project-gamma", "uid-gamma-0003")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns1, ns2, ns3).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	// Unlabeled namespaces use the identity path: one DStoUS entry per namespace
	// keyed by the namespace name itself (EG puts the plain namespace name in
	// filter_metadata in a single-cluster deployment where no mapping is in play).
	assert.Equal(t, "project-alpha", idx.DStoUS["project-alpha"])
	assert.Equal(t, "project-beta", idx.DStoUS["project-beta"])
	assert.Equal(t, "project-gamma", idx.DStoUS["project-gamma"])
	assert.Len(t, idx.DStoUS, 3, "one identity entry per unlabeled namespace")
}

func TestBuildPolicyIndexFromClient_NSReverseMap_NoNamespaces(t *testing.T) {
	scheme := indexTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)
	assert.Empty(t, idx.DStoUS, "empty cluster must produce empty reverse map")
}

func TestBuildPolicyIndexFromClient_NSReverseMap_UnlabeledUsesIdentity(t *testing.T) {
	// Unlabeled namespaces (single-cluster, no replication) produce identity
	// entries: DStoUS[ns.Name] = ns.Name. In single-cluster EG puts the plain
	// namespace name in filter_metadata, so dsNS == ns.Name and the lookup
	// resolves correctly without any UID arithmetic.
	scheme := indexTestScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(newNS("my-project", "any-uid-value")).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	assert.Equal(t, "my-project", idx.DStoUS["my-project"],
		"unlabeled namespace must resolve via identity: DStoUS[ns.Name] = ns.Name")
	assert.Len(t, idx.DStoUS, 1, "exactly one entry per unlabeled namespace")
}

// TestBuildPolicyIndexFromClient_NSReverseMap_ReplicaNamespaceDistinctUID is the
// GAP-1b regression test. In the two-cluster edge topology the ext-server's client
// lists REPLICA namespaces from the edge cluster. These namespaces are NAMED
// "ns-<upstream-uid>" and carry UpstreamOwnerNamespaceLabel stamped by
// mappednamespace.go, but their own k8s UID is an unrelated edge-cluster value.
//
// Old code: DStoUS was keyed only as "ns-<edge-own-uid>" using the replica
// namespace's own UID, which never matched the dsNS value "ns-<upstream-uid>"
// from EG VH metadata → WAF + Connector mutations silently skipped.
// This test FAILS on the old code and PASSES after the label-based fix.
func TestBuildPolicyIndexFromClient_NSReverseMap_ReplicaNamespaceDistinctUID(t *testing.T) {
	scheme := indexTestScheme(t)

	const upstreamNSName = "my-project"

	// Edge cluster replica namespace:
	//   Name   = "ns-upstream-abc-uid"   (set by mappednamespace.go from upstream UID)
	//   UID    = "edge-own-uid-xyz"       (assigned by EDGE cluster — DISTINCT from upstream UID)
	//   Labels = {upstream-namespace: "my-project"}  (stamped by mappednamespace.go)
	replicaNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-upstream-abc-uid",
			UID:  "edge-own-uid-xyz",
			Labels: map[string]string{
				downstreamclient.UpstreamOwnerNamespaceLabel: upstreamNSName,
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(replicaNS).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	// The critical lookup: dsNS from EG VH metadata is the replica namespace NAME.
	// With the old code this key was absent — only "ns-edge-own-uid-xyz" was set.
	// With the label-based fix: DStoUS["ns-upstream-abc-uid"] = "my-project".
	resolvedUpstream, ok := idx.DStoUS["ns-upstream-abc-uid"]
	assert.True(t, ok,
		"GAP-1b: replica namespace must be resolvable by its NAME in DStoUS "+
			"(old code only keyed by edge-own-uid, which never matched the dsNS from VH metadata)")
	assert.Equal(t, upstreamNSName, resolvedUpstream,
		"label-based resolution: dsNS maps to the upstream namespace name from "+
			"UpstreamOwnerNamespaceLabel, enabling idx.TPPs[upstreamNS] to find policies")

	// The edge-UID-derived key must NOT be present: with the label path the
	// fallback UID keying is skipped, keeping DStoUS clean.
	_, uidKeyPresent := idx.DStoUS["ns-edge-own-uid-xyz"]
	assert.False(t, uidKeyPresent,
		"label path must not add a UID-derived key for replica namespaces")
}

// TestBuildPolicyIndexFromClient_LabelBasedTPPAndConnectorResolution verifies
// the full label-based index path: a replica namespace, replica TPP, and replica
// HTTPProxy all carry UpstreamOwnerNamespaceLabel, and all three are indexed
// consistently under the upstream namespace name so that route→policy resolution
// (dsNS → upstreamNS → TPPs[upstreamNS] / Connectors[{upstreamNS,...}]) works
// in the two-cluster edge topology.
func TestBuildPolicyIndexFromClient_LabelBasedTPPAndConnectorResolution(t *testing.T) {
	const (
		upstreamNSName = "real-project"
		replicaNSName  = "ns-upstream-uid-def"
		replicaNSUID   = types.UID("edge-cluster-uid-xyz") // DISTINCT from embedded "upstream-uid-def"
		proxyName      = "replica-proxy"
		connectorName  = "replica-connector"
	)
	scheme := indexTestScheme(t)

	replicaLabels := map[string]string{
		downstreamclient.UpstreamOwnerNamespaceLabel: upstreamNSName,
	}

	// Replica namespace with label pointing to upstream name.
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   replicaNSName,
			UID:    replicaNSUID,
			Labels: replicaLabels,
		},
	}

	// Replica TPP in the replica namespace, labelled with upstream name.
	tpp := newTPP(replicaNSName, "replica-tpp",
		withOWASPCRS(5, 4, 1, 1),
		func(p *networkingv1alpha.TrafficProtectionPolicy) {
			p.Labels = map[string]string{
				downstreamclient.UpstreamOwnerNamespaceLabel: upstreamNSName,
			}
		},
	)

	// Replica HTTPProxy in the replica namespace, labelled with upstream name.
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
							Endpoint:  "http://backend.example.com:9000",
							Connector: &networkingv1alpha.ConnectorReference{Name: connectorName},
						},
					},
				},
			},
		},
	}

	// Replica Connector (online) in the replica namespace.
	connector := newOnlineConnector(replicaNSName, connectorName, "node-replica-abc")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns, tpp, proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	// DStoUS: dsNS (replica namespace name) → upstream namespace name (from label).
	resolvedNS, ok := idx.DStoUS[replicaNSName]
	require.True(t, ok, "replica namespace must resolve via DStoUS")
	assert.Equal(t, upstreamNSName, resolvedNS,
		"DStoUS must map replica namespace name to upstream namespace label value")

	// TPP indexed by upstreamNSName (from label), not by replicaNSName.
	tpps := idx.TPPs[upstreamNSName]
	assert.Len(t, tpps, 1, "TPP must be indexed under the upstream namespace name from its label")
	assert.Empty(t, idx.TPPs[replicaNSName],
		"TPP must NOT be indexed under the replica namespace name")

	// Connector indexed by upstreamNSName (from proxy label).
	key := ConnectorKey{UpstreamNS: upstreamNSName, HTTPProxyName: proxyName, RuleIndex: 0}
	info, ok := idx.Connectors[key]
	require.True(t, ok, "Connector must be indexed under upstreamNSName from proxy label")
	assert.True(t, info.Online)
	assert.Equal(t, "backend.example.com", info.TargetHost)

	// Simulate the full route resolution: dsNS → upstreamNS → policies.
	// This is what ApplyTPPRouteConfig and ReplaceConnectorClusters do.
	assert.Equal(t, upstreamNSName, idx.DStoUS[replicaNSName],
		"route resolution chain: dsNS → upstreamNS must work end-to-end")
	assert.Len(t, idx.TPPs[idx.DStoUS[replicaNSName]], 1,
		"full chain: idx.TPPs[idx.DStoUS[dsNS]] must find the replica TPP")
}

// =============================================================================
// TPP precedence tests
// =============================================================================

func TestBuildPolicyIndexFromClient_TPPPrecedence_OlderTimestampFirst(t *testing.T) {
	// NSO reconciler: older creation timestamp wins (position 0 in the slice).
	scheme := indexTestScheme(t)
	now := time.Now().Truncate(time.Second)

	tppOld := newTPP("test-ns", "tpp-old",
		withCreationTime(now.Add(-2*time.Hour)),
		withOWASPCRS(5, 4, 1, 1),
	)
	tppNew := newTPP("test-ns", "tpp-new",
		withCreationTime(now),
		withOWASPCRS(5, 4, 1, 1),
	)
	tppMid := newTPP("test-ns", "tpp-mid",
		withCreationTime(now.Add(-1*time.Hour)),
		withOWASPCRS(5, 4, 1, 1),
	)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		// Intentionally seed in reverse order to confirm sort is applied.
		WithObjects(tppNew, tppMid, tppOld).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	tpps := idx.TPPs["test-ns"]
	require.Len(t, tpps, 3, "all three TPPs must appear in the namespace bucket")
	assert.Equal(t, "tpp-old", tpps[0].Name, "oldest TPP must be first (highest precedence)")
	assert.Equal(t, "tpp-mid", tpps[1].Name)
	assert.Equal(t, "tpp-new", tpps[2].Name, "newest TPP must be last (lowest precedence)")
}

func TestBuildPolicyIndexFromClient_TPPPrecedence_NameTiebreakerAlphabetical(t *testing.T) {
	// Same creation timestamp → alphabetical name order (a < z).
	scheme := indexTestScheme(t)
	sameTime := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	tppZ := newTPP("test-ns", "tpp-zebra",
		withCreationTime(sameTime),
		withOWASPCRS(5, 4, 1, 1),
	)
	tppA := newTPP("test-ns", "tpp-aardvark",
		withCreationTime(sameTime),
		withOWASPCRS(5, 4, 1, 1),
	)
	tppM := newTPP("test-ns", "tpp-monkey",
		withCreationTime(sameTime),
		withOWASPCRS(5, 4, 1, 1),
	)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		// Seed in non-alphabetical order.
		WithObjects(tppZ, tppM, tppA).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	tpps := idx.TPPs["test-ns"]
	require.Len(t, tpps, 3)
	assert.Equal(t, "tpp-aardvark", tpps[0].Name, "alphabetically first name must be first")
	assert.Equal(t, "tpp-monkey", tpps[1].Name)
	assert.Equal(t, "tpp-zebra", tpps[2].Name)
}

func TestBuildPolicyIndexFromClient_TPPPrecedence_CrossNamespaceBucketing(t *testing.T) {
	// TPPs in different namespaces are bucketed separately; ordering is
	// per-namespace, not global.
	scheme := indexTestScheme(t)

	tppA1 := newTPP("ns-alpha", "tpp-1", withOWASPCRS(5, 4, 1, 1))
	tppA2 := newTPP("ns-alpha", "tpp-2", withOWASPCRS(5, 4, 1, 1))
	tppB1 := newTPP("ns-beta", "tpp-1", withOWASPCRS(5, 4, 1, 1))

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(tppA1, tppA2, tppB1).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	assert.Len(t, idx.TPPs["ns-alpha"], 2, "ns-alpha must have 2 TPPs")
	assert.Len(t, idx.TPPs["ns-beta"], 1, "ns-beta must have 1 TPP")
	assert.Empty(t, idx.TPPs["ns-other"], "ns-other must have no TPPs")
}

func TestBuildPolicyIndexFromClient_TPPFieldsPreserved(t *testing.T) {
	// Verify that all TPPInfo fields are correctly populated from the CR.
	scheme := indexTestScheme(t)

	tpp := newTPP("test-ns", "my-tpp", func(tpp *networkingv1alpha.TrafficProtectionPolicy) {
		tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce
		tpp.Spec.RuleSets = []networkingv1alpha.TrafficProtectionPolicyRuleSet{
			{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
					ScoreThresholds: networkingv1alpha.OWASPScoreThresholds{
						Inbound: 5, Outbound: 4,
					},
					ParanoiaLevels: networkingv1alpha.ParanoiaLevels{
						Blocking: 1, Detection: 2,
					},
				},
			},
		}
	})

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tpp).Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	tpps := idx.TPPs["test-ns"]
	require.Len(t, tpps, 1)

	info := tpps[0]
	assert.Equal(t, "test-ns", info.Namespace)
	assert.Equal(t, "my-tpp", info.Name)
	assert.Equal(t, networkingv1alpha.TrafficProtectionPolicyEnforce, info.Mode)
	assert.NotEmpty(t, info.Directives, "OWASP CRS rules must generate non-empty directives")
}

// =============================================================================
// computeCorazaDirectives unit tests
//
// These test the package-internal function directly to lock its output format,
// which is part of the xDS metadata contract (config-dump parity gate).
// =============================================================================

func TestComputeCorazaDirectives_NoOWASPRuleSet_ReturnsNil(t *testing.T) {
	tpp := newTPP("ns", "tpp") // no RuleSets
	result := computeCorazaDirectives(tpp, nil)
	assert.Nil(t, result, "no OWASP ruleset → nil directives (TPP skipped by mutation layer)")
}

func TestComputeCorazaDirectives_NoOWASPRuleSet_WithBaseDirectives_ReturnsNil(t *testing.T) {
	tpp := newTPP("ns", "tpp") // no RuleSets
	result := computeCorazaDirectives(tpp, []string{"SecRuleEngine On"})
	assert.Nil(t, result,
		"no OWASP CRS ruleset → nil regardless of baseDirectives (TPP has no directives to compute)")
}

func TestComputeCorazaDirectives_ObserveMode_SecRuleEngineDetectionOnly(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyObserve

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	assert.Contains(t, result, "SecRuleEngine DetectionOnly",
		"Observe mode must set SecRuleEngine DetectionOnly")
}

func TestComputeCorazaDirectives_EnforceMode_SecRuleEngineOn(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	assert.Contains(t, result, "SecRuleEngine On",
		"Enforce mode must set SecRuleEngine On")
}

func TestComputeCorazaDirectives_DisabledMode_SecRuleEngineOff(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyDisabled

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	assert.Contains(t, result, "SecRuleEngine Off",
		"Disabled mode must set SecRuleEngine Off")
}

func TestComputeCorazaDirectives_BaseDirectivesPrependedBeforePolicyDirectives(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	baseDirectives := []string{"Include /etc/modsecurity/*.conf", "SecRequestBodyLimit 1000000"}

	result := computeCorazaDirectives(tpp, baseDirectives)
	require.NotNil(t, result)
	require.GreaterOrEqual(t, len(result), len(baseDirectives)+1)

	// Base directives must appear first (index 0, 1).
	assert.Equal(t, "Include /etc/modsecurity/*.conf", result[0],
		"first base directive must be at index 0")
	assert.Equal(t, "SecRequestBodyLimit 1000000", result[1],
		"second base directive must be at index 1")

	// Policy-specific directives follow base directives.
	assert.Equal(t, "SecRuleEngine DetectionOnly", result[2],
		"SecRuleEngine must come after base directives")
}

func TestComputeCorazaDirectives_AnomalyScoreThresholds(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(7, 3, 2, 2))

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	var found bool
	for _, d := range result {
		if strings.Contains(d, "tx.inbound_anomaly_score_threshold=7") &&
			strings.Contains(d, "tx.outbound_anomaly_score_threshold=3") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"anomaly score thresholds (inbound=7, outbound=3) must appear in directives")
}

func TestComputeCorazaDirectives_ParanoiaLevels(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 3, 4))

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	var foundBlocking, foundDetection bool
	for _, d := range result {
		if strings.Contains(d, "tx.blocking_paranoia_level=3") {
			foundBlocking = true
		}
		if strings.Contains(d, "tx.detection_paranoia_level=4") {
			foundDetection = true
		}
	}
	assert.True(t, foundBlocking, "blocking paranoia level 3 must appear in directives")
	assert.True(t, foundDetection, "detection paranoia level 4 must appear in directives")
}

func TestComputeCorazaDirectives_IncludeOWASPCRSAppendedAfterActions(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	// "Include @owasp_crs/*.conf" must be present.
	assert.Contains(t, result, "Include @owasp_crs/*.conf")

	// It must appear AFTER the SecAction directives (not before).
	includeIdx := -1
	lastActionIdx := -1
	for i, d := range result {
		if d == "Include @owasp_crs/*.conf" {
			includeIdx = i
		}
		if strings.HasPrefix(d, `SecAction "id:9001`) || strings.HasPrefix(d, `SecAction "id:9000`) {
			if i > lastActionIdx {
				lastActionIdx = i
			}
		}
	}
	assert.Greater(t, includeIdx, lastActionIdx,
		"Include @owasp_crs must come after all SecAction directives")
}

func TestComputeCorazaDirectives_SamplingPercentage_AddedWhenBetween0And100(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.SamplingPercentage = 50

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	var found bool
	for _, d := range result {
		if strings.Contains(d, "tx.sampling_percentage=50") {
			found = true
			break
		}
	}
	assert.True(t, found, "sampling percentage 50 must appear in directives")
}

func TestComputeCorazaDirectives_SamplingPercentage_ZeroNotAdded(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.SamplingPercentage = 0

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	for _, d := range result {
		assert.False(t, strings.Contains(d, "sampling_percentage"),
			"sampling_percentage must not appear when value is 0")
	}
}

func TestComputeCorazaDirectives_SamplingPercentage_100NotAdded(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.SamplingPercentage = 100

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	for _, d := range result {
		assert.False(t, strings.Contains(d, "sampling_percentage"),
			"sampling_percentage must not appear when value is 100 (full sampling)")
	}
}

func TestComputeCorazaDirectives_RuleExclusions_Tags(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.RuleSets[0].OWASPCoreRuleSet.RuleExclusions = &networkingv1alpha.OWASPRuleExclusions{
		Tags: []networkingv1alpha.OWASPTag{"attack-injection-php", "OWASP_CRS"},
	}

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	assert.Contains(t, result, `SecRuleRemoveByTag "attack-injection-php"`)
	assert.Contains(t, result, `SecRuleRemoveByTag "OWASP_CRS"`)
}

func TestComputeCorazaDirectives_RuleExclusions_IDs(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.RuleSets[0].OWASPCoreRuleSet.RuleExclusions = &networkingv1alpha.OWASPRuleExclusions{
		IDs: []int{941100, 942200},
	}

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	assert.Contains(t, result, "SecRuleRemoveById 941100")
	assert.Contains(t, result, "SecRuleRemoveById 942200")
}

func TestComputeCorazaDirectives_RuleExclusions_IDRanges(t *testing.T) {
	tpp := newTPP("ns", "tpp", withOWASPCRS(5, 4, 1, 1))
	tpp.Spec.RuleSets[0].OWASPCoreRuleSet.RuleExclusions = &networkingv1alpha.OWASPRuleExclusions{
		IDRanges: []networkingv1alpha.OWASPIDRange{"941100-941200"},
	}

	result := computeCorazaDirectives(tpp, nil)
	require.NotNil(t, result)

	assert.Contains(t, result, `SecRuleRemoveById "941100-941200"`)
}

// =============================================================================
// Connector resolution tests
// =============================================================================

func TestBuildPolicyIndexFromClient_ConnectorResolution_Online(t *testing.T) {
	const (
		upstreamNS    = "test-project"
		proxyName     = "my-proxy"
		connectorName = "my-connector"
		nodeID        = "connector-node-id-abc"
	)
	scheme := indexTestScheme(t)

	proxy := newHTTPProxy(upstreamNS, "http://backend.example.com:9000", connectorName)
	connector := newOnlineConnector(upstreamNS, connectorName, nodeID)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	key := ConnectorKey{
		UpstreamNS:    upstreamNS,
		HTTPProxyName: proxyName,
		RuleIndex:     0,
	}
	info, ok := idx.Connectors[key]
	require.True(t, ok, "ConnectorKey {%s, %s, 0} must be present in index", upstreamNS, proxyName)

	assert.True(t, info.Online, "online connector must have Online=true")
	assert.Equal(t, "backend.example.com", info.TargetHost,
		"TargetHost must be the hostname from the backend Endpoint URL")
	assert.Equal(t, 9000, info.TargetPort,
		"TargetPort must be parsed from the backend Endpoint URL")
	assert.Equal(t, nodeID, info.NodeID,
		"NodeID must be populated from Connector.Status.ConnectionDetails.PublicKey.Id")
}

func TestBuildPolicyIndexFromClient_ConnectorResolution_OfflineConditionFalse(t *testing.T) {
	const (
		upstreamNS    = "test-project"
		proxyName     = "my-proxy"
		connectorName = "my-connector"
	)
	scheme := indexTestScheme(t)

	proxy := newHTTPProxy(upstreamNS, "http://backend.example.com:9000", connectorName)
	connector := newOfflineConnector(upstreamNS, connectorName)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	key := ConnectorKey{UpstreamNS: upstreamNS, HTTPProxyName: proxyName, RuleIndex: 0}
	info, ok := idx.Connectors[key]
	require.True(t, ok, "offline connector must still produce a ConnectorInfo entry")

	assert.False(t, info.Online, "offline connector must have Online=false")
	assert.Empty(t, info.NodeID, "offline connector must not have NodeID")
	// TargetHost/Port are still populated from the Endpoint.
	assert.Equal(t, "backend.example.com", info.TargetHost)
	assert.Equal(t, 9000, info.TargetPort)
}

func TestBuildPolicyIndexFromClient_ConnectorResolution_MissingConnector_TreatedAsOffline(t *testing.T) {
	// HTTPProxy references a Connector by name, but no such Connector object exists.
	// Production behavior: cl.Get returns NotFound → ConnectorInfo{Online: false, ...}.
	const (
		upstreamNS    = "test-project"
		proxyName     = "my-proxy"
		connectorName = "missing-connector"
	)
	scheme := indexTestScheme(t)

	proxy := newHTTPProxy(upstreamNS, "http://backend.example.com:8080", connectorName)
	// No Connector object in the fake client.
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(proxy).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	key := ConnectorKey{UpstreamNS: upstreamNS, HTTPProxyName: proxyName, RuleIndex: 0}
	info, ok := idx.Connectors[key]
	require.True(t, ok,
		"missing Connector must still produce a ConnectorInfo entry (offline)")
	assert.False(t, info.Online)
	assert.Equal(t, "backend.example.com", info.TargetHost)
	assert.Equal(t, 8080, info.TargetPort)
	assert.Empty(t, info.NodeID)
}

func TestBuildPolicyIndexFromClient_ConnectorResolution_MultipleRulesCorrectIndex(t *testing.T) {
	// HTTPProxy with two rules, each with a connector backend at different rule indices.
	const (
		upstreamNS = "test-project"
		proxyName  = "multi-rule-proxy"
	)
	scheme := indexTestScheme(t)

	connector0 := newOnlineConnector(upstreamNS, "conn-0", "node-0")
	connector1 := newOnlineConnector(upstreamNS, "conn-1", "node-1")

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: upstreamNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint:  "http://host0.example.com:9000",
							Connector: &networkingv1alpha.ConnectorReference{Name: "conn-0"},
						},
					},
				},
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint:  "http://host1.example.com:9001",
							Connector: &networkingv1alpha.ConnectorReference{Name: "conn-1"},
						},
					},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(proxy).
		WithStatusSubresource(connector0, connector1).
		WithObjects(connector0, connector1).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	key0 := ConnectorKey{UpstreamNS: upstreamNS, HTTPProxyName: proxyName, RuleIndex: 0}
	key1 := ConnectorKey{UpstreamNS: upstreamNS, HTTPProxyName: proxyName, RuleIndex: 1}

	info0, ok0 := idx.Connectors[key0]
	require.True(t, ok0, "rule 0 connector must be indexed")
	assert.Equal(t, "host0.example.com", info0.TargetHost)
	assert.Equal(t, 9000, info0.TargetPort)
	assert.Equal(t, "node-0", info0.NodeID)

	info1, ok1 := idx.Connectors[key1]
	require.True(t, ok1, "rule 1 connector must be indexed")
	assert.Equal(t, "host1.example.com", info1.TargetHost)
	assert.Equal(t, 9001, info1.TargetPort)
	assert.Equal(t, "node-1", info1.NodeID)
}

func TestBuildPolicyIndexFromClient_ConnectorResolution_NonConnectorBackend_Skipped(t *testing.T) {
	// HTTPProxy rule backend without a Connector ref must not appear in Connectors.
	const (
		upstreamNS = "test-project"
		proxyName  = "http-only-proxy"
	)
	scheme := indexTestScheme(t)

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: upstreamNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Endpoint:  "http://plain-backend.example.com:8080",
							Connector: nil, // no connector
						},
					},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proxy).Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	assert.Empty(t, idx.Connectors,
		"HTTPProxy rules without a Connector ref must not produce Connectors entries")
}

// =============================================================================
// Connector liveness tests
//
// In the two-cluster Karmada topology, the member-cluster Connector the
// extension server reads carries spec + metadata only — Karmada does not
// propagate the status subresource. The replicator mirrors the upstream status
// into the generic UpstreamStatusAnnotation instead. These tests lock the
// annotation-first, live-status-fallback classification.
// =============================================================================

// publicKeyDetails builds ConnectionDetails for the PublicKey connection type
// advertising the given node id — the only connection type defined today.
func publicKeyDetails(nodeID string) *networkingv1alpha1.ConnectorConnectionDetails {
	return &networkingv1alpha1.ConnectorConnectionDetails{
		Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
		PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
			Id: nodeID,
		},
	}
}

// connectorStatus builds a ConnectorStatus with a Ready condition of the given
// truth value and the supplied connection details.
func connectorStatus(ready bool, details *networkingv1alpha1.ConnectorConnectionDetails) networkingv1alpha1.ConnectorStatus {
	readyStatus := metav1.ConditionFalse
	if ready {
		readyStatus = metav1.ConditionTrue
	}
	return networkingv1alpha1.ConnectorStatus{
		Conditions: []metav1.Condition{
			{
				Type:               networkingv1alpha1.ConnectorConditionReady,
				Status:             readyStatus,
				Reason:             "Test",
				LastTransitionTime: metav1.Now(),
			},
		},
		ConnectionDetails: details,
	}
}

// newConnectorWithStatusAnnotation builds a Connector carrying only the mirrored
// upstream-status annotation (no live status) — simulating a member-cluster
// replica object as Karmada delivers it.
func newConnectorWithStatusAnnotation(ns, name string, status networkingv1alpha1.ConnectorStatus) *networkingv1alpha1.Connector {
	raw, _ := json.Marshal(status)
	return &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				networkingv1alpha1.UpstreamStatusAnnotation: string(raw),
			},
		},
	}
}

func TestConnectorLiveness_AnnotationOnlineWithNodeID(t *testing.T) {
	c := newConnectorWithStatusAnnotation("ns", "conn",
		connectorStatus(true, publicKeyDetails("node-anno")))
	online, nodeID := connectorLiveness(c)
	assert.True(t, online, "ready annotation must classify online")
	assert.Equal(t, "node-anno", nodeID, "nodeID must come from the annotation's connectionDetails")
}

func TestConnectorLiveness_AnnotationOnlineUnknownTypeEmptyNodeID(t *testing.T) {
	// A connection type the extension server does not (yet) understand still
	// classifies online from ready, but yields no node ID rather than panicking
	// or assuming PublicKey.
	c := newConnectorWithStatusAnnotation("ns", "conn",
		connectorStatus(true, &networkingv1alpha1.ConnectorConnectionDetails{Type: "FutureType"}))
	online, nodeID := connectorLiveness(c)
	assert.True(t, online, "ready annotation must classify online regardless of connection type")
	assert.Empty(t, nodeID, "unknown connection type must yield an empty node ID")
}

func TestConnectorLiveness_AnnotationOnlineNilDetailsEmptyNodeID(t *testing.T) {
	// Ready but no connection details published yet: online, empty node ID.
	c := newConnectorWithStatusAnnotation("ns", "conn", connectorStatus(true, nil))
	online, nodeID := connectorLiveness(c)
	assert.True(t, online, "ready annotation with nil connectionDetails must classify online")
	assert.Empty(t, nodeID, "nil connectionDetails must yield an empty node ID")
}

func TestConnectorLiveness_AnnotationOffline(t *testing.T) {
	c := newConnectorWithStatusAnnotation("ns", "conn", connectorStatus(false, nil))
	online, nodeID := connectorLiveness(c)
	assert.False(t, online, "not-ready annotation must classify offline")
	assert.Empty(t, nodeID)
}

func TestConnectorLiveness_AnnotationTakesPrecedenceOverStatus(t *testing.T) {
	// Annotation says online; live status says offline. Annotation wins (it is the
	// authoritative status on member clusters where the real status never
	// propagates).
	c := newOfflineConnector("ns", "conn")
	raw, _ := json.Marshal(connectorStatus(true, publicKeyDetails("node-anno")))
	c.Annotations = map[string]string{networkingv1alpha1.UpstreamStatusAnnotation: string(raw)}

	online, nodeID := connectorLiveness(c)
	assert.True(t, online, "annotation must take precedence over live status")
	assert.Equal(t, "node-anno", nodeID)
}

func TestConnectorLiveness_AbsentAnnotationFallsBackToStatus(t *testing.T) {
	// No annotation: single-cluster / pre-rollout object → use status.
	c := newOnlineConnector("ns", "conn", "node-status")
	online, nodeID := connectorLiveness(c)
	assert.True(t, online, "absent annotation must fall back to status-based classification")
	assert.Equal(t, "node-status", nodeID, "nodeID must come from status on fallback")
}

func TestConnectorLiveness_UnparseableAnnotationFallsBackToStatus(t *testing.T) {
	c := newOnlineConnector("ns", "conn", "node-status")
	c.Annotations = map[string]string{
		networkingv1alpha1.UpstreamStatusAnnotation: "{not valid json",
	}
	online, nodeID := connectorLiveness(c)
	assert.True(t, online, "unparseable annotation must fall back to status")
	assert.Equal(t, "node-status", nodeID)
}

func TestConnectorLiveness_NeitherAnnotationNorReadyStatus_Offline(t *testing.T) {
	c := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
	}
	online, nodeID := connectorLiveness(c)
	assert.False(t, online, "no annotation and no Ready condition must classify offline")
	assert.Empty(t, nodeID)
}

// TestBuildPolicyIndexFromClient_ConnectorResolution_AnnotationDrivesOnline is the
// member-cluster regression test: the Connector carries ONLY the mirrored
// upstream-status annotation (no live status, as Karmada delivers it), yet the
// index must classify it online and carry the annotation's nodeID.
// TargetHost/TargetPort still come from the HTTPProxy backend endpoint.
func TestBuildPolicyIndexFromClient_ConnectorResolution_AnnotationDrivesOnline(t *testing.T) {
	const (
		upstreamNS    = "test-project"
		proxyName     = "my-proxy"
		connectorName = "my-connector"
		nodeID        = "node-from-annotation"
	)
	scheme := indexTestScheme(t)

	proxy := newHTTPProxy(upstreamNS, "http://backend.example.com:9000", connectorName)
	connector := newConnectorWithStatusAnnotation(upstreamNS, connectorName,
		connectorStatus(true, publicKeyDetails(nodeID)))

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(proxy, connector).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	key := ConnectorKey{UpstreamNS: upstreamNS, HTTPProxyName: proxyName, RuleIndex: 0}
	info, ok := idx.Connectors[key]
	require.True(t, ok)
	assert.True(t, info.Online, "annotation ready:true must drive Online even with no live status")
	assert.Equal(t, nodeID, info.NodeID, "NodeID must come from the mirrored upstream-status annotation")
	assert.Equal(t, "backend.example.com", info.TargetHost,
		"TargetHost still derives from the HTTPProxy backend endpoint, not the annotation")
	assert.Equal(t, 9000, info.TargetPort)
}

// =============================================================================
// parseEndpoint unit tests
//
// parseEndpoint is the internal helper that extracts TargetHost/TargetPort from
// HTTPProxy.Spec.Rules[i].Backends[j].Endpoint. Its output feeds ConnectorInfo
// directly, so its behavior must be locked.
// =============================================================================

func TestParseEndpoint_HTTP_DefaultPort80(t *testing.T) {
	host, port, err := parseEndpoint("http://backend.example.com")
	require.NoError(t, err)
	assert.Equal(t, "backend.example.com", host)
	assert.Equal(t, 80, port, "http scheme defaults to port 80")
}

func TestParseEndpoint_HTTP_ExplicitPort(t *testing.T) {
	host, port, err := parseEndpoint("http://backend.example.com:9000")
	require.NoError(t, err)
	assert.Equal(t, "backend.example.com", host)
	assert.Equal(t, 9000, port)
}

func TestParseEndpoint_HTTPS_DefaultPort443(t *testing.T) {
	host, port, err := parseEndpoint("https://backend.example.com")
	require.NoError(t, err)
	assert.Equal(t, "backend.example.com", host)
	assert.Equal(t, 443, port, "https scheme defaults to port 443")
}

func TestParseEndpoint_HTTPS_ExplicitPort(t *testing.T) {
	host, port, err := parseEndpoint("https://backend.example.com:8443")
	require.NoError(t, err)
	assert.Equal(t, "backend.example.com", host)
	assert.Equal(t, 8443, port)
}

func TestParseEndpoint_NoHostname_ReturnsError(t *testing.T) {
	_, _, err := parseEndpoint("http://")
	require.Error(t, err, "empty hostname must return an error")
	assert.Contains(t, err.Error(), "no hostname")
}

func TestParseEndpoint_InvalidPort_ReturnsError(t *testing.T) {
	_, _, err := parseEndpoint("http://host.example.com:notaport")
	require.Error(t, err, "non-numeric port must return an error")
}

// =============================================================================
// Namespace name collision — latent multi-cluster risk
// =============================================================================

// TestPopulateFromClient_NamespaceNameCollision_LatentRisk documents and locks
// the assumption that upstream namespace names are globally unique across all
// engaged clusters.
//
// PolicyIndex.TPPs is keyed by upstream namespace NAME (a string), not by a
// (clusterName, namespaceName) tuple. BuildPolicyIndex calls populateFromClient
// once per engaged cluster, accumulating all clusters' policies into a single
// flat map.
//
// In Datum's Milo architecture, project namespace names are derived from
// globally-unique project identifiers, making cross-cluster namespace name
// collisions impossible in practice. However, if this assumption were ever
// violated (e.g., a future naming change), TPPs from two different project
// clusters with the same namespace name would silently accumulate into the same
// PolicyIndex.TPPs key, causing policies from one project to govern traffic
// for another.
//
// This test LOCKS the accumulation behavior so that any future change to the
// keying strategy produces a clear test failure, prompting a review.
func TestPopulateFromClient_NamespaceNameCollision_LatentRisk(t *testing.T) {
	const sharedNSName = "shared-namespace" // same name, two simulated clusters

	scheme := indexTestScheme(t)

	// "Cluster A" fake client: has tpp-a in sharedNSName.
	tppA := newTPP(sharedNSName, "tpp-from-cluster-a", withOWASPCRS(5, 4, 1, 1))
	clA := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tppA).Build()

	// "Cluster B" fake client: has tpp-b in sharedNSName (different cluster,
	// same namespace name — the latent collision scenario).
	tppB := newTPP(sharedNSName, "tpp-from-cluster-b", withOWASPCRS(7, 4, 2, 2))
	clB := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tppB).Build()

	// Simulate what BuildPolicyIndex does: call populateFromClient once per cluster.
	idx := &PolicyIndex{
		DStoUS:     make(map[string]string),
		TPPs:       make(map[string][]TPPInfo),
		Connectors: make(map[ConnectorKey]ConnectorInfo),
	}
	require.NoError(t, populateFromClient(context.Background(), clA, idx, nil))
	require.NoError(t, populateFromClient(context.Background(), clB, idx, nil))

	// LATENT RISK DOCUMENTED HERE: both TPPs end up in the same namespace key.
	// In production (Milo architecture) this is safe because namespace names are
	// globally unique. If that ever changes, this assertion will still pass but the
	// comment warns that the behavior is dangerous.
	tpps := idx.TPPs[sharedNSName]
	assert.Len(t, tpps, 2,
		"cross-cluster TPPs with the same namespace name accumulate into one slice — "+
			"this is SAFE only because Datum's Milo namespace names are globally unique. "+
			"If cross-cluster namespace collisions become possible, PolicyIndex must be "+
			"redesigned to key by (clusterName, namespaceName).")

	// Verify both TPPs are present (order depends on sort, but both must exist).
	names := make([]string, 0, len(tpps))
	for _, info := range tpps {
		names = append(names, info.Name)
	}
	assert.Contains(t, names, "tpp-from-cluster-a")
	assert.Contains(t, names, "tpp-from-cluster-b")
}

// =============================================================================
// Integration: namespace reverse map + TPP + Connector wired together
// =============================================================================

func TestBuildPolicyIndexFromClient_FullIndex(t *testing.T) {
	// Smoke test: all three populations in a single call produce a coherent index.
	const (
		upstreamNS    = "my-project"
		nsUID         = types.UID("uid-1234-5678")
		connectorName = "my-conn"
		proxyName     = "my-proxy"
	)
	scheme := indexTestScheme(t)

	ns := newNS(upstreamNS, nsUID)
	tpp := newTPP(upstreamNS, "my-tpp", withOWASPCRS(5, 4, 1, 1))
	proxy := newHTTPProxy(upstreamNS, "http://svc.internal:8080", connectorName)
	connector := newOnlineConnector(upstreamNS, connectorName, "node-xyz")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns, tpp, proxy).
		WithStatusSubresource(connector).
		WithObjects(connector).
		Build()

	idx, err := BuildPolicyIndexFromClient(context.Background(), cl, []string{"SecRequestBodyLimit 1048576"})
	require.NoError(t, err)

	// NS reverse map: unlabeled namespace produces an identity entry.
	assert.Equal(t, upstreamNS, idx.DStoUS[upstreamNS])

	// TPP indexed and directives computed with base directive prepended.
	tpps := idx.TPPs[upstreamNS]
	require.Len(t, tpps, 1)
	assert.Equal(t, "my-tpp", tpps[0].Name)
	require.NotEmpty(t, tpps[0].Directives)
	assert.Equal(t, "SecRequestBodyLimit 1048576", tpps[0].Directives[0],
		"base directive must be first in the directive list")

	// Connector info indexed under the correct key.
	key := ConnectorKey{UpstreamNS: upstreamNS, HTTPProxyName: proxyName, RuleIndex: 0}
	info, ok := idx.Connectors[key]
	require.True(t, ok)
	assert.True(t, info.Online)
	assert.Equal(t, "svc.internal", info.TargetHost)
	assert.Equal(t, 8080, info.TargetPort)
	assert.Equal(t, "node-xyz", info.NodeID)
}
