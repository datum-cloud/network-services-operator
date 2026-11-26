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

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
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

func TestPostTranslateModify_UpstreamAddressGuardCoversVPCNameResolvingClusters(t *testing.T) {
	const (
		dsNS      = "ns-vpc"
		proxyName = "vpc-proxy"
		name      = "httproute/" + dsNS + "/" + proxyName + "/rule/0"
		host      = "127-0-0-1.nip.io"
	)
	legacy := func() *clusterv3.Cluster {
		cl := tlsDNSCluster(name, host)
		cl.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STRICT_DNS}
		return cl
	}
	tests := []struct {
		name    string
		cluster func() *clusterv3.Cluster
	}{
		{name: "dns cluster type", cluster: func() *clusterv3.Cluster { return tlsDNSCluster(name, host) }},
		{name: "legacy strict dns", cluster: legacy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proxy := &networkingv1alpha.HTTPProxy{
				ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: dsNS},
				Spec: networkingv1alpha.HTTPProxySpec{Rules: []networkingv1alpha.HTTPProxyRule{{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "svc", Port: "http"}},
						{Endpoint: "http://" + host + ":19000"},
					},
				}}},
			}
			cl := fake.NewClientBuilder().WithScheme(testServerScheme(t)).
				WithObjects(tenantNamespace(dsNS), proxy).Build()
			cfg := testServerConfig()
			cfg.UpstreamAddressGuard = true
			srv := New(cl, cfg, discardLogger())

			resp, err := srv.PostTranslateModify(context.Background(), &pb.PostTranslateModifyRequest{
				Clusters: []*clusterv3.Cluster{tc.cluster()},
			})
			require.NoError(t, err)
			require.Len(t, resp.GetClusters(), 1)
			_, has := resp.GetClusters()[0].GetTypedExtensionProtocolOptions()["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
			assert.True(t, has)
		})
	}
}
