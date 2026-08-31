package mutate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// testTenantID follows galactic's crdnames.TenantIdentifier(vpc,
// vpcAttachment) format ("<vpc>-<vpcAttachment>", both base62) — the real
// shape galactic-cni publishes, not an arbitrary string.
const testTenantID = "2-8s5"

// vpcPodPolicyIndex builds a PolicyIndex with a single vpcPod entry, reusing
// the connector test fixtures' dsNS/upstreamNS/proxyName so testClusterName()
// (defined in connector_test.go) applies unchanged — EG names every
// HTTPRoute-rule cluster the same way, connector or not.
func vpcPodPolicyIndex(tenantID string) *extcache.PolicyIndex {
	return &extcache.PolicyIndex{
		DStoUS: map[string]string{testDSNS: testUpstreamNS},
		VPCPods: map[extcache.VPCPodKey]extcache.VPCPodInfo{
			{
				UpstreamNS:    testUpstreamNS,
				HTTPProxyName: testProxyName,
				RuleIndex:     0,
			}: {TenantID: tenantID},
		},
	}
}

func TestApplyVPCPodSocketBind_BindsMatchingCluster(t *testing.T) {
	idx := vpcPodPolicyIndex(testTenantID)
	clusterName := testClusterName()

	clusters := []*clusterv3.Cluster{
		{Name: clusterName, LbPolicy: clusterv3.Cluster_ROUND_ROBIN},
		{Name: "infra-cluster"}, // must be untouched
	}

	mutated, err := ApplyVPCPodSocketBind(clusters, idx)
	require.NoError(t, err)
	assert.Equal(t, 1, mutated)

	got := clusters[0]
	// Existing fields must survive the patch — this is a merge, not a
	// wholesale replacement like buildConnectorCluster.
	assert.Equal(t, clusterName, got.GetName())
	assert.Equal(t, clusterv3.Cluster_ROUND_ROBIN, got.GetLbPolicy())

	opts := got.GetUpstreamBindConfig().GetSocketOptions()
	require.Len(t, opts, 1)
	assert.EqualValues(t, soLevelSocket, opts[0].GetLevel(), "level must be SOL_SOCKET")
	assert.EqualValues(t, soNameBindToDevice, opts[0].GetName(), "name must be SO_BINDTODEVICE")
	assert.Equal(t, corev3.SocketOption_STATE_PREBIND, opts[0].GetState())

	// GetBufValue() returns the raw bytes — protojson already decoded the
	// wire-format base64 during Unmarshal.
	wantDevice, ok := vrfDeviceName(testTenantID)
	require.True(t, ok)
	assert.Equal(t, wantDevice, string(opts[0].GetBufValue()))
	assert.LessOrEqual(t, len(opts[0].GetBufValue()), 15, "device name must fit IFNAMSIZ-1")

	// Non-matching cluster untouched.
	assert.Equal(t, "infra-cluster", clusters[1].GetName())
	assert.Nil(t, clusters[1].GetUpstreamBindConfig())
}

func TestApplyVPCPodSocketBind_ClusterNotInPolicyIndex_Skipped(t *testing.T) {
	// Cluster name matches the httproute pattern, but idx has no VPCPods entry
	// for it (e.g. an ordinary endpoint/connector backend on this rule).
	idx := &extcache.PolicyIndex{
		DStoUS:  map[string]string{testDSNS: testUpstreamNS},
		VPCPods: map[extcache.VPCPodKey]extcache.VPCPodInfo{},
	}

	clusters := []*clusterv3.Cluster{{Name: testClusterName()}}

	mutated, err := ApplyVPCPodSocketBind(clusters, idx)
	require.NoError(t, err)
	assert.Zero(t, mutated)
	assert.Nil(t, clusters[0].GetUpstreamBindConfig())
}

func TestApplyVPCPodSocketBind_EmptyTenantID_Skipped(t *testing.T) {
	// Simulates a vpcPod backend whose referenced EndpointSlice was missing
	// or carried no tenant-id label — TenantID left empty by design (see
	// cache/index.go). Must never bind to a zero-value device name.
	idx := vpcPodPolicyIndex("")

	clusters := []*clusterv3.Cluster{{Name: testClusterName()}}

	mutated, err := ApplyVPCPodSocketBind(clusters, idx)
	require.NoError(t, err)
	assert.Zero(t, mutated)
	assert.Nil(t, clusters[0].GetUpstreamBindConfig())
}

func TestApplyVPCPodSocketBind_NonMatchingClusterName_Untouched(t *testing.T) {
	idx := vpcPodPolicyIndex(testTenantID)

	clusters := []*clusterv3.Cluster{{Name: "grpc-backend"}}

	mutated, err := ApplyVPCPodSocketBind(clusters, idx)
	require.NoError(t, err)
	assert.Zero(t, mutated)
	assert.Nil(t, clusters[0].GetUpstreamBindConfig())
}

func TestVRFDeviceName(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		want     string
		wantOK   bool
	}{
		{
			name:     "single-character vpc, zero-padded",
			tenantID: "2-8s5",
			want:     "G000000002V",
			wantOK:   true,
		},
		{
			// Matches a VRF device observed live in
			// us-central-1-staging-lab (G0ouHZATYMV) — an 8-character vpc
			// pads with exactly one leading zero.
			name:     "eight-character vpc",
			tenantID: "ouHZATYM-x1",
			want:     "G0ouHZATYMV",
			wantOK:   true,
		},
		{
			name:     "nine-character vpc, no padding needed",
			tenantID: "123456789-x1",
			want:     "G123456789V",
			wantOK:   true,
		},
		{
			name:     "no separator",
			tenantID: "novpcattachment",
			wantOK:   false,
		},
		{
			name:     "empty vpc half",
			tenantID: "-8s5",
			wantOK:   false,
		},
		{
			name:     "vpc half exceeds 9 base62 characters",
			tenantID: "1234567890-x1",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := vrfDeviceName(tt.tenantID)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
				assert.Len(t, got, 11)
				assert.LessOrEqual(t, len(got), 15, "device name must fit IFNAMSIZ-1")
			}
		})
	}

	// Deterministic: same tenant always yields the same device name.
	first, _ := vrfDeviceName(testTenantID)
	second, _ := vrfDeviceName(testTenantID)
	assert.Equal(t, first, second)
}
