// Package managercmd implements the "manager" subcommand entrypoint.
// It is called from cmd/main.go when the first argument is "manager".
package managercmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	cmacmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1alpha3 "sigs.k8s.io/gateway-api/apis/v1alpha3"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/manager/coordinator/sharded"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/cmd/clusterdiscovery"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/controller"
	networkingwebhook "go.datum.net/network-services-operator/internal/webhook"
	networkinggatewayv1webhooks "go.datum.net/network-services-operator/internal/webhook/v1"
	networkingv1alphawebhooks "go.datum.net/network-services-operator/internal/webhook/v1alpha"
	webhookgatewayv1alpha1 "go.datum.net/network-services-operator/internal/webhook/v1alpha1"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	codecs   = serializer.NewCodecFactory(scheme, serializer.EnableStrict)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(config.AddToScheme(scheme))
	utilruntime.Must(config.RegisterDefaults(scheme))
	utilruntime.Must(networkingv1alpha.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
	utilruntime.Must(gatewayv1alpha2.Install(scheme))
	utilruntime.Must(gatewayv1alpha3.Install(scheme))
	utilruntime.Must(envoygatewayv1alpha1.AddToScheme(scheme))
	utilruntime.Must(networkingv1alpha1.AddToScheme(scheme))
	utilruntime.Must(cmacmev1.AddToScheme(scheme))
	utilruntime.Must(cmv1.AddToScheme(scheme))
	utilruntime.Must(dnsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ipamv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// BuildInfo holds version metadata injected at link time via ldflags.
type BuildInfo struct {
	Version      string
	GitCommit    string
	GitTreeState string
	BuildDate    string
}

// NewCommand builds the "manager" subcommand, which runs the
// network-services-operator controller manager.
func NewCommand(build BuildInfo) *cobra.Command {
	var enableLeaderElection bool
	var leaderElectionNamespace string
	var probeAddr string
	var enableClusterSharding bool
	var clusterShardingLeaseNamespace string
	var clusterShardingLeasePrefix string
	var clusterShardingPeerWeight uint
	var singletonControllersLeaderElection bool
	var singletonControllersLeaderElectionID string
	var leaderElectionID string

	var serverConfigFile string

	fs := flag.NewFlagSet("manager", flag.ContinueOnError)

	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.StringVar(&leaderElectionNamespace, "leader-elect-namespace", "", "The namespace to use for leader election.")
	fs.StringVar(
		&leaderElectionID,
		"leader-election-id",
		"",
		"Leader election ID for the controller manager. When empty, it is derived from the enabled controller sets.",
	)
	fs.BoolVar(
		&enableClusterSharding,
		"cluster-sharding-enabled",
		false,
		"Enable multicluster controller sharding via per-cluster coordination leases.",
	)
	fs.StringVar(
		&clusterShardingLeaseNamespace,
		"cluster-sharding-lease-namespace",
		"kube-system",
		"Namespace for controller cluster sharding leases.",
	)
	fs.StringVar(
		&clusterShardingLeasePrefix,
		"cluster-sharding-lease-prefix",
		"mcr-shard",
		"Lease name prefix for controller cluster sharding.",
	)
	fs.UintVar(
		&clusterShardingPeerWeight,
		"cluster-sharding-peer-weight",
		1,
		"Relative shard weight for this controller instance.",
	)
	fs.BoolVar(
		&singletonControllersLeaderElection,
		"singleton-controllers-leader-elect",
		true,
		"Enable leader election for singleton downstream controllers (Challenge and GatewayDownstreamCertificateSolver).",
	)
	fs.StringVar(
		&singletonControllersLeaderElectionID,
		"singleton-controllers-leader-election-id",
		"",
		"Leader election ID for singleton downstream controllers. When empty, it is derived from the enabled controller sets.",
	)

	opts := zap.Options{
		Development: true,
	}

	fs.StringVar(&serverConfigFile, "server-config", "", "path to the server config file")

	opts.BindFlags(fs)

	cmd := &cobra.Command{
		Use:   "manager",
		Short: "Run the network-services-operator controller manager",
		Args:  cobra.NoArgs,
		// nolint:gocyclo
		RunE: func(_ *cobra.Command, _ []string) error {
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

			setupLog.Info("starting network-services-operator",
				"version", build.Version,
				"gitCommit", build.GitCommit,
				"gitTreeState", build.GitTreeState,
				"buildDate", build.BuildDate,
			)

			var serverConfig config.NetworkServicesOperator
			var configData []byte
			if len(serverConfigFile) > 0 {
				var err error
				configData, err = os.ReadFile(serverConfigFile)
				if err != nil {
					setupLog.Error(fmt.Errorf("unable to read server config from %q", serverConfigFile), "")
					os.Exit(1)
				}
			}

			if err := runtime.DecodeInto(codecs.UniversalDecoder(), configData, &serverConfig); err != nil {
				setupLog.Error(err, "unable to decode server config")
				os.Exit(1)
			}

			// Allow overriding Redis URL at runtime via env var.
			if redisURL := strings.TrimSpace(os.Getenv("REDIS_URL")); redisURL != "" {
				serverConfig.Redis.URL = redisURL
				setupLog.Info("overriding redis.url from REDIS_URL")
			}

			setupLog.Info("server config", "config", serverConfig)

			if err := serverConfig.Validate(); err != nil {
				setupLog.Error(err, "invalid server config")
				os.Exit(1)
			}

			if leaderElectionID == "" {
				leaderElectionID = defaultLeaderElectionID
			}
			if singletonControllersLeaderElectionID == "" {
				singletonControllersLeaderElectionID = defaultLeaderElectionID + "-singleton"
			}

			cfg := ctrl.GetConfigOrDie()
			serverConfig.ControlPlaneClient.ApplyTo(cfg)

			deploymentCluster, err := cluster.New(cfg, func(o *cluster.Options) {
				o.Scheme = scheme
			})
			if err != nil {
				setupLog.Error(err, "failed creating local cluster")
				os.Exit(1)
			}

			runnables, provider, err := clusterdiscovery.Initialize(serverConfig.Discovery, serverConfig.ProjectClient, deploymentCluster, scheme)
			if err != nil {
				setupLog.Error(err, "unable to initialize cluster discovery")
				os.Exit(1)
			}

			setupLog.Info("cluster discovery mode", "mode", serverConfig.Discovery.Mode)

			ctx := ctrl.SetupSignalHandler()

			deploymentClusterClient := deploymentCluster.GetClient()

			metricsServerOptions := serverConfig.MetricsServer.Options(ctx, deploymentClusterClient)

			webhookServer := networkingwebhook.NewClusterAwareWebhookServer(
				webhook.NewServer(serverConfig.WebhookServer.Options(ctx, deploymentClusterClient)),
				serverConfig.Discovery.Mode,
			)

			leaseDuration := serverConfig.LeaderElection.LeaseDuration.Duration
			renewDeadline := serverConfig.LeaderElection.RenewDeadline.Duration
			retryPeriod := serverConfig.LeaderElection.RetryPeriod.Duration

			mcManagerOptions := []mcmanager.Option{}
			if enableClusterSharding {
				setupLog.Info(
					"enabling cluster sharding coordinator",
					"leaseNamespace",
					clusterShardingLeaseNamespace,
					"leasePrefix",
					clusterShardingLeasePrefix,
					"peerWeight",
					clusterShardingPeerWeight,
				)

				clusterShardingOptions := []sharded.Option{
					sharded.WithShardLease(clusterShardingLeaseNamespace, clusterShardingLeasePrefix),
					sharded.WithPerClusterLease(true),
				}
				if clusterShardingPeerWeight > 0 {
					clusterShardingOptions = append(
						clusterShardingOptions,
						sharded.WithPeerWeight(uint32(clusterShardingPeerWeight)),
					)
				}

				mcManagerOptions = append(
					mcManagerOptions,
					mcmanager.WithCoordinator(
						sharded.New(
							deploymentCluster.GetClient(),
							ctrl.Log.WithName("cluster-sharding-coordinator"),
							clusterShardingOptions...,
						),
					),
				)
			}

			primaryManagerLeaderElection := enableLeaderElection
			if enableClusterSharding && enableLeaderElection {
				setupLog.Info(
					"disabling primary manager leader election while cluster sharding is enabled",
					"singletonControllersLeaderElection",
					singletonControllersLeaderElection,
				)
				primaryManagerLeaderElection = false
			}

			mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
				Scheme:                  scheme,
				Metrics:                 metricsServerOptions,
				WebhookServer:           webhookServer,
				HealthProbeBindAddress:  probeAddr,
				LeaderElection:          primaryManagerLeaderElection,
				LeaderElectionID:        leaderElectionID,
				LeaderElectionNamespace: leaderElectionNamespace,
				LeaseDuration:           &leaseDuration,
				RenewDeadline:           &renewDeadline,
				RetryPeriod:             &retryPeriod,
				// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
				// when the Manager ends. This requires the binary to immediately end when the
				// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
				// speeds up voluntary leader transitions as the new leader don't have to wait
				// LeaseDuration time first.
				//
				// In the default scaffold provided, the program ends immediately after
				// the manager stops, so would be fine to enable this option. However,
				// if you are doing or is intended to do any operation such as perform cleanups
				// after the manager stops then its usage might be unsafe.
				// LeaderElectionReleaseOnCancel: true,
			}, mcManagerOptions...)
			if err != nil {
				setupLog.Error(err, "unable to start manager")
				os.Exit(1)
			}

			var downstreamCluster cluster.Cluster
			downstreamRestConfig, err := serverConfig.DownstreamResourceManagement.RestConfig()
			if err != nil {
				setupLog.Error(err, "unable to load control plane kubeconfig")
				os.Exit(1)
			}
			serverConfig.DownstreamClient.ApplyTo(downstreamRestConfig)

			downstreamCluster, err = cluster.New(downstreamRestConfig, func(o *cluster.Options) {
				o.Scheme = scheme
				o.Client = client.Options{
					Cache: &client.CacheOptions{
						Unstructured: true,
					},
				}
			})
			if err != nil {
				setupLog.Error(err, "failed to construct cluster")
				os.Exit(1)
			}

			var singletonMgr manager.Manager
			singletonControllerMgr := mgr.GetLocalManager()
			if enableClusterSharding {
				singletonMgr, err = manager.New(cfg, manager.Options{
					Scheme:                  scheme,
					Metrics:                 metricsserver.Options{BindAddress: "0"},
					WebhookServer:           webhook.NewServer(webhook.Options{Port: 0}),
					HealthProbeBindAddress:  "0",
					LeaderElection:          singletonControllersLeaderElection,
					LeaderElectionID:        singletonControllersLeaderElectionID,
					LeaderElectionNamespace: leaderElectionNamespace,
					LeaseDuration:           &leaseDuration,
					RenewDeadline:           &renewDeadline,
					RetryPeriod:             &retryPeriod,
				})
				if err != nil {
					setupLog.Error(err, "unable to create singleton controller manager")
					os.Exit(1)
				}
				singletonControllerMgr = singletonMgr
			}

			var irohDownstream cluster.Cluster
			if serverConfig.Connector.Iroh.DNSEnabled {
				irohRestCfg, err := serverConfig.Connector.Iroh.DownstreamRestConfig()
				if err != nil {
					setupLog.Error(err, "unable to load iroh dns downstream kubeconfig")
					os.Exit(1)
				}
				irohDownstream, err = cluster.New(irohRestCfg, func(o *cluster.Options) {
					o.Scheme = scheme
				})
				if err != nil {
					setupLog.Error(err, "unable to build iroh dns downstream cluster")
					os.Exit(1)
				}
			}

			ipamClients, err := newIPAMClientFactory(serverConfig.IPAM)
			if err != nil {
				setupLog.Error(err, "unable to build IPAM client factory")
				os.Exit(1)
			}

			registeredControllers, err := setupControllers(mgr, serverConfig, controllerDeps{
				downstreamCluster: downstreamCluster,
				singletonManager:  singletonControllerMgr,
				irohDownstream:    irohDownstream,
				ipamClients:       ipamClients,
			})
			if err != nil {
				setupLog.Error(err, "unable to set up controllers")
				os.Exit(1)
			}
			setupLog.Info("registered controllers", "controllers", registeredControllers)

			if err := controller.AddIndexers(ctx, mgr); err != nil {
				setupLog.Error(err, "unable to add indexers")
				os.Exit(1)
			}

			if serverConfig.Gateway.EnableDNSIntegration {
				if err := controller.AddDNSZoneDomainNameIndexer(ctx, mgr); err != nil {
					setupLog.Error(err, "unable to add DNSZone indexer")
					os.Exit(1)
				}
			}

			registeredWebhooks, err := setupWebhooks(mgr, serverConfig)
			if err != nil {
				setupLog.Error(err, "unable to set up webhooks")
				os.Exit(1)
			}
			setupLog.Info("registered webhooks", "webhooks", registeredWebhooks)

			// +kubebuilder:scaffold:builder

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				setupLog.Error(err, "unable to set up health check")
				os.Exit(1)
			}
			if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				setupLog.Error(err, "unable to set up ready check")
				os.Exit(1)
			}

			g, ctx := errgroup.WithContext(ctx)
			for _, runnable := range runnables {
				g.Go(func() error {
					return ignoreCanceled(runnable.Start(ctx))
				})
			}

			// Providers that still implement the legacy Run(ctx, mgr) shape (e.g. the
			// Milo provider) must be started by us. Providers that implement upstream's
			// multicluster.ProviderRunnable interface (e.g. mcsingle) are started
			// automatically by mgr.Start, so we skip them here.
			if runner, ok := provider.(legacyRunnableProvider); ok {
				setupLog.Info("starting cluster discovery provider")
				g.Go(func() error {
					return ignoreCanceled(runner.Run(ctx, mgr))
				})
			}

			if downstreamCluster != nil {
				g.Go(func() error {
					return ignoreCanceled(downstreamCluster.Start(ctx))
				})
			}

			if irohDownstream != nil {
				g.Go(func() error {
					return ignoreCanceled(irohDownstream.Start(ctx))
				})
			}

			setupLog.Info("starting multicluster manager")
			g.Go(func() error {
				return ignoreCanceled(mgr.Start(ctx))
			})

			if singletonMgr != nil {
				setupLog.Info("starting singleton controller manager")
				g.Go(func() error {
					return ignoreCanceled(singletonMgr.Start(ctx))
				})
			}

			if err := g.Wait(); err != nil {
				setupLog.Error(err, "unable to start")
				os.Exit(1)
			}

			return nil
		},
	}

	cmd.Flags().AddGoFlagSet(fs)
	return cmd
}

