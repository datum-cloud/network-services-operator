package mutate

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/stretchr/testify/assert"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

func tenantIndex() *extcache.PolicyIndex {
	return &extcache.PolicyIndex{
		TenantNamespaces: map[string]struct{}{testDSNS: {}},
	}
}

func dnsCluster(name string, kind clusterv3.Cluster_DiscoveryType, hosts ...string) *clusterv3.Cluster {
	lbEndpoints := make([]*endpointv3.LbEndpoint, 0, len(hosts))
	for _, h := range hosts {
		lbEndpoints = append(lbEndpoints, &endpointv3.LbEndpoint{
			HostIdentifier: &endpointv3.LbEndpoint_Endpoint{Endpoint: &endpointv3.Endpoint{
				Address: &corev3.Address{Address: &corev3.Address_SocketAddress{SocketAddress: &corev3.SocketAddress{
					Address:       h,
					PortSpecifier: &corev3.SocketAddress_PortValue{PortValue: 443},
				}}},
			}},
		})
	}
	return &clusterv3.Cluster{
		Name:                 name,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: kind},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: name,
			Endpoints:   []*endpointv3.LocalityLbEndpoints{{LbEndpoints: lbEndpoints}},
		},
	}
}

func clusterHosts(cl *clusterv3.Cluster) []string {
	var hosts []string
	for _, locality := range cl.GetLoadAssignment().GetEndpoints() {
		for _, lbEndpoint := range locality.GetLbEndpoints() {
			hosts = append(hosts, lbEndpoint.GetEndpoint().GetAddress().GetSocketAddress().GetAddress())
		}
	}
	return hosts
}

func TestResolveUpstreamNamesAbsolute(t *testing.T) {
	tests := []struct {
		name        string
		cluster     *clusterv3.Cluster
		wantHosts   []string
		wantMutated int
	}{
		{
			name:        "strict dns name becomes absolute",
			cluster:     dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example.com"),
			wantHosts:   []string{"origin.example.com."},
			wantMutated: 1,
		},
		{
			name:        "two label name becomes absolute",
			cluster:     dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example"),
			wantHosts:   []string{"origin.example."},
			wantMutated: 1,
		},
		{
			name:        "logical dns name becomes absolute",
			cluster:     dnsCluster("securitypolicy/"+testDSNS+"/jwt/jwks/0", clusterv3.Cluster_LOGICAL_DNS, "issuer.example.com"),
			wantHosts:   []string{"issuer.example.com."},
			wantMutated: 1,
		},
		{
			name:        "every endpoint in a cluster becomes absolute",
			cluster:     dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "a.example.com", "b.example.net."),
			wantHosts:   []string{"a.example.com.", "b.example.net."},
			wantMutated: 1,
		},
		{
			name:        "already absolute name is unchanged",
			cluster:     dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example.com."),
			wantHosts:   []string{"origin.example.com."},
			wantMutated: 0,
		},
		{
			name:        "address literals are unchanged",
			cluster:     dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "203.0.113.7", "2001:db8::7"),
			wantHosts:   []string{"203.0.113.7", "2001:db8::7"},
			wantMutated: 0,
		},
		{
			name:        "operator namespace cluster is unchanged",
			cluster:     dnsCluster("httproute/envoy-gateway-system/proxy/rule/0", clusterv3.Cluster_STRICT_DNS, "origin.example.com"),
			wantHosts:   []string{"origin.example.com"},
			wantMutated: 0,
		},
		{
			name:        "cluster that is not derived from a tenant object is unchanged",
			cluster:     dnsCluster("xds_cluster", clusterv3.Cluster_STRICT_DNS, "origin.example.com"),
			wantHosts:   []string{"origin.example.com"},
			wantMutated: 0,
		},
		{
			name:        "tenant namespace in the wrong name position is unchanged",
			cluster:     dnsCluster("ratelimit/"+testDSNS+"/x", clusterv3.Cluster_STRICT_DNS, "origin.example.com"),
			wantHosts:   []string{"origin.example.com"},
			wantMutated: 0,
		},
		{
			name:        "static cluster is unchanged",
			cluster:     dnsCluster(testClusterName(), clusterv3.Cluster_STATIC, "origin.example.com"),
			wantHosts:   []string{"origin.example.com"},
			wantMutated: 0,
		},
		{
			name:        "eds cluster is unchanged",
			cluster:     &clusterv3.Cluster{Name: testClusterName(), ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS}},
			wantHosts:   nil,
			wantMutated: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := ResolveUpstreamNamesAbsolute([]*clusterv3.Cluster{tt.cluster}, tenantIndex())
			assert.Equal(t, tt.wantMutated, mutated)
			assert.Equal(t, tt.wantHosts, clusterHosts(tt.cluster))
		})
	}
}

func TestResolveUpstreamNamesAbsoluteIsIdempotent(t *testing.T) {
	cl := dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example.com")
	assert.Equal(t, 1, ResolveUpstreamNamesAbsolute([]*clusterv3.Cluster{cl}, tenantIndex()))
	assert.Equal(t, 0, ResolveUpstreamNamesAbsolute([]*clusterv3.Cluster{cl}, tenantIndex()))
	assert.Equal(t, []string{"origin.example.com."}, clusterHosts(cl))
}

func TestResolveUpstreamNamesAbsoluteSetsHostname(t *testing.T) {
	cl := dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example.com", "kept.example.com")
	cl.LoadAssignment.Endpoints[0].LbEndpoints[1].GetEndpoint().Hostname = "set.example.com"
	ResolveUpstreamNamesAbsolute([]*clusterv3.Cluster{cl}, tenantIndex())
	lb := cl.LoadAssignment.Endpoints[0].LbEndpoints
	assert.Equal(t, "origin.example.com", lb[0].GetEndpoint().GetHostname())
	assert.Equal(t, "set.example.com", lb[1].GetEndpoint().GetHostname())
}

func TestResolveUpstreamNamesAbsoluteWithoutIndexChangesNothing(t *testing.T) {
	cl := dnsCluster(testClusterName(), clusterv3.Cluster_STRICT_DNS, "origin.example.com")
	assert.Equal(t, 0, ResolveUpstreamNamesAbsolute([]*clusterv3.Cluster{cl}, nil))
	assert.Equal(t, []string{"origin.example.com"}, clusterHosts(cl))
}
