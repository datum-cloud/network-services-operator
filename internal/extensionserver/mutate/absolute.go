package mutate

import (
	"net/netip"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
)

func ResolveUpstreamNamesAbsolute(clusters []*clusterv3.Cluster) int {
	mutated := 0
	for _, cl := range clusters {
		if cl.GetType() != clusterv3.Cluster_STRICT_DNS && cl.GetType() != clusterv3.Cluster_LOGICAL_DNS {
			continue
		}
		changed := false
		for _, locality := range cl.GetLoadAssignment().GetEndpoints() {
			for _, lbEndpoint := range locality.GetLbEndpoints() {
				addr := lbEndpoint.GetEndpoint().GetAddress().GetSocketAddress()
				if addr == nil || !isRelativeName(addr.GetAddress()) {
					continue
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

func isRelativeName(host string) bool {
	if host == "" || strings.HasSuffix(host, ".") {
		return false
	}
	_, err := netip.ParseAddr(host)
	return err != nil
}