// legacyRunnableProvider matches providers that still expose the pre-upstream
// Run(ctx, mgr) shape (notably the Milo provider). Upstream multicluster-runtime
// has moved to multicluster.ProviderRunnable (Start(ctx, Aware)), which the
// manager starts automatically. Once Milo migrates this interface can be
// removed along with the manual goroutine that drives it.
type legacyRunnableProvider interface {
	multicluster.Provider
	Run(context.Context, mcmanager.Manager) error
}

type namedSetup struct {
	name    string
	enabled bool
	setup   func() error
}

func runSetups(kind string, registrations []namedSetup) ([]string, error) {
	registered := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		if !registration.enabled {
			continue
		}
		if err := registration.setup(); err != nil {
			return nil, fmt.Errorf("unable to create %s %s: %w", kind, registration.name, err)
		}
		registered = append(registered, registration.name)
	}
	return registered, nil
}

// webhookRegistrations lists every admission webhook.
func webhookRegistrations(mgr mcmanager.Manager, serverConfig config.NetworkServicesOperator) []namedSetup {
	registrations := []namedSetup{
		{"Gateway", true, func() error {
			return networkinggatewayv1webhooks.SetupGatewayWebhookWithManager(mgr, serverConfig)
		}},
		{"HTTPRoute", true, func() error {
			return networkinggatewayv1webhooks.SetupHTTPRouteWebhookWithManager(mgr, serverConfig)
		}},
		{"BackendTLSPolicy", true, func() error {
			return networkinggatewayv1webhooks.SetupBackendTLSPolicyWebhookWithManager(mgr)
		}},
		{"HTTPProxy", true, func() error {
			return networkingv1alphawebhooks.SetupHTTPProxyWebhookWithManager(mgr)
		}},
		{"Domain", true, func() error {
			return networkingv1alphawebhooks.SetupDomainWebhookWithManager(mgr)
		}},
		{"BackendTrafficPolicy", true, func() error {
			return webhookgatewayv1alpha1.SetupBackendTrafficPolicyWebhookWithManager(mgr, serverConfig)
		}},
		{"SecurityPolicy", true, func() error {
			return webhookgatewayv1alpha1.SetupSecurityPolicyWebhookWithManager(mgr, serverConfig)
		}},
		{"HTTPRouteFilter", true, func() error {
			return webhookgatewayv1alpha1.SetupHTTPRouteFilterWebhookWithManager(mgr, serverConfig)
		}},
		{"Backend", true, func() error {
			return webhookgatewayv1alpha1.SetupBackendWebhookWithManager(mgr)
		}},
	}

	return registrations
}

