package mutate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"google.golang.org/protobuf/encoding/protojson"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// SOL_SOCKET / SO_BINDTODEVICE per Linux's socket(7). go-control-plane's
// SocketOption carries these as opaque int64 level/name values, same as a
// raw setsockopt(2) call would — there's no first-class "bind to VRF" field
// anywhere in the Envoy API, and none is needed.
const (
	soLevelSocket      = 1  // SOL_SOCKET
	soNameBindToDevice = 25 // SO_BINDTODEVICE
)

// ApplyVPCPodSocketBind patches upstream_bind_config.socket_options onto any
// cluster whose HTTPProxy rule resolves to a vpcPod backend, binding Envoy's
// outbound socket to the tenant's VRF device (managed by the #855 sidecar)
// via SO_BINDTODEVICE — the kernel's own documented mechanism for making a
// VRF-unaware process participate in a specific VRF (see Documentation/
// networking/vrf.txt). Clusters are matched by name using the same
// "httproute/<dsNS>/<proxyName>/rule/<idx>" pattern parseConnectorClusterName
// already relies on for connector clusters — EG names every HTTPRoute-rule
// cluster this way, not just connector ones.
//
// Returns the number of clusters mutated.
func ApplyVPCPodSocketBind(clusters []*clusterv3.Cluster, idx *extcache.PolicyIndex) (mutated int, err error) {
	for _, cl := range clusters {
		dsNS, proxyName, ruleIndex, ok := parseConnectorClusterName(cl.GetName())
		if !ok {
			continue
		}

		upstreamNS, ok := idx.DStoUS[dsNS]
		if !ok {
			continue
		}

		info, ok := idx.VPCPods[extcache.VPCPodKey{
			UpstreamNS:    upstreamNS,
			HTTPProxyName: proxyName,
			RuleIndex:     ruleIndex,
		}]
		if !ok || info.TenantID == "" {
			// No vpcPod backend on this rule, or the referenced EndpointSlice
			// was missing/unlabeled — never bind to a zero-value device name.
			continue
		}

		if bindErr := applyVPCPodBindConfig(cl, vrfDeviceName(info.TenantID)); bindErr != nil {
			return mutated, fmt.Errorf("apply vpcPod socket-bind for cluster %q: %w", cl.GetName(), bindErr)
		}
		mutated++
	}
	return mutated, nil
}

// applyVPCPodBindConfig sets upstream_bind_config on an existing cluster,
// leaving every other field Envoy Gateway already populated (load
// assignment, health checks, etc.) untouched. This is a targeted field
// assignment, unlike buildConnectorCluster in connector.go, which replaces
// the whole cluster wholesale.
//
// The BindConfig is unmarshaled into its own fresh message rather than
// merged into cl directly — protojson.Unmarshal resets its destination
// message before decoding (same as binary proto.Unmarshal), it does not
// merge into an already-populated one. Unmarshaling straight into cl would
// silently wipe every field EG had already set.
func applyVPCPodBindConfig(cl *clusterv3.Cluster, device string) error {
	bindConfigJSON := fmt.Sprintf(`{
  "socket_options": [{
    "level": %d,
    "name": %d,
    "buf_value": %q,
    "state": "STATE_PREBIND"
  }]
}`, soLevelSocket, soNameBindToDevice, base64.StdEncoding.EncodeToString([]byte(device)))

	bindConfig := &corev3.BindConfig{}
	if err := protojson.Unmarshal([]byte(bindConfigJSON), bindConfig); err != nil {
		return fmt.Errorf("unmarshal vpcPod bind config JSON: %w", err)
	}

	cl.UpstreamBindConfig = bindConfig
	return nil
}

// vrfDeviceName derives the VRF device name #855's sidecar is expected to
// create for a tenant. Bounded to IFNAMSIZ-1 (15 bytes) regardless of tenant
// ID length — SO_BINDTODEVICE silently fails to bind past that limit.
//
// TODO(#856): placeholder naming scheme, unconfirmed with #855. Must match
// their actual convention exactly or the socket bind resolves nothing.
func vrfDeviceName(tenantID string) string {
	sum := sha256.Sum256([]byte(tenantID))
	return "vrf-" + hex.EncodeToString(sum[:])[:11]
}
