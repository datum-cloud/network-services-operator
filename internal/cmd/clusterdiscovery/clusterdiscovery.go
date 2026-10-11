package clusterdiscovery

import (
	"errors"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcsingle "sigs.k8s.io/multicluster-runtime/providers/single"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"
	milomulticluster "go.miloapis.com/milo/pkg/multicluster-runtime/milo"

	"go.datum.net/network-services-operator/internal/config"
)

// Initialize builds the multicluster provider the manager discovers clusters
// through, and the setup to run on the manager once it exists.
func Initialize(
	discovery config.DiscoveryConfig,
	projectClient config.ClientConnectionConfig,
	deploymentCluster cluster.Cluster,
	scheme *runtime.Scheme,
) (runnables []manager.Runnable, provider multicluster.Provider, setup func(mcmanager.Manager) error, err error) {
	runnables = append(runnables, deploymentCluster)
	setup = func(mcmanager.Manager) error { return nil }
	switch discovery.Mode {
	case multiclusterproviders.ProviderSingle:
		// mcsingle implements multicluster.ProviderRunnable; mgr.Start engages
		// the cluster and blocks for us — no wrapper needed.
		provider = mcsingle.New("single", deploymentCluster)

	case multiclusterproviders.ProviderMilo:
		discoveryRestConfig, err := discovery.DiscoveryRestConfig()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to get discovery rest config: %w", err)
		}
		projectClient.ApplyTo(discoveryRestConfig)

		projectRestConfig, err := discovery.ProjectRestConfig()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to get project rest config: %w", err)
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
			return nil, nil, nil, fmt.Errorf("unable to set up overall controller manager: %w", err)
		}

		miloProvider, err := milomulticluster.New(discoveryManager, milomulticluster.Options{
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.Scheme = scheme
				},
			},
			InternalServiceDiscovery: discovery.InternalServiceDiscovery,
			ProjectRestConfig:        projectRestConfig,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create datum project provider: %w", err)
		}

		// Every replica serves the webhooks, which read a project through the
		// provider, so the provider runs on every replica. Started by the
		// manager it would run on the elected leader only, and the readiness
		// check below would then hold every other replica unready.
		provider = milomulticluster.WithoutAutoStart(miloProvider)
		setup = func(mgr mcmanager.Manager) error {
			if err := milomulticluster.EngageAlways(mgr, miloProvider); err != nil {
				return fmt.Errorf("unable to start the datum project provider on every replica: %w", err)
			}
			return mgr.AddReadyzCheck("projects", projectsRegistered(miloProvider))
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
		return nil, nil, nil, fmt.Errorf(
			"unsupported cluster discovery mode %s",
			discovery.Mode,
		)
	}

	return runnables, provider, setup, nil
}

// projectsRegistered reports ready once the provider has taken on every
// project it found at start, so a new replica does not answer admission
// requests for a project it cannot reach yet.
func projectsRegistered(provider interface{ HasSynced() bool }) healthz.Checker {
	return func(*http.Request) error {
		if !provider.HasSynced() {
			return errors.New("projects found at start are not all registered yet")
		}
		return nil
	}
}