// setupWebhooks registers every admission webhook belonging to an enabled set
// and returns the names it registered.
func setupWebhooks(mgr mcmanager.Manager, serverConfig config.NetworkServicesOperator) ([]string, error) {
	return runSetups("webhook", webhookRegistrations(mgr, serverConfig))
}

// controllerDeps carries the clients and managers that controllers are wired
// against. Fields are only populated for the sets that need them.
type controllerDeps struct {
	downstreamCluster cluster.Cluster
	singletonManager  manager.Manager
	irohDownstream    cluster.Cluster
	ipamClients       controller.IPAMClientFactory
}

// newIPAMClientFactory returns nil when no IPAM connection is configured. A
// deployment that never named one keeps reconciling everything it reconciled
// before; only the address space a network would have been given goes unclaimed.
func newIPAMClientFactory(ipamConfig config.IPAMConfig) (controller.IPAMClientFactory, error) {
	if ipamConfig.KubeconfigPath == "" && !ipamConfig.InCluster {
		return nil, nil
	}

	// A network's identity has to be unique across every network on the
	// platform, so a deployment that names an identifier space and nowhere
	// platform-owned to allocate it in would hand out identities unique only
	// within one consumer, which is not unique at all. Say so at startup rather
	// than per network.
	if ipamConfig.Classes.FabricIdentity != "" && ipamConfig.Platform.Project == "" {
		return nil, fmt.Errorf("ipam.classes.fabricIdentity names an identifier space, but ipam.platform.project names nowhere platform-owned to allocate from")
	}

	restConfig, err := ipamConfig.RestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load IPAM kubeconfig: %w", err)
	}

	ipamScheme, err := controller.IPAMScheme()
	if err != nil {
		return nil, fmt.Errorf("unable to build IPAM scheme: %w", err)
	}

	return controller.NewIPAMClientFactory(restConfig, ipamScheme, ipamConfig.Platform.Project)
}

