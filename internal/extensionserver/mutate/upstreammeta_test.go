package mutate

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

func upstreamRefIndex(ref extcache.UpstreamRef) *extcache.PolicyIndex {
	return &extcache.PolicyIndex{
		DStoUS: map[string]string{testDSNS: testUpstreamNS},
		UpstreamRefs: map[extcache.UpstreamRefKey]extcache.UpstreamRef{
			{
				UpstreamNS:    testUpstreamNS,
				HTTPProxyName: testProxyName,
				RuleIndex:     0,
			}: ref,
		},
	}
}

func datumGatewayFields(t *testing.T, cl *clusterv3.Cluster) map[string]*structpb.Value {
	t.Helper()
	require.NotNil(t, cl.Metadata)
	s := cl.Metadata.FilterMetadata[datumGatewayMetadataKey]
	require.NotNil(t, s, "datum-gateway filter_metadata must be present")
	return s.Fields
}

func TestApplyUpstreamMetadata_StampsMatchingCluster(t *testing.T) {
	idx := upstreamRefIndex(extcache.UpstreamRef{
		APIGroup: "compute.datumapis.com",
		Kind:     "Instance",
		Name:     "web-0",
	})

	clusters := []*clusterv3.Cluster{
		{Name: testClusterName()},
		{Name: "infra-cluster"},
	}

	mutated := ApplyUpstreamMetadata(clusters, idx)
	assert.Equal(t, 1, mutated)

	fields := datumGatewayFields(t, clusters[0])
	assert.Equal(t, "compute.datumapis.com", fields[upstreamAPIGroupField].GetStringValue())
	assert.Equal(t, "Instance", fields[upstreamKindField].GetStringValue())
	assert.Equal(t, "web-0", fields[upstreamNameField].GetStringValue())

	assert.Nil(t, clusters[1].Metadata, "non-matching cluster must be untouched")
}

func TestApplyUpstreamMetadata_PreservesExistingDatumGatewayFields(t *testing.T) {
	idx := upstreamRefIndex(extcache.UpstreamRef{
		APIGroup: "compute.datumapis.com",
		Kind:     "Instance",
		Name:     "web-0",
	})

	existing, err := structpb.NewStruct(map[string]any{"project_name": "acme"})
	require.NoError(t, err)

	clusters := []*clusterv3.Cluster{{
		Name: testClusterName(),
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				datumGatewayMetadataKey: existing,
			},
		},
	}}

	mutated := ApplyUpstreamMetadata(clusters, idx)
	assert.Equal(t, 1, mutated)

	fields := datumGatewayFields(t, clusters[0])
	assert.Equal(t, "acme", fields["project_name"].GetStringValue())
	assert.Equal(t, "Instance", fields[upstreamKindField].GetStringValue())
}

func TestApplyUpstreamMetadata_SkipsWhenNoIndexEntry(t *testing.T) {
	idx := &extcache.PolicyIndex{
		DStoUS:       map[string]string{testDSNS: testUpstreamNS},
		UpstreamRefs: map[extcache.UpstreamRefKey]extcache.UpstreamRef{},
	}

	clusters := []*clusterv3.Cluster{{Name: testClusterName()}}

	mutated := ApplyUpstreamMetadata(clusters, idx)
	assert.Equal(t, 0, mutated)
	assert.Nil(t, clusters[0].Metadata, "a rule with no upstream reference is left unstamped")
}

func TestUpstreamRefFromLabels(t *testing.T) {
	ref, ok := extcache.UpstreamRefFromLabels(map[string]string{
		extcache.AttachedToGroupLabel: "compute.datumapis.com",
		extcache.AttachedToKindLabel:  "Instance",
		extcache.AttachedToNameLabel:  "web-0",
	})
	require.True(t, ok)
	assert.Equal(t, extcache.UpstreamRef{
		APIGroup: "compute.datumapis.com",
		Kind:     "Instance",
		Name:     "web-0",
	}, ref)

	_, ok = extcache.UpstreamRefFromLabels(map[string]string{
		extcache.AttachedToGroupLabel: "compute.datumapis.com",
		extcache.AttachedToKindLabel:  "Instance",
	})
	assert.False(t, ok, "a partial label set names no reference")

	_, ok = extcache.UpstreamRefFromLabels(nil)
	assert.False(t, ok)
}
