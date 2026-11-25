package server

import (
	"context"
	"testing"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	dnsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/dns/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"go.datum.net/network-services-operator/internal/downstreamclient"
)

func TestPostTranslateModify_UpstreamAddressGuard(t *testing.T) {
	const clusterName = "httproute/ns-tenant/origin/rule/0"
	tests := []struct {
		name    string
		enabled bool
		guarded bool
	}{
		{name: "enabled", enabled: true, guarded: true},
		{name: "disabled", enabled: false, guarded: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name:   "ns-tenant",
				Labels: map[string]string{downstreamclient.UpstreamOwnerNamespaceLabel: "default"},
			}}
			cl := fake.NewClientBuilder().WithScheme(testServerScheme(t)).WithObjects(ns).Build()
			cfg := testServerConfig()
			cfg.UpstreamAddressGuard = tc.enabled
			srv := New(cl, cfg, discardLogger())

			req := &pb.PostTranslateModifyRequest{Clusters: []*clusterv3.Cluster{{
				Name: clusterName,
				ClusterDiscoveryType: &clusterv3.Cluster_ClusterType{
					ClusterType: &clusterv3.Cluster_CustomClusterType{
						Name:        "envoy.cluster.dns",
						TypedConfig: mustAny(&dnsv3.DnsCluster{}),
					},
				},
			}}}
			resp, err := srv.PostTranslateModify(context.Background(), req)
			require.NoError(t, err)
			require.Len(t, resp.GetClusters(), 1)
			_, has := resp.GetClusters()[0].GetTypedExtensionProtocolOptions()["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
			assert.Equal(t, tc.guarded, has)
		})
	}
}
