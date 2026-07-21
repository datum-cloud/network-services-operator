// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

func TestConnectorEngagesClusterWithoutLeaseDiscovery(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	log.SetLogger(zap.New(zap.UseDevMode(true)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	provider := &staticProvider{clusters: map[string]cluster.Cluster{}}

	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	require.NoError(t, err)

	const clusterName = "no-coordination"
	noLease := &clusterWithoutLeaseDiscovery{Cluster: mgr.GetLocalManager()}
	provider.clusters[clusterName] = noLease

	directClient, err := client.New(cfg, client.Options{Scheme: testScheme})
	require.NoError(t, err)

	connectorKey := client.ObjectKey{Namespace: "default", Name: "connector"}
	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: connectorKey.Name, Namespace: connectorKey.Namespace},
		Spec:       networkingv1alpha1.ConnectorSpec{ConnectorClassName: "missing"},
	}
	require.NoError(t, directClient.Create(ctx, connector))

	require.NoError(t, (&ConnectorReconciler{}).SetupWithManager(mgr))

	require.NoError(t, mgr.Engage(ctx, clusterName, noLease))

	startErr := make(chan error, 1)
	go func() { startErr <- mgr.Start(ctx) }()

	deadline := time.After(60 * time.Second)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-startErr:
			require.NoError(t, err, "manager exited before reconciling the connector")
			t.Fatal("manager stopped before reconciling the connector")
		case <-deadline:
			t.Fatal("timed out waiting for the connector to be reconciled on a cluster without Lease discovery")
		case <-tick.C:
			var got networkingv1alpha1.Connector
			if err := directClient.Get(ctx, connectorKey, &got); err != nil {
				continue
			}
			if apimeta.FindStatusCondition(got.Status.Conditions, networkingv1alpha1.ConnectorConditionAccepted) != nil {
				return
			}
		}
	}
}

type staticProvider struct {
	clusters map[string]cluster.Cluster
}

func (p *staticProvider) Get(_ context.Context, name multicluster.ClusterName) (cluster.Cluster, error) {
	if cl, ok := p.clusters[string(name)]; ok {
		return cl, nil
	}
	return nil, fmt.Errorf("cluster %q not found", name)
}

func (p *staticProvider) IndexField(context.Context, client.Object, string, client.IndexerFunc) error {
	return nil
}

type clusterWithoutLeaseDiscovery struct {
	cluster.Cluster
}

func (c *clusterWithoutLeaseDiscovery) GetCache() cache.Cache {
	return &cacheWithoutLease{Cache: c.Cluster.GetCache()}
}

func (c *clusterWithoutLeaseDiscovery) GetRESTMapper() apimeta.RESTMapper {
	return &restMapperWithoutLease{RESTMapper: c.Cluster.GetRESTMapper()}
}

type cacheWithoutLease struct {
	cache.Cache
}

func (c *cacheWithoutLease) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	if _, ok := obj.(*coordinationv1.Lease); ok {
		return nil, noLeaseMatchError()
	}
	return c.Cache.GetInformer(ctx, obj, opts...)
}

type restMapperWithoutLease struct {
	apimeta.RESTMapper
}

func (m *restMapperWithoutLease) RESTMapping(gk schema.GroupKind, versions ...string) (*apimeta.RESTMapping, error) {
	if gk == coordinationLeaseGroupKind {
		return nil, noLeaseMatchError()
	}
	return m.RESTMapper.RESTMapping(gk, versions...)
}

func (m *restMapperWithoutLease) RESTMappings(gk schema.GroupKind, versions ...string) ([]*apimeta.RESTMapping, error) {
	if gk == coordinationLeaseGroupKind {
		return nil, noLeaseMatchError()
	}
	return m.RESTMapper.RESTMappings(gk, versions...)
}

func noLeaseMatchError() error {
	return &apimeta.NoKindMatchError{
		GroupKind:        coordinationLeaseGroupKind,
		SearchedVersions: []string{coordinationv1.SchemeGroupVersion.Version},
	}
}
