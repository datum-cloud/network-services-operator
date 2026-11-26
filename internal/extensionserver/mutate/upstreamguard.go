package mutate

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	rbacv3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	commondnsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/common/dns/v3"
	dnsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/dns/v3"
	rbacfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
	upstreamcodecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	upstreamipportv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/rbac/matchers/upstream_ip_port/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
	"go.datum.net/network-services-operator/internal/util/addressrange"
)

const (
	UpstreamAddressGuardFilterName = "envoy.filters.http.upstream_rbac"
	upstreamAddressGuardPolicyName = "non-public-address"
	upstreamIPPortMatcherName      = "envoy.rbac.matchers.upstream_ip_port"
	upstreamCodecFilterName        = "envoy.filters.http.upstream_codec"
	httpProtocolOptionsKey         = "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"
	dynamicForwardProxyClusterType = "envoy.clusters.dynamic_forward_proxy"
	dnsClusterType                 = "envoy.cluster.dns"
	logicalDNSClusterType          = "envoy.cluster.logical_dns"
)

func nonPublicUpstreamPrefixes() []netip.Prefix {
	var v4 []netip.Prefix
	out := make([]netip.Prefix, 0, len(addressrange.NonPublic)*(1+len(addressrange.NAT64TranslationPrefixes())))
	for _, r := range addressrange.NonPublic {
		out = append(out, r.Prefix)
		if r.Prefix.Addr().Is4() {
			v4 = append(v4, r.Prefix)
		}
	}
	for _, n := range addressrange.NAT64TranslationPrefixes() {
		for _, p := range v4 {
			out = append(out, embedIPv4(n, p))
		}
	}
	return out
}

func embedIPv4(nat64 netip.Prefix, v4 netip.Prefix) netip.Prefix {
	b := nat64.Addr().As16()
	a := v4.Addr().As4()
	copy(b[12:], a[:])
	return netip.PrefixFrom(netip.AddrFrom16(b), 96+v4.Bits())
}

func cidrPermission(p netip.Prefix) (*rbacv3.Permission, error) {
	matcher, err := anypb.New(&upstreamipportv3.UpstreamIpPortMatcher{
		UpstreamIp: &corev3.CidrRange{
			AddressPrefix: p.Addr().String(),
			PrefixLen:     wrapperspb.UInt32(uint32(p.Bits())),
		},
	})
	if err != nil {
		return nil, err
	}
	return &rbacv3.Permission{
		Rule: &rbacv3.Permission_Matcher{Matcher: &corev3.TypedExtensionConfig{
			Name:        upstreamIPPortMatcherName,
			TypedConfig: matcher,
		}},
	}, nil
}

func ApplyUpstreamAddressGuard(clusters []*clusterv3.Cluster, idx *extcache.PolicyIndex) (int, error) {
	filter, err := upstreamAddressGuardFilter()
	if err != nil {
		return 0, err
	}
	guarded := 0
	for _, cl := range clusters {
		if !isGuardedTenantCluster(cl, idx) {
			continue
		}
		added, err := guardCluster(cl, filter)
		if err != nil {
			return guarded, fmt.Errorf("guard cluster %q: %w", cl.GetName(), err)
		}
		if added {
			guarded++
		}
	}
	return guarded, nil
}

func isGuardedTenantCluster(cl *clusterv3.Cluster, idx *extcache.PolicyIndex) bool {
	if !guardResolvesNames(cl) || !isTenantCluster(cl.GetName(), idx) {
		return false
	}
	ns, proxy, rule, ok := parseTenantRouteClusterName(cl.GetName())
	if !ok {
		return false
	}
	_, connector := idx.Connectors[extcache.ConnectorKey{Namespace: ns, HTTPProxyName: proxy, RuleIndex: rule}]
	return !connector
}

func guardResolvesNames(cl *clusterv3.Cluster) bool {
	return resolvesNames(cl) || cl.GetClusterType().GetName() == dynamicForwardProxyClusterType
}

