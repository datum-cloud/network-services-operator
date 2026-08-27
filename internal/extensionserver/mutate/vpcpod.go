package mutate

import (
	"encoding/base64"
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
// cluster whose HTTPProxy rule resolves to a backend on a tenant VPC —
// instance or networkService — binding Envoy's outbound socket to that
// tenant's VRF device via SO_BINDTODEVICE.
//
// SO_BINDTODEVICE is the kernel's own documented mechanism for making a
// VRF-unaware process participate in a specific VRF (see Documentation/
// networking/vrf.txt).
//
// Envoy's only job here is to leave the default network namespace by the
// right door. galactic's ingress sidecar, running alongside Envoy, owns
// everything past that: it creates the VRF device and installs the SRv6
// encapsulation routes into it from the same EndpointSlices this binding is
// resolved through. Envoy never sees, parses, or carries a segment
// identifier. Clusters are matched by name using the same
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

		device := vrfDeviceName(info.TenantID)
		if device == "" {
			// Tenant identifier galactic would never have produced, so no
			// device answers to the name it implies. Binding to a name the
			// kernel cannot resolve fails every connection on this cluster,
			// which is strictly worse than leaving it unbound.
			continue
		}

		if bindErr := applyVPCPodBindConfig(cl, device); bindErr != nil {
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

// maxInterfaceNameLen is IFNAMSIZ-1, the longest device name the kernel will
// resolve for SO_BINDTODEVICE.
const maxInterfaceNameLen = 15

// vrfDeviceName returns the name of the kernel VRF device galactic's ingress
// sidecar creates for a tenant: "G", the base62 VPC zero-padded to nine
// characters, then "V". This must stay byte-identical to
// intf.GenerateInterfaceNameVRF in datum-cloud/galactic — the sidecar creates
// the device, Envoy only binds to it, and a name that differs by one
// character resolves nothing and times out every connection.
//
// The name is keyed on the VPC alone, never the full "<vpc>-<vpcAttachment>"
// tenant identifier: galactic shares one device across every attachment of a
// VPC on a node.
//
// Returns "" for a tenant identifier galactic could not have produced, or one
// whose VPC half overflows the interface-name limit.
func vrfDeviceName(tenantID string) string {
	vpc, ok := extcache.TenantVPC(tenantID)
	if !ok {
		return ""
	}

	name := fmt.Sprintf("G%09s%s", vpc, "V")
	if len(name) > maxInterfaceNameLen {
		return ""
	}
	return name
}
