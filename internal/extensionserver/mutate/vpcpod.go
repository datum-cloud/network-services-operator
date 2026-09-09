package mutate

import (
	"encoding/base64"
	"fmt"
	"strings"

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

		device, ok := vrfDeviceName(info.TenantID)
		if !ok {
			// Tenant identifier doesn't parse as a galactic-cni
			// vpc-vpcAttachment pair — same treatment as an empty TenantID
			// above: never guess a device name, since binding to the wrong
			// one is a silent cross-tenant blackhole, not a visible error.
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

// vrfDeviceName derives the VRF device name #855's sidecar creates for a
// tenant, from the tenant-id label value galactic-cni (#854) publishes on
// the backend's EndpointSlice.
//
// That value is galactic's crdnames.TenantIdentifier(vpc, vpcAttachment) —
// an unencoded "<vpc>-<vpcAttachment>" join (galactic
// internal/crdnames/crdnames.go). But the VRF itself is per-VPC-per-node,
// shared by every attachment on that VPC on a given node, not per
// attachment (galactic internal/plumbing/intf.go's
// vrfInterfaceNameTemplate doc comment) — so only the vpc half feeds the
// device name, recovered the same way galactic's own
// crdnames.ParseTenantIdentifier does: split on the first "-" (vpc and
// vpcAttachment are both base62 and so never contain the separator
// themselves).
//
// The name itself must exactly match
// intf.GenerateInterfaceNameVRF(vpc) — "G" + vpc zero-padded to 9
// characters + "V" (galactic internal/ingresssidecar/backend.go's
// vrfNameRegex: `^G([A-Za-z0-9]{9})V$`) — or the socket bind resolves to a
// device the sidecar never created. This format is always exactly 11
// bytes, comfortably inside the IFNAMSIZ-1 (15 byte) limit
// SO_BINDTODEVICE enforces, provided vpc itself is no longer than 9 base62
// characters — true of every vpc identifier this fabric allocates today.
//
// Returns ok=false for a tenant identifier that doesn't parse as a
// vpc-vpcAttachment pair, or whose vpc half is too long to fit the
// template — callers must never bind to a guessed device name.
func vrfDeviceName(tenantID string) (device string, ok bool) {
	vpc, _, found := strings.Cut(tenantID, "-")
	if !found || vpc == "" || len(vpc) > 9 {
		return "", false
	}
	return fmt.Sprintf("G%09sV", vpc), true
}
