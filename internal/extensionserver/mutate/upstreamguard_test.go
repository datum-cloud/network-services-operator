package mutate

import (
	"net/netip"
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	rbacv3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	rbacfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	upstreamipportv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/rbac/matchers/upstream_ip_port/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

func guardDNSCluster(name string) *clusterv3.Cluster {
	return customDNSCluster(name, "envoy.cluster.dns")
}

func guardTestIndex() *extcache.PolicyIndex {
	return &extcache.PolicyIndex{
		DStoUS:           map[string]string{testDSNS: testUpstreamNS, "datum-downstream-gateway": "datum-downstream-gateway"},
		TenantNamespaces: map[string]struct{}{testDSNS: {}},
		Connectors: map[extcache.ConnectorKey]extcache.ConnectorInfo{
			{Namespace: testDSNS, HTTPProxyName: "tunnel", RuleIndex: 0}:  {Online: true, TargetHost: "connector.local", TargetPort: 80},
			{Namespace: testDSNS, HTTPProxyName: "offline", RuleIndex: 0}: {Online: false, TargetHost: "connector.local", TargetPort: 80},
		},
		VPCPods: map[extcache.VPCPodKey]extcache.VPCPodInfo{
			{Namespace: testDSNS, HTTPProxyName: "vpc", RuleIndex: 0}:            {TenantID: testTenantID},
			{Namespace: testDSNS, HTTPProxyName: "vpc-unresolved", RuleIndex: 0}: {},
		},
		VPCPodBackends: map[extcache.VPCPodBackendKey]extcache.VPCPodInfo{
			{Namespace: testDSNS, HTTPProxyName: "mixed", RuleIndex: 0, BackendIndex: 1}: {TenantID: testTenantID},
		},
	}
}

func guardFilterNames(t *testing.T, cl *clusterv3.Cluster) []string {
	t.Helper()
	raw, ok := cl.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey]
	if !ok {
		return nil
	}
	opts := &httpv3.HttpProtocolOptions{}
	require.NoError(t, raw.UnmarshalTo(opts))
	var names []string
	for _, f := range opts.GetHttpFilters() {
		names = append(names, f.GetName())
	}
	return names
}

