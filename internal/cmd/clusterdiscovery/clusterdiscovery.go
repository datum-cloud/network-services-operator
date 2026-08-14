package clusterdiscovery

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcsingle "sigs.k8s.io/multicluster-runtime/providers/single"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"
	milomulticluster "go.miloapis.com/milo/pkg/multicluster-runtime/milo"

	"go.datum.net/network-services-operator/internal/config"
)

// Initialize builds the multicluster provider the manager discovers clusters
// through.
func Initialize(
	discovery config.DiscoveryConfig,
	projectClient config.ClientConnectionConfig,
	deploymentCluster cluster.Cluster,
	scheme *runtime.Scheme,
) (runnables []manager.Runnable, provider multicluster.Provider, err error) {
	runnables = append(runnables, deploymentCluster)
	switch discovery.Mode {
	case multiclusterproviders.ProviderSingle:
		// mcsingle implements multicluster.ProviderRunnable; mgr.Start engages
		// the cluster and blocks for us — no wrapper needed.
		provider = mcsingle.New("single", deploymentCluster)

	case multiclusterproviders.ProviderMilo:
		discoveryRestConfig, err := discovery.DiscoveryRestConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("unable to get discovery rest config: %w", err)
		}
		projectClient.ApplyTo(discoveryRestConfig)

		projectRestConfig, err := discovery.ProjectRestConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("unable to get project rest config: %w", err)
		}
		projectClient.ApplyTo(projectRestConfig)

		discoveryManager, err := manager.New(discoveryRestConfig, manager.Options{
			Client: client.Options{
				Cache: &client.CacheOptions{
					Unstructured: true,
				},
			},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("unable to set up overall controller manager: %w", err)
		}

		provider, err = milomulticluster.New(discoveryManager, milomulticluster.Options{
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.Scheme = scheme
				},
			},
			InternalServiceDiscovery: discovery.InternalServiceDiscovery,
			ProjectRestConfig:        projectRestConfig,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("unable to create datum project provider: %w", err)
		}

		runnables = append(runnables, discoveryManager)

	// case providers.ProviderKind:
	// 	provider = mckind.New(mckind.Options{
	// 		ClusterOptions: []cluster.Option{
	// 			func(o *cluster.Options) {
	// 				o.Scheme = scheme
	// 			},
	// 		},
	// 	})

	default:
		return nil, nil, fmt.Errorf(
			"unsupported cluster discovery mode %s",
			discovery.Mode,
		)
	}

	return runnables, provider, nil
}
