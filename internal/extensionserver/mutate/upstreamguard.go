package mutate

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	rbacv3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	rbacfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
	upstreamcodecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	upstreamipportv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/rbac/matchers/upstream_ip_port/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

const (
	UpstreamAddressGuardFilterName = "envoy.filters.http.upstream_rbac"
	upstreamAddressGuardPolicyName = "non-public-address"
	upstreamIPPortMatcherName      = "envoy.rbac.matchers.upstream_ip_port"
	upstreamCodecFilterName        = "envoy.filters.http.upstream_codec"
	httpProtocolOptionsKey         = "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"
)

var nameResolvingClusterTypes = map[string]bool{
	"envoy.cluster.dns":                    true,
	"envoy.cluster.strict_dns":             true,
	"envoy.cluster.logical_dns":            true,
	"envoy.clusters.dynamic_forward_proxy": true,
}

var nonPublicUpstreamPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::ffff:0:0/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
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
		added, err := addUpstreamHTTPFilter(cl, filter)
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
	if !resolvesNames(cl) {
		return false
	}
	ns, proxy, rule, backend, ok := parseTenantRouteClusterName(cl.GetName())
	if !ok {
		return false
	}
	if _, tenant := idx.TenantNamespaces[ns]; !tenant {
		return false
	}
	if _, connector := idx.Connectors[extcache.ConnectorKey{Namespace: ns, HTTPProxyName: proxy, RuleIndex: rule}]; connector {
		return false
	}
	if backend == ruleCluster {
		_, vpc := idx.VPCPods[extcache.VPCPodKey{Namespace: ns, HTTPProxyName: proxy, RuleIndex: rule}]
		return !vpc
	}
	_, vpc := idx.VPCPodBackends[extcache.VPCPodBackendKey{
		Namespace: ns, HTTPProxyName: proxy, RuleIndex: rule, BackendIndex: backend,
	}]
	return !vpc
}

func resolvesNames(cl *clusterv3.Cluster) bool {
	if ct := cl.GetClusterType(); ct != nil {
		return nameResolvingClusterTypes[ct.GetName()]
	}
	switch cl.GetType() {
	case clusterv3.Cluster_STRICT_DNS, clusterv3.Cluster_LOGICAL_DNS:
		return true
	}
	return false
}

func parseTenantRouteClusterName(name string) (ns, route string, rule, backend int, ok bool) {
	kind, rest, found := strings.Cut(name, "/")
	if !found || (kind != "httproute" && kind != "grpcroute") {
		return "", "", 0, 0, false
	}
	parts := strings.Split(rest, "/")
	if (len(parts) != 4 && len(parts) != 6) || parts[2] != "rule" {
		return "", "", 0, 0, false
	}
	rule, err := strconv.Atoi(parts[3])
	if err != nil || rule < 0 {
		return "", "", 0, 0, false
	}
	backend = ruleCluster
	if len(parts) == 6 {
		if parts[4] != "backend" {
			return "", "", 0, 0, false
		}
		backend, err = strconv.Atoi(parts[5])
		if err != nil || backend < 0 {
			return "", "", 0, 0, false
		}
	}
	return parts[0], parts[1], rule, backend, true
}

func upstreamAddressGuardFilter() (*hcmv3.HttpFilter, error) {
	permissions := make([]*rbacv3.Permission, 0, len(nonPublicUpstreamPrefixes))
	for _, p := range nonPublicUpstreamPrefixes {
		matcher, err := anypb.New(&upstreamipportv3.UpstreamIpPortMatcher{
			UpstreamIp: &corev3.CidrRange{
				AddressPrefix: p.Addr().String(),
				PrefixLen:     wrapperspb.UInt32(uint32(p.Bits())),
			},
		})
		if err != nil {
			return nil, err
		}
		permissions = append(permissions, &rbacv3.Permission{
			Rule: &rbacv3.Permission_Matcher{Matcher: &corev3.TypedExtensionConfig{
				Name:        upstreamIPPortMatcherName,
				TypedConfig: matcher,
			}},
		})
	}
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

func addUpstreamHTTPFilter(cl *clusterv3.Cluster, filter *hcmv3.HttpFilter) (bool, error) {
	if cl.GetHttpProtocolOptions() != nil || cl.GetHttp2ProtocolOptions() != nil ||
		cl.GetCommonHttpProtocolOptions() != nil || cl.GetUpstreamHttpProtocolOptions() != nil ||
		cl.GetProtocolSelection() != clusterv3.Cluster_USE_CONFIGURED_PROTOCOL {
		return false, fmt.Errorf("cluster sets deprecated HTTP protocol fields")
	}

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
