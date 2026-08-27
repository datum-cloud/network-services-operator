package mutate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/downstreamclient"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// testTenantID is a realistic "<vpc>-<vpcAttachment>" identifier: both halves
// are base62, as galactic's crdnames.TenantIdentifier guarantees.
const testTenantID = "2wJqT7d-9xKp2Qm"

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
	assert.Equal(t, vrfDeviceName(testTenantID), string(opts[0].GetBufValue()))
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

// TestVRFDeviceName_MatchesGalactic pins the device name against galactic's
// own naming function. The expected values were produced by calling
// intf.GenerateInterfaceNameVRF in datum-cloud/galactic directly, not by
// re-deriving them from the format string here — a name that differs from the
// device galactic actually creates resolves nothing, and Envoy reports it as
// every connection timing out rather than as a configuration error.
func TestVRFDeviceName_MatchesGalactic(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		want     string
	}{
		{"typical vpc", "2wJqT7d-9xKp2Qm", "G002wJqT7dV"},
		{"another vpc", "5hLm3Xc-9xKp2Qm", "G005hLm3XcV"},
		{"single character vpc", "1-9xKp2Qm", "G000000001V"},
		{"alphabetic vpc", "abc-9xKp2Qm", "G000000abcV"},
		{"vpc filling the pad", "123456789-9xKp2Qm", "G123456789V"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, vrfDeviceName(tt.tenantID))
		})
	}
}

// TestVRFDeviceName_KeyedOnVPCAlone pins the property that makes the naming
// scheme correct: galactic shares one VRF device across every attachment of a
// VPC on a node, so the attachment half must not reach the name.
func TestVRFDeviceName_KeyedOnVPCAlone(t *testing.T) {
	first := vrfDeviceName("2wJqT7d-9xKp2Qm")
	second := vrfDeviceName("2wJqT7d-4bNr8Zt")

	require.NotEmpty(t, first)
	assert.Equal(t, first, second, "two attachments of one VPC share one device")

	other := vrfDeviceName("5hLm3Xc-9xKp2Qm")
	assert.NotEqual(t, first, other, "distinct VPCs must get distinct devices")
}

func TestVRFDeviceName_UnusableTenantIDYieldsNoName(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
	}{
		{"empty", ""},
		{"no separator", "2wJqT7d"},
		{"empty vpc half", "-9xKp2Qm"},
		{"empty attachment half", "2wJqT7d-"},
		{"vpc half overflows IFNAMSIZ", "abcdefghijklmnop-9xKp2Qm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, vrfDeviceName(tt.tenantID),
				"an unusable tenant identifier must yield no name, so the caller skips the bind")
		})
	}
}

func TestVRFDeviceName_FitsIFNAMSIZ(t *testing.T) {
	assert.LessOrEqual(t, len(vrfDeviceName(testTenantID)), maxInterfaceNameLen)
}

// TestApplyVPCPodSocketBind_NetworkServiceBackendEndToEnd drives the whole
// path the way production does: Kubernetes objects in, PolicyIndex built by
// the cache layer, Envoy cluster out. It exists because the two halves of this
// feature can each be correct in isolation and still not meet — the index has
// to key the entry the same way the mutator looks it up, and both have to
// agree with the cluster name Envoy Gateway assigns.
func TestApplyVPCPodSocketBind_NetworkServiceBackendEndToEnd(t *testing.T) {
	const tenantID = "2wJqT7d-9xKp2Qm"

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, discoveryv1.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(scheme))

	// The edge sees the replica namespace and the HTTPProxy replica within it,
	// both stamped with the upstream namespace they came from.
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   testDSNS,
			Labels: map[string]string{downstreamclient.UpstreamOwnerNamespaceLabel: testUpstreamNS},
		},
	}

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testProxyName,
			Namespace: testDSNS,
			Labels:    map[string]string{downstreamclient.UpstreamOwnerNamespaceLabel: testUpstreamNS},
		},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							NetworkService: &networkingv1alpha.NetworkServiceBackendRef{
								Name: "my-service",
								Port: "http",
							},
						},
					},
				},
			},
		},
	}

	// The downstream copy of the slice the HTTPProxy controller synthesized for
	// this backend, carrying the member address.
	members := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-some-uid-rule-0-backendref-0",
			Namespace: testDSNS,
			Labels: map[string]string{
				downstreamclient.UpstreamOwnerNameLabel: testProxyName + "-0-0",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"fd20:0:2::1:0:0"}}},
	}

	// The per-pod slice galactic published for that member, federated to this
	// edge from the cell that hosts it.
	galactic := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vpc-us-central-1-pod-a",
			Namespace: testDSNS,
			Labels:    map[string]string{extcache.VPCPodTenantIDLabel: tenantID},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"fd20:0:2::1:0:0"}}},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(namespace, proxy, members, galactic).
		Build()

	idx, err := extcache.BuildPolicyIndexFromClient(context.Background(), cl, nil)
	require.NoError(t, err)

	clusters := []*clusterv3.Cluster{{Name: testClusterName()}}

	mutated, err := ApplyVPCPodSocketBind(clusters, idx)
	require.NoError(t, err)
	require.Equal(t, 1, mutated, "a networkService backend on a tenant VPC must be bound")

	opts := clusters[0].GetUpstreamBindConfig().GetSocketOptions()
	require.Len(t, opts, 1)
	assert.EqualValues(t, soLevelSocket, opts[0].GetLevel())
	assert.EqualValues(t, soNameBindToDevice, opts[0].GetName())
	assert.Equal(t, "G002wJqT7dV", string(opts[0].GetBufValue()),
		"must be the device galactic's sidecar creates for this VPC")
}
