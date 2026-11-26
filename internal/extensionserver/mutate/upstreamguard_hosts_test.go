package mutate

import (
	"testing"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	commondnsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/common/dns/v3"
	dnsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/dns/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

func dnsClusterWith(t *testing.T, name, typeName string, cfg *dnsv3.DnsCluster) *clusterv3.Cluster {
	t.Helper()
	cl := customDNSCluster(name, typeName, "origin.example.com")
	packed, err := anypb.New(cfg)
	require.NoError(t, err)
	cl.GetClusterType().TypedConfig = packed
	return cl
}

func legacyLogicalDNS(name string) *clusterv3.Cluster {
	cl := dnsCluster(name, clusterv3.Cluster_LOGICAL_DNS, "origin.example.com")
	cl.DnsRefreshRate = durationpb.New(30 * time.Second)
	cl.RespectDnsTtl = true
	cl.DnsLookupFamily = clusterv3.Cluster_V4_ONLY
	cl.DnsFailureRefreshRate = &clusterv3.Cluster_RefreshRate{
		BaseInterval: durationpb.New(2 * time.Second),
		MaxInterval:  durationpb.New(20 * time.Second),
	}
	return cl
}

func clusterDNSConfig(t *testing.T, cl *clusterv3.Cluster) *dnsv3.DnsCluster {
	t.Helper()
	require.Equal(t, dnsClusterType, cl.GetClusterType().GetName())
	cfg := &dnsv3.DnsCluster{}
	require.NoError(t, cl.GetClusterType().GetTypedConfig().UnmarshalTo(cfg))
	return cfg
}

func TestApplyUpstreamAddressGuard_ResolvesEachAddressAsHost(t *testing.T) {
	carried := &dnsv3.DnsCluster{
		DnsRefreshRate:  durationpb.New(30 * time.Second),
		RespectDnsTtl:   true,
		DnsLookupFamily: commondnsv3.DnsLookupFamily_V4_ONLY,
		DnsFailureRefreshRate: &dnsv3.DnsCluster_RefreshRate{
			BaseInterval: durationpb.New(2 * time.Second),
			MaxInterval:  durationpb.New(20 * time.Second),
		},
	}

	tests := []struct {
		name    string
		cluster func(t *testing.T) *clusterv3.Cluster
		want    *dnsv3.DnsCluster
	}{
		{
			name: "dns cluster with all addresses in one endpoint",
			cluster: func(t *testing.T) *clusterv3.Cluster {
				return dnsClusterWith(t, testClusterName(), dnsClusterType, &dnsv3.DnsCluster{
					AllAddressesInSingleEndpoint: true,
					DnsRefreshRate:               durationpb.New(10 * time.Second),
					DnsLookupFamily:              commondnsv3.DnsLookupFamily_V6_ONLY,
				})
			},
			want: &dnsv3.DnsCluster{
				DnsRefreshRate:  durationpb.New(10 * time.Second),
				DnsLookupFamily: commondnsv3.DnsLookupFamily_V6_ONLY,
			},
		},
		{
			name: "logical dns extension cluster with dns config",
			cluster: func(t *testing.T) *clusterv3.Cluster {
				cfg := proto.Clone(carried).(*dnsv3.DnsCluster)
				cfg.AllAddressesInSingleEndpoint = true
				return dnsClusterWith(t, testClusterName(), logicalDNSClusterType, cfg)
			},
			want: carried,
		},
		{
			name: "logical dns extension cluster with legacy settings",
			cluster: func(t *testing.T) *clusterv3.Cluster {
				cl := legacyLogicalDNS(testClusterName())
				cl.ClusterDiscoveryType = &clusterv3.Cluster_ClusterType{ClusterType: &clusterv3.Cluster_CustomClusterType{
					Name: logicalDNSClusterType,
				}}
				return cl
			},
			want: carried,
		},
		{
			name: "legacy logical dns cluster",
			cluster: func(t *testing.T) *clusterv3.Cluster {
				return legacyLogicalDNS(testClusterName())
			},
			want: carried,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := tc.cluster(t)
			n, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
			require.NoError(t, err)
			assert.Equal(t, 1, n)
			got := clusterDNSConfig(t, cl)
			assert.False(t, got.GetAllAddressesInSingleEndpoint())
			assert.True(t, proto.Equal(tc.want, got), "got %v", got)
			assert.Nil(t, cl.GetDnsRefreshRate())
			assert.Nil(t, cl.GetDnsFailureRefreshRate())
			assert.False(t, cl.GetRespectDnsTtl())
			assert.Equal(t, clusterv3.Cluster_AUTO, cl.GetDnsLookupFamily())
			require.NoError(t, got.ValidateAll())
		})
	}
}

func TestApplyUpstreamAddressGuard_KeepsHostPerAddressClusters(t *testing.T) {
	tests := []struct {
		name    string
		cluster func(t *testing.T) *clusterv3.Cluster
	}{
		{"dns cluster", func(t *testing.T) *clusterv3.Cluster {
			return dnsClusterWith(t, testClusterName(), dnsClusterType, &dnsv3.DnsCluster{DnsRefreshRate: durationpb.New(5 * time.Second)})
		}},
		{"strict dns extension cluster", func(t *testing.T) *clusterv3.Cluster {
			return customDNSCluster(testClusterName(), "envoy.cluster.strict_dns", "origin.example.com")
		}},
		{"legacy strict dns cluster", func(t *testing.T) *clusterv3.Cluster {
			return dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example.com")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := tc.cluster(t)
			before := proto.Clone(cl).(*clusterv3.Cluster)
			n, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
			require.NoError(t, err)
			assert.Equal(t, 1, n)
			assert.True(t, proto.Equal(before.GetClusterType(), cl.GetClusterType()))
			assert.Equal(t, before.GetType(), cl.GetType())
		})
	}
}

func TestApplyUpstreamAddressGuard_LeavesUnguardedLogicalDNS(t *testing.T) {
	cl := legacyLogicalDNS("httproute/datum-downstream-gateway/canary/rule/0")
	n, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, clusterv3.Cluster_LOGICAL_DNS, cl.GetType())
	assert.NotNil(t, cl.GetDnsRefreshRate())
}

func TestApplyUpstreamAddressGuard_IndexWithoutEntries(t *testing.T) {
	tests := []struct {
		name    string
		cluster *clusterv3.Cluster
		idx     *extcache.PolicyIndex
	}{
		{
			name:    "vpc ingress literal cluster",
			cluster: dnsCluster("httproute/"+testDSNS+"/vpc/rule/0", clusterv3.Cluster_EDS),
			idx:     &extcache.PolicyIndex{TenantNamespaces: map[string]struct{}{testDSNS: {}}},
		},
		{
			name:    "tenant namespaces not yet known",
			cluster: guardDNSCluster("httproute/" + testDSNS + "/vpc/rule/0"),
			idx:     &extcache.PolicyIndex{},
		},
		{
			name:    "no index",
			cluster: guardDNSCluster("httproute/" + testDSNS + "/vpc/rule/0"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{tc.cluster}, tc.idx)
			require.NoError(t, err)
			assert.Zero(t, n)
			assert.Nil(t, guardFilterNames(t, tc.cluster))
		})
	}
}