func TestApplyUpstreamAddressGuard_SelectsTenantNameResolvingClusters(t *testing.T) {
	staticCluster := func(name string) *clusterv3.Cluster {
		return &clusterv3.Cluster{Name: name, ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC}}
	}
	edsCluster := func(name string) *clusterv3.Cluster {
		return &clusterv3.Cluster{Name: name, ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS}}
	}
	legacyDNS := func(name string, typ clusterv3.Cluster_DiscoveryType) *clusterv3.Cluster {
		return &clusterv3.Cluster{Name: name, ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: typ}}
	}
	dfp := func(name string) *clusterv3.Cluster {
		return &clusterv3.Cluster{Name: name, ClusterDiscoveryType: &clusterv3.Cluster_ClusterType{
			ClusterType: &clusterv3.Cluster_CustomClusterType{Name: "envoy.clusters.dynamic_forward_proxy"},
		}}
	}

	tests := []struct {
		name    string
		cluster *clusterv3.Cluster
		guarded bool
	}{
		{"tenant rule cluster resolving a name", guardDNSCluster(testClusterName()), true},
		{"tenant per-backend cluster resolving a name", guardDNSCluster("httproute/" + testDSNS + "/" + testProxyName + "/rule/2/backend/0"), true},
		{"grpc route cluster", guardDNSCluster("grpcroute/" + testDSNS + "/svc/rule/0"), false},
		{"tenant strict dns extension cluster", customDNSCluster(testClusterName(), "envoy.cluster.strict_dns"), true},
		{"tenant logical dns extension cluster", customDNSCluster(testClusterName(), "envoy.cluster.logical_dns"), true},
		{"tenant legacy strict dns cluster", legacyDNS(testClusterName(), clusterv3.Cluster_STRICT_DNS), true},
		{"tenant legacy logical dns cluster", legacyDNS(testClusterName(), clusterv3.Cluster_LOGICAL_DNS), true},
		{"tenant dynamic forward proxy cluster", dfp(testClusterName()), true},
		{"tenant cluster with literal endpoints", edsCluster(testClusterName()), false},
		{"tenant static cluster", staticCluster(testClusterName()), false},
		{"online connector cluster", staticCluster("httproute/" + testDSNS + "/tunnel/rule/0"), false},
		{"online connector placeholder name", guardDNSCluster("httproute/" + testDSNS + "/tunnel/rule/0"), false},
		{"offline connector placeholder name", guardDNSCluster("httproute/" + testDSNS + "/offline/rule/0"), false},
		{"vpc ingress rule resolving a name", guardDNSCluster("httproute/" + testDSNS + "/vpc/rule/0"), true},
		{"vpc ingress rule with unresolved tenant resolving a name", guardDNSCluster("httproute/" + testDSNS + "/vpc-unresolved/rule/0"), true},
		{"vpc ingress rule with legacy strict dns", legacyDNS("httproute/"+testDSNS+"/vpc/rule/0", clusterv3.Cluster_STRICT_DNS), true},
		{"vpc ingress rule with unresolved tenant and legacy strict dns", legacyDNS("httproute/"+testDSNS+"/vpc-unresolved/rule/0", clusterv3.Cluster_STRICT_DNS), true},
		{"vpc ingress rule with a loopback name", dnsCluster("httproute/"+testDSNS+"/vpc-unresolved/rule/0", clusterv3.Cluster_STRICT_DNS, "127-0-0-1.nip.io"), true},
		{"vpc ingress rule with a loopback name and dns cluster type", customDNSCluster("httproute/"+testDSNS+"/vpc-unresolved/rule/0", "envoy.cluster.dns", "127-0-0-1.nip.io"), true},
		{"vpc ingress rule with static endpoints", staticCluster("httproute/" + testDSNS + "/vpc/rule/0"), false},
		{"vpc ingress rule with eds endpoints", edsCluster("httproute/" + testDSNS + "/vpc-unresolved/rule/0"), false},
		{"vpc ingress backend in a mixed rule resolving a name", guardDNSCluster("httproute/" + testDSNS + "/mixed/rule/0/backend/1"), true},
		{"vpc ingress backend in a mixed rule with static endpoints", staticCluster("httproute/" + testDSNS + "/mixed/rule/0/backend/1"), false},
		{"public backend in a mixed rule", guardDNSCluster("httproute/" + testDSNS + "/mixed/rule/0/backend/0"), true},
		{"platform namespace route", guardDNSCluster("httproute/datum-downstream-gateway/canary/rule/0"), false},
		{"unknown namespace route", guardDNSCluster("httproute/ns-other/proxy/rule/0"), false},
		{"tls route cluster", guardDNSCluster("tlsroute/" + testDSNS + "/relay/rule/-1"), false},
		{"oidc token endpoint cluster", guardDNSCluster("accounts_google_com_443"), false},
		{"xds cluster", guardDNSCluster("xds_cluster"), false},
		{"legacy xds cluster", legacyDNS("xds_cluster", clusterv3.Cluster_STRICT_DNS), false},
		{"malformed rule index", guardDNSCluster("httproute/" + testDSNS + "/p/rule/x"), false},
		{"malformed backend segment", guardDNSCluster("httproute/" + testDSNS + "/p/rule/0/other/1"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusters := []*clusterv3.Cluster{tc.cluster}
			n, err := ApplyUpstreamAddressGuard(clusters, guardTestIndex())
			require.NoError(t, err)
			if tc.guarded {
				assert.Equal(t, 1, n)
				assert.Equal(t, []string{UpstreamAddressGuardFilterName, upstreamCodecFilterName}, guardFilterNames(t, clusters[0]))
			} else {
				assert.Zero(t, n)
				assert.Nil(t, guardFilterNames(t, clusters[0]))
			}
		})
	}
}

func TestApplyUpstreamAddressGuard_PreservesProtocolOptions(t *testing.T) {
	existing := &httpv3.HttpProtocolOptions{
		CommonHttpProtocolOptions: &corev3.HttpProtocolOptions{MaxRequestsPerConnection: nil},
		UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
					Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
				},
			},
		},
	}
	packed, err := anypb.New(existing)
	require.NoError(t, err)
	cl := guardDNSCluster(testClusterName())
	cl.TypedExtensionProtocolOptions = map[string]*anypb.Any{httpProtocolOptionsKey: packed}

	_, err = ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
	require.NoError(t, err)

	got := &httpv3.HttpProtocolOptions{}
	require.NoError(t, cl.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey].UnmarshalTo(got))
	assert.NotNil(t, got.GetExplicitHttpConfig().GetHttp2ProtocolOptions())
	assert.NotNil(t, got.GetCommonHttpProtocolOptions())
}

