package mutate

import (
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"google.golang.org/protobuf/types/known/structpb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// upstream_* metadata field names written into a cluster's datum-gateway
// filter_metadata. The access log format reads them back with
// %CLUSTER_METADATA(datum-gateway:upstream_kind)% and its siblings.
const (
	upstreamAPIGroupField = "upstream_apigroup"
	upstreamKindField     = "upstream_kind"
	upstreamNameField     = "upstream_name"
)

// ApplyUpstreamMetadata writes the consumer resource a rule's backend is
// attached to into that rule's Envoy cluster metadata, so the access log can
// report the upstream a request was proxied to. The reference is read from the
// attached-to labels the HTTPProxy controller stamps on the rule's
// EndpointSlice, carried through the policy index, and stamped into
// cluster.Metadata.FilterMetadata["datum-gateway"] here.
//
// Clusters are matched by name using the same
// "httproute/<dsNS>/<proxyName>/rule/<idx>" pattern the connector and vpcPod
// families rely on. A rule whose members disagree on their upstream, or that no
// label set reached, has no index entry and is left unstamped: the access log
// then renders the field empty rather than naming one member's upstream for a
// request another member served.
//
// Returns the number of clusters mutated.
func ApplyUpstreamMetadata(clusters []*clusterv3.Cluster, idx *extcache.PolicyIndex) (mutated int) {
	for _, cl := range clusters {
		dsNS, proxyName, ruleIndex, ok := parseConnectorClusterName(cl.GetName())
		if !ok {
			continue
		}

		upstreamNS, ok := idx.DStoUS[dsNS]
		if !ok {
			continue
		}

		ref, ok := idx.UpstreamRefs[extcache.UpstreamRefKey{
			UpstreamNS:    upstreamNS,
			HTTPProxyName: proxyName,
			RuleIndex:     ruleIndex,
		}]
		if !ok {
			continue
		}

		injectClusterUpstreamMetadata(cl, ref)
		mutated++
	}
	return mutated
}

// injectClusterUpstreamMetadata writes the three upstream_* fields into a
// cluster's datum-gateway filter_metadata, creating the metadata and the
// namespace struct when absent and leaving every other field untouched. It
// mirrors injectProjectNameMetadata in tpp.go, which does the same for a route.
func injectClusterUpstreamMetadata(cl *clusterv3.Cluster, ref extcache.UpstreamRef) {
	if cl.Metadata == nil {
		cl.Metadata = &corev3.Metadata{}
	}
	if cl.Metadata.FilterMetadata == nil {
		cl.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
	}

	fields := map[string]any{
		upstreamAPIGroupField: ref.APIGroup,
		upstreamKindField:     ref.Kind,
		upstreamNameField:     ref.Name,
	}

	existing := cl.Metadata.FilterMetadata[datumGatewayMetadataKey]
	if existing == nil {
		s, _ := structpb.NewStruct(fields)
		cl.Metadata.FilterMetadata[datumGatewayMetadataKey] = s
		return
	}
	if existing.Fields == nil {
		existing.Fields = make(map[string]*structpb.Value)
	}
	for k, v := range fields {
		existing.Fields[k] = structpb.NewStringValue(v.(string))
	}
}