// controllerRegistrations lists every controller and the set it belongs to.
func controllerRegistrations(
	mgr mcmanager.Manager,
	serverConfig config.NetworkServicesOperator,
	deps controllerDeps,
) []namedSetup {
	// One channel between the two presence controllers, so a project-plane event
	// and a hub-side binding event land in the same workqueue and the presence
	// keeps exactly one writer.
	presenceEvents := controller.NewNetworkPresenceEvents()

	return []namedSetup{
		{"network", true, func() error {
			return (&controller.NetworkReconciler{
				IPAM:                    deps.ipamClients,
				PrefixClass:             serverConfig.IPAM.Classes.Network,
				FabricIdentityClass:     serverConfig.IPAM.Classes.FabricIdentity,
				FabricIdentityNamespace: serverConfig.IPAM.Platform.Namespace,
			}).SetupWithManager(mgr)
		}},
		{"networkbinding", true, func() error {
			return (&controller.NetworkBindingReconciler{}).SetupWithManager(mgr)
		}},
		{"networkcontext", true, func() error {
			return (&controller.NetworkContextReconciler{
				IPAM:        deps.ipamClients,
				SubnetClass: serverConfig.IPAM.Classes.Subnet,
			}).SetupWithManager(mgr)
		}},
		// The deployment cluster is the hub, and the milo provider engages
		// project control planes in the same process. The presence controller
		// goes on the singleton manager because the sharded managers run three
		// replicas with leader election disabled, which would reconcile every
		// hub object three times.
		{"networkpresence", true, func() error {
			return (&controller.NetworkPresenceReconciler{
				Projects:             controller.NewProjectClusterResolver(mgr),
				Events:               presenceEvents,
				UnclaimedGracePeriod: serverConfig.NetworkPresence.UnclaimedGracePeriod.Duration,
			}).SetupWithManager(deps.singletonManager)
		}},
		// Everything the presence controller reads besides the binding lives in a
		// project control plane, which only the multicluster manager watches. The
		// sync controller watches them there and hands the presence back to the
		// controller that owns it; it writes nothing itself.
		{"networkpresencesync", true, func() error {
			return (&controller.NetworkPresenceSyncReconciler{
				Events: presenceEvents,
			}).SetupWithManager(mgr, deps.singletonManager)
		}},
		// The interfaces a cell publishes arrive on the hub, and only the
		// multicluster manager reaches the project control planes they are for.
		// The projector goes on the singleton manager for the same reason the
		// presence controller does: three replicas would project every one of
		// them three times.
		{"networkinterfaceprojector", true, func() error {
			return (&controller.NetworkInterfaceProjector{
				Projects: controller.NewProjectClusterResolver(mgr),
			}).SetupWithManager(deps.singletonManager)
		}},
		// Removing a copy is the other half, and it has to watch project control
		// planes: a published interface that has gone says nothing about where
		// its copy went.
		{"networkinterfaceprojectiongc", true, func() error {
			return (&controller.NetworkInterfaceProjectionGCReconciler{}).SetupWithManager(mgr)
		}},
		{"networkpolicy", true, func() error {
			return (&controller.NetworkPolicyReconciler{}).SetupWithManager(mgr)
		}},
		{"subnet", true, func() error {
			return (&controller.SubnetReconciler{}).SetupWithManager(mgr)
		}},
		{"subnetclaim", true, func() error {
			return (&controller.SubnetClaimReconciler{}).SetupWithManager(mgr)
		}},
		{"httpproxy", true, func() error {
			return (&controller.HTTPProxyReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(mgr)
		}},
		{"gateway", true, func() error {
			return (&controller.GatewayReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(mgr)
		}},
		{"gatewayclass", true, func() error {
			return (&controller.GatewayClassReconciler{
				Config: serverConfig,
			}).SetupWithManager(mgr)
		}},
		{"gateway_downstream_resources", true, func() error {
			return (&controller.GatewayDownstreamGCReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(mgr)
		}},
		{"gateway_resource_replicator", true, func() error {
			return (&controller.GatewayResourceReplicatorReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(mgr)
		}},
		{"trafficprotectionpolicy", !serverConfig.Gateway.Coraza.Disabled, func() error {
			return (&controller.TrafficProtectionPolicyReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(mgr)
		}},
		{"downstream-certificate-solver", serverConfig.Gateway.EnableDownstreamCertificateSolver, func() error {
			return (&controller.GatewayDownstreamCertificateSolverReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(deps.singletonManager)
		}},
		{"domain", true, func() error {
			return (&controller.DomainReconciler{
				Config: serverConfig,
			}).SetupWithManager(mgr)
		}},
		{"connector", true, func() error {
			return (&controller.ConnectorReconciler{
				Config: serverConfig,
			}).SetupWithManager(mgr)
		}},
		{"connectoradvertisement", true, func() error {
			return (&controller.ConnectorAdvertisementReconciler{}).SetupWithManager(mgr)
		}},
		{"iroh-dns", serverConfig.Connector.Iroh.DNSEnabled, func() error {
			return (&controller.IrohDNSReconciler{
				Config:     serverConfig,
				Downstream: deps.irohDownstream,
			}).SetupWithManager(mgr)
		}},
		{"challenge", serverConfig.Gateway.ShouldDeleteErroredChallenges(), func() error {
			return (&controller.ChallengeReconciler{
				Config:            serverConfig,
				DownstreamCluster: deps.downstreamCluster,
			}).SetupWithManager(deps.singletonManager)
		}},
		{"location_publisher", serverConfig.LocationPublisher.Enabled(), func() error {
			return setupLocationPublisher(serverConfig, scheme, deps.singletonManager)
		}},
	}
}

// leaderElectedRunnable states a runnable's leader-election intent explicitly.
type leaderElectedRunnable struct {
	manager.Runnable
	leaderElected bool
}

func (r leaderElectedRunnable) NeedLeaderElection() bool { return r.leaderElected }

// setupLocationPublisher connects the publisher to the control plane it reads
// Locations from and the federation hub it writes copies to. Both connections
// run only on the leader.
func setupLocationPublisher(
	serverConfig config.NetworkServicesOperator,
	scheme *runtime.Scheme,
	mgr manager.Manager,
) error {
	sourceRestConfig, err := serverConfig.LocationPublisher.SourceRestConfig(&serverConfig.Discovery)
	if err != nil {
		return fmt.Errorf("unable to load the location source kubeconfig: %w", err)
	}
	hubRestConfig, err := serverConfig.LocationPublisher.HubRestConfig()
	if err != nil {
		return fmt.Errorf("unable to load the federation hub kubeconfig: %w", err)
	}

	sourceCluster, err := cluster.New(sourceRestConfig, func(o *cluster.Options) {
		o.Scheme = scheme
	})
	if err != nil {
		return fmt.Errorf("unable to construct the location source cluster: %w", err)
	}

	hubCluster, err := cluster.New(hubRestConfig, func(o *cluster.Options) {
		o.Scheme = scheme
		o.Client = client.Options{Cache: &client.CacheOptions{Unstructured: true}}
	})
	if err != nil {
		return fmt.Errorf("unable to construct the federation hub cluster: %w", err)
	}

	for _, c := range []cluster.Cluster{sourceCluster, hubCluster} {
		if err := mgr.Add(leaderElectedRunnable{Runnable: c, leaderElected: true}); err != nil {
			return fmt.Errorf("unable to add a location publisher cluster: %w", err)
		}
	}

	return (&controller.LocationPublisherReconciler{
		Config:        serverConfig,
		SourceCluster: sourceCluster,
		HubCluster:    hubCluster,
	}).SetupWithManager(mgr)
}

// setupControllers registers every controller belonging to an enabled set and
// returns the names it registered.
func setupControllers(
	mgr mcmanager.Manager,
	serverConfig config.NetworkServicesOperator,
	deps controllerDeps,
) ([]string, error) {
	return runSetups("controller", controllerRegistrations(mgr, serverConfig, deps))
}

const defaultLeaderElectionID = "6a7d51cc.datumapis.com"

func ignoreCanceled(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// ControllerNames returns every controller this command registers, including
// those a capability gate would skip.
func ControllerNames() []string {
	registrations := controllerRegistrations(nil, config.NetworkServicesOperator{}, controllerDeps{})
	names := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		names = append(names, registration.name)
	}
	return names
}