func TestApplyUpstreamAddressGuard_DefaultsToHTTP1(t *testing.T) {
	cl := guardDNSCluster(testClusterName())
	_, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
	require.NoError(t, err)

	got := &httpv3.HttpProtocolOptions{}
	require.NoError(t, cl.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey].UnmarshalTo(got))
	assert.NotNil(t, got.GetExplicitHttpConfig().GetHttpProtocolOptions())
}

func TestApplyUpstreamAddressGuard_KeepsExistingUpstreamFilters(t *testing.T) {
	existing := &httpv3.HttpProtocolOptions{
		HttpFilters: []*hcmv3.HttpFilter{
			{Name: "envoy.filters.http.header_mutation"},
			{Name: upstreamCodecFilterName},
		},
	}
	packed, err := anypb.New(existing)
	require.NoError(t, err)
	cl := guardDNSCluster(testClusterName())
	cl.TypedExtensionProtocolOptions = map[string]*anypb.Any{httpProtocolOptionsKey: packed}

	_, err = ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
	require.NoError(t, err)
	assert.Equal(t,
		[]string{UpstreamAddressGuardFilterName, "envoy.filters.http.header_mutation", upstreamCodecFilterName},
		guardFilterNames(t, cl))
}

func TestApplyUpstreamAddressGuard_Idempotent(t *testing.T) {
	cl := guardDNSCluster(testClusterName())
	clusters := []*clusterv3.Cluster{cl}
	first, err := ApplyUpstreamAddressGuard(clusters, guardTestIndex())
	require.NoError(t, err)
	second, err := ApplyUpstreamAddressGuard(clusters, guardTestIndex())
	require.NoError(t, err)
	assert.Equal(t, 1, first)
	assert.Zero(t, second)
	assert.Equal(t, []string{UpstreamAddressGuardFilterName, upstreamCodecFilterName}, guardFilterNames(t, cl))
}

func guardPolicy(t *testing.T) *rbacv3.Policy {
	t.Helper()
	cl := guardDNSCluster(testClusterName())
	_, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
	require.NoError(t, err)

	opts := &httpv3.HttpProtocolOptions{}
	require.NoError(t, cl.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey].UnmarshalTo(opts))
	rbac := &rbacfilterv3.RBAC{}
	require.NoError(t, opts.GetHttpFilters()[0].GetTypedConfig().UnmarshalTo(rbac))
	require.Equal(t, rbacv3.RBAC_DENY, rbac.GetRules().GetAction())

	policy := rbac.GetRules().GetPolicies()[upstreamAddressGuardPolicyName]
	require.NotNil(t, policy)
	require.True(t, policy.GetPrincipals()[0].GetAny())
	require.Len(t, policy.GetPermissions(), 1)
	return policy
}

func permissionMatches(t *testing.T, p *rbacv3.Permission, a netip.Addr) bool {
	t.Helper()
	switch r := p.GetRule().(type) {
	case *rbacv3.Permission_OrRules:
		for _, sub := range r.OrRules.GetRules() {
			if permissionMatches(t, sub, a) {
				return true
			}
		}
		return false
	case *rbacv3.Permission_AndRules:
		for _, sub := range r.AndRules.GetRules() {
			if !permissionMatches(t, sub, a) {
				return false
			}
		}
		return true
	case *rbacv3.Permission_NotRule:
		return !permissionMatches(t, r.NotRule, a)
	case *rbacv3.Permission_Matcher:
		require.Equal(t, upstreamIPPortMatcherName, r.Matcher.GetName())
		m := &upstreamipportv3.UpstreamIpPortMatcher{}
		require.NoError(t, r.Matcher.GetTypedConfig().UnmarshalTo(m))
		addr, err := netip.ParseAddr(m.GetUpstreamIp().GetAddressPrefix())
		require.NoError(t, err)
		prefix := netip.PrefixFrom(addr, int(m.GetUpstreamIp().GetPrefixLen().GetValue()))
		require.True(t, prefix.IsValid())
		return prefix.Contains(a)
	}
	t.Fatalf("unexpected permission %T", p.GetRule())
	return false
}

