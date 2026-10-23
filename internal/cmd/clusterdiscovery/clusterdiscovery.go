package clusterdiscovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcsingle "sigs.k8s.io/multicluster-runtime/providers/single"

	infrastructurev1alpha1 "go.miloapis.com/milo/pkg/apis/infrastructure/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"
	milomulticluster "go.miloapis.com/milo/pkg/multicluster-runtime/milo"

	"go.datum.net/network-services-operator/internal/config"
)

// CheckRestConfigs loads the kubeconfigs Initialize needs for discovery.
func CheckRestConfigs(discovery config.DiscoveryConfig) error {
	if discovery.Mode != multiclusterproviders.ProviderMilo {
		return nil
	}
	if _, err := discovery.DiscoveryRestConfig(); err != nil {
		return fmt.Errorf("unable to get discovery rest config: %w", err)
	}
	if _, err := discovery.ProjectRestConfig(); err != nil {
		return fmt.Errorf("unable to get project rest config: %w", err)
	}
	return nil
}

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
		provider = projectProvider{
			Provider: milomulticluster.WithoutAutoStart(miloProvider),
			ready:    projectReady(discoveryManager.GetClient(), discovery.InternalServiceDiscovery),
		}
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

// projectProvider is the Milo provider as the manager is given it, which can
// also tell a project that is gone from one it has not registered yet: both
// are not found.
type projectProvider struct {
	multicluster.Provider
	ready func(ctx context.Context, project string) (bool, error)
}

// ProjectReady reports whether a project still exists and is ready.
func (p projectProvider) ProjectReady(ctx context.Context, project string) (bool, error) {
	return p.ready(ctx, project)
}

// projectReady reads a project the way the Milo provider decides to register
// it and to let it go: the object exists and its readiness condition is true.
// It reads through the client of the provider's own cache.
func projectReady(reader client.Reader, internalServiceDiscovery bool) func(context.Context, string) (bool, error) {
	gvk, conditionType := resourcemanagerv1alpha1.GroupVersion.WithKind("Project"), "Ready"
	if internalServiceDiscovery {
		gvk, conditionType = infrastructurev1alpha1.GroupVersion.WithKind("ProjectControlPlane"), "ControlPlaneReady"
	}
	return func(ctx context.Context, name string) (bool, error) {
		var project unstructured.Unstructured
		project.SetGroupVersionKind(gvk)
		if err := reader.Get(ctx, client.ObjectKey{Name: name}, &project); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		var status struct {
			Conditions []metav1.Condition `json:"conditions"`
		}
		if raw, ok := project.Object["status"].(map[string]any); ok {
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &status); err != nil {
				return false, fmt.Errorf("reading the conditions of %s %q: %w", gvk.Kind, name, err)
			}
		}
		return apimeta.IsStatusConditionTrue(status.Conditions, conditionType), nil
	}
}
