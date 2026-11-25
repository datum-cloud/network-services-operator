package mutate

import (
	"net/netip"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

var tenantClusterKinds = map[string]struct{}{
	"httproute":      {},
	"securitypolicy": {},
}

func ResolveUpstreamNamesAbsolute(clusters []*clusterv3.Cluster, idx *extcache.PolicyIndex) int {
	mutated := 0
	for _, cl := range clusters {
		if !resolvesNames(cl) {
			continue
		}
		if !isTenantCluster(cl.GetName(), idx) {
			continue
		}
		changed := false
		for _, locality := range cl.GetLoadAssignment().GetEndpoints() {
			for _, lbEndpoint := range locality.GetLbEndpoints() {
				endpoint := lbEndpoint.GetEndpoint()
				addr := endpoint.GetAddress().GetSocketAddress()
				if addr == nil || !isRelativeName(addr.GetAddress()) {
					continue
				}
				if endpoint.GetHostname() == "" {
					endpoint.Hostname = addr.GetAddress()
				}
				addr.Address += "."
				changed = true
			}
		}
		if changed {
			mutated++
		}
	}
	return mutated
}

var nameResolvingClusterTypes = map[string]bool{
	"envoy.cluster.dns":         true,
	"envoy.cluster.strict_dns":  true,
	"envoy.cluster.logical_dns": true,
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

func isTenantCluster(name string, idx *extcache.PolicyIndex) bool {
	if idx == nil {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) < 3 {
		return false
	}
	if _, ok := tenantClusterKinds[parts[0]]; !ok {
		return false
	}
	_, ok := idx.TenantNamespaces[parts[1]]
	return ok
}

func isRelativeName(host string) bool {
	if host == "" || strings.HasSuffix(host, ".") {
		return false
	}
	_, err := netip.ParseAddr(host)
	return err != nil
}