func TestApplyUpstreamAddressGuard_DeniedAddresses(t *testing.T) {
	policy := guardPolicy(t)
	denied := func(a netip.Addr) bool {
		return permissionMatches(t, policy.GetPermissions()[0], a)
	}

	tests := []struct {
		addr   string
		denied bool
	}{
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"0.0.0.0", true},
		{"10.96.0.1", true},
		{"10.244.11.181", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"255.255.255.255", true},
		{"224.0.0.1", true},
		{"::", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:8.8.8.8", true},
		{"64:ff9b::7f00:1", true},
		{"64:ff9b::a00:1", true},
		{"64:ff9b::ac10:1", true},
		{"64:ff9b::c0a8:101", true},
		{"64:ff9b::a9fe:a9fe", true},
		{"64:ff9b::6440:1", true},
		{"64:ff9b::1", true},
		{"64:ff9b::e000:1", true},
		{"64:ff9b:1::7f00:1", true},
		{"64:ff9b:1::a00:1", true},
		{"64:ff9b:1::a9fe:a9fe", true},
		{"64:ff9b:1::6440:1", true},
		{"64:ff9b:1:1::808:808", true},
		{"64:ff9b:1:ffff::1", true},
		{"::ffff:10.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"::ffff:100.64.0.1", true},
		{"192.0.2.1", true},
		{"198.51.100.7", true},
		{"203.0.113.10", true},
		{"2001:db8::1", true},
		{"100::1", true},
		{"64:ff9b::c000:201", true},
		{"64:ff9b:1::cb00:710a", true},
		{"64:ff9b::808:808", false},
		{"64:ff9b::4321:a4e1", false},
		{"64:ff9b:1::808:808", false},
		{"64:ff9b::ac20:1", false},
		{"2002:7f00:1::", true},
		{"2001:0:4136:e378:8000:63bf:80ff:fffe", true},
		{"fc00::1", true},
		{"fd20:0:0:b::c93b", true},
		{"fe80::1", true},
		{"ff02::1", true},
		{"8.8.8.8", false},
		{"172.32.0.1", false},
		{"100.128.0.1", false},
		{"67.14.164.1", false},
		{"170.23.227.207", false},
		{"2607:ed40:10::1", false},
		{"2606:4700::6810:84e5", false},
		{"2001:4860:4860::8888", false},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			assert.Equal(t, tc.denied, denied(netip.MustParseAddr(tc.addr)))
		})
	}
}

func TestApplyUpstreamAddressGuard_ConfigPassesEnvoyValidation(t *testing.T) {
	cl := guardDNSCluster(testClusterName())
	_, err := ApplyUpstreamAddressGuard([]*clusterv3.Cluster{cl}, guardTestIndex())
	require.NoError(t, err)

	opts := &httpv3.HttpProtocolOptions{}
	require.NoError(t, cl.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey].UnmarshalTo(opts))
	require.NoError(t, opts.ValidateAll())

	rbac := &rbacfilterv3.RBAC{}
	require.NoError(t, opts.GetHttpFilters()[0].GetTypedConfig().UnmarshalTo(rbac))
	require.NoError(t, rbac.ValidateAll())
	var validate func(p *rbacv3.Permission)
	validate = func(p *rbacv3.Permission) {
		switch r := p.GetRule().(type) {
		case *rbacv3.Permission_OrRules:
			for _, sub := range r.OrRules.GetRules() {
				validate(sub)
			}
		case *rbacv3.Permission_AndRules:
			for _, sub := range r.AndRules.GetRules() {
				validate(sub)
			}
		case *rbacv3.Permission_NotRule:
			validate(r.NotRule)
		default:
			m := &upstreamipportv3.UpstreamIpPortMatcher{}
			require.NoError(t, p.GetMatcher().GetTypedConfig().UnmarshalTo(m))
			require.NoError(t, m.ValidateAll())
		}
	}
	validate(rbac.GetRules().GetPolicies()[upstreamAddressGuardPolicyName].GetPermissions()[0])
}