func parseTenantRouteClusterName(name string) (ns, route string, rule int, ok bool) {
	kind, rest, found := strings.Cut(name, "/")
	if !found || kind != "httproute" {
		return "", "", 0, false
	}
	parts := strings.Split(rest, "/")
	if (len(parts) != 4 && len(parts) != 6) || parts[2] != "rule" {
		return "", "", 0, false
	}
	rule, err := strconv.Atoi(parts[3])
	if err != nil || rule < 0 {
		return "", "", 0, false
	}
	if len(parts) == 6 {
		if parts[4] != "backend" {
			return "", "", 0, false
		}
		if backend, err := strconv.Atoi(parts[5]); err != nil || backend < 0 {
			return "", "", 0, false
		}
	}
	return parts[0], parts[1], rule, true
}

func upstreamAddressGuardFilter() (*hcmv3.HttpFilter, error) {
	prefixes := nonPublicUpstreamPrefixes()
	permissions := make([]*rbacv3.Permission, 0, len(prefixes)+1)
	for _, p := range prefixes {
		perm, err := cidrPermission(p)
		if err != nil {
			return nil, err
		}
		permissions = append(permissions, perm)
	}
	localUse, err := cidrPermission(addressrange.LocalUseNAT64.Prefix)
	if err != nil {
		return nil, err
	}
	embedded, err := cidrPermission(addressrange.LocalUseNAT64Translation)
	if err != nil {
		return nil, err
	}
	permissions = append(permissions, &rbacv3.Permission{
		Rule: &rbacv3.Permission_AndRules{AndRules: &rbacv3.Permission_Set{Rules: []*rbacv3.Permission{
			localUse,
			{Rule: &rbacv3.Permission_NotRule{NotRule: embedded}},
		}}},
	})
	cfg, err := anypb.New(&rbacfilterv3.RBAC{
		Rules: &rbacv3.RBAC{
			Action: rbacv3.RBAC_DENY,
			Policies: map[string]*rbacv3.Policy{
				upstreamAddressGuardPolicyName: {
					Permissions: []*rbacv3.Permission{{
						Rule: &rbacv3.Permission_OrRules{OrRules: &rbacv3.Permission_Set{Rules: permissions}},
					}},
					Principals: []*rbacv3.Principal{{Identifier: &rbacv3.Principal_Any{Any: true}}},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return &hcmv3.HttpFilter{
		Name:       UpstreamAddressGuardFilterName,
		ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: cfg},
	}, nil
}

func guardCluster(cl *clusterv3.Cluster, filter *hcmv3.HttpFilter) (bool, error) {
	if err := resolveEachAddressAsHost(cl); err != nil {
		return false, err
	}
	return addUpstreamHTTPFilter(cl, filter)
}

func resolveEachAddressAsHost(cl *clusterv3.Cluster) error {
	ct := cl.GetClusterType()
	if ct == nil {
		if cl.GetType() != clusterv3.Cluster_LOGICAL_DNS {
			return nil
		}
		return setDNSClusterType(cl, dnsClusterFromLegacy(cl))
	}
	switch ct.GetName() {
	case dnsClusterType:
		cfg := &dnsv3.DnsCluster{}
		if ct.GetTypedConfig() == nil || !ct.GetTypedConfig().MessageIs(cfg) {
			return nil
		}
		if err := ct.GetTypedConfig().UnmarshalTo(cfg); err != nil {
			return fmt.Errorf("decode %s config: %w", dnsClusterType, err)
		}
		if !cfg.GetAllAddressesInSingleEndpoint() {
			return nil
		}
		cfg.AllAddressesInSingleEndpoint = false
		return setDNSClusterType(cl, cfg)
	case logicalDNSClusterType:
		cfg := &dnsv3.DnsCluster{}
		if tc := ct.GetTypedConfig(); tc != nil && tc.MessageIs(cfg) {
			if err := tc.UnmarshalTo(cfg); err != nil {
				return fmt.Errorf("decode %s config: %w", logicalDNSClusterType, err)
			}
		} else {
			cfg = dnsClusterFromLegacy(cl)
		}
		cfg.AllAddressesInSingleEndpoint = false
		return setDNSClusterType(cl, cfg)
	}
	return nil
}

func dnsClusterFromLegacy(cl *clusterv3.Cluster) *dnsv3.DnsCluster {
	cfg := &dnsv3.DnsCluster{
		DnsRefreshRate:         cl.GetDnsRefreshRate(),
		RespectDnsTtl:          cl.GetRespectDnsTtl(),
		DnsJitter:              cl.GetDnsJitter(),
		TypedDnsResolverConfig: cl.GetTypedDnsResolverConfig(),
		DnsLookupFamily:        legacyLookupFamilies[cl.GetDnsLookupFamily()],
	}
	if r := cl.GetDnsFailureRefreshRate(); r != nil {
		cfg.DnsFailureRefreshRate = &dnsv3.DnsCluster_RefreshRate{
			BaseInterval: r.GetBaseInterval(),
			MaxInterval:  r.GetMaxInterval(),
		}
	}
	cl.DnsRefreshRate = nil
	cl.RespectDnsTtl = false
	cl.DnsJitter = nil
	cl.TypedDnsResolverConfig = nil
	cl.DnsLookupFamily = clusterv3.Cluster_AUTO
	cl.DnsFailureRefreshRate = nil
	return cfg
}

var legacyLookupFamilies = map[clusterv3.Cluster_DnsLookupFamily]commondnsv3.DnsLookupFamily{
	clusterv3.Cluster_AUTO:         commondnsv3.DnsLookupFamily_AUTO,
	clusterv3.Cluster_V4_ONLY:      commondnsv3.DnsLookupFamily_V4_ONLY,
	clusterv3.Cluster_V6_ONLY:      commondnsv3.DnsLookupFamily_V6_ONLY,
	clusterv3.Cluster_V4_PREFERRED: commondnsv3.DnsLookupFamily_V4_PREFERRED,
	clusterv3.Cluster_ALL:          commondnsv3.DnsLookupFamily_ALL,
}

func setDNSClusterType(cl *clusterv3.Cluster, cfg *dnsv3.DnsCluster) error {
	packed, err := anypb.New(cfg)
	if err != nil {
		return err
	}
	cl.ClusterDiscoveryType = &clusterv3.Cluster_ClusterType{ClusterType: &clusterv3.Cluster_CustomClusterType{
		Name:        dnsClusterType,
		TypedConfig: packed,
	}}
	return nil
}

func addUpstreamHTTPFilter(cl *clusterv3.Cluster, filter *hcmv3.HttpFilter) (bool, error) {
	opts := &httpv3.HttpProtocolOptions{}
	if existing, ok := cl.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey]; ok {
		if err := existing.UnmarshalTo(opts); err != nil {
			return false, fmt.Errorf("decode %s: %w", httpProtocolOptionsKey, err)
		}
	}
	if opts.GetUpstreamProtocolOptions() == nil {
		opts.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{
					HttpProtocolOptions: &corev3.Http1ProtocolOptions{},
				},
			},
		}
	}
	for _, f := range opts.GetHttpFilters() {
		if f.GetName() == filter.GetName() {
			return false, nil
		}
	}
	if len(opts.GetHttpFilters()) == 0 {
		codec, err := anypb.New(&upstreamcodecv3.UpstreamCodec{})
		if err != nil {
			return false, err
		}
		opts.HttpFilters = []*hcmv3.HttpFilter{{
			Name:       upstreamCodecFilterName,
			ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: codec},
		}}
	}
	opts.HttpFilters = append([]*hcmv3.HttpFilter{filter}, opts.HttpFilters...)

	packed, err := anypb.New(opts)
	if err != nil {
		return false, err
	}
	if cl.TypedExtensionProtocolOptions == nil {
		cl.TypedExtensionProtocolOptions = map[string]*anypb.Any{}
	}
	cl.TypedExtensionProtocolOptions[httpProtocolOptionsKey] = packed
	return true, nil
}
