package cell

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/cmd/clusterdiscovery"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/controller"
	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
)

const leaderElectionID = "6a7d51cc.datumapis.com-cell"

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
	utilruntime.Must(networkingv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ipamv1alpha1.AddToScheme(scheme))
}

// NewCommand builds the cell controller manager command.
func NewCommand() *cobra.Command {
	var (
		probeAddr                string
		enableLeaderElection     bool
		leaderElectionNamespace  string
		leaderElectionIDOverride string
		serverConfigFile         string
	)

	fs := flag.NewFlagSet("cell-manager", flag.ContinueOnError)
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for the cell controller manager.")
	fs.StringVar(&leaderElectionNamespace, "leader-elect-namespace", "", "The namespace to use for leader election.")
	fs.StringVar(&leaderElectionIDOverride, "leader-election-id", "",
		fmt.Sprintf("Leader election ID. Defaults to %s.", leaderElectionID))
	fs.StringVar(&serverConfigFile, "server-config", "", "path to the server config file")

	opts := zap.Options{Development: true}
	opts.BindFlags(fs)

	cmd := &cobra.Command{
		Use:   "cell-manager",
		Short: "Run the network-services-operator controller manager for a cell",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

			serverConfig, err := loadConfig(serverConfigFile)
			if err != nil {
				setupLog.Error(err, "unable to load server config")
				os.Exit(1)
			}

			setupLog.Info("server config", "config", serverConfig)
			setupLog.Info("configured location fallback, used only until a "+
				"ServingLocation is delivered to this cell",
				"name", serverConfig.Location.Name,
			)

			if leaderElectionIDOverride == "" {
				leaderElectionIDOverride = leaderElectionID
			}

			ctx := ctrl.SetupSignalHandler()

			cfg := ctrl.GetConfigOrDie()
			serverConfig.ControlPlaneClient.ApplyTo(cfg)

			deploymentCluster, err := cluster.New(cfg, func(o *cluster.Options) {
				o.Scheme = scheme
			})
			if err != nil {
				setupLog.Error(err, "unable to create deployment cluster")
				os.Exit(1)
			}

			runnables, provider, err := clusterdiscovery.Initialize(
				serverConfig.Discovery,
				serverConfig.ProjectClient,
				deploymentCluster,
				scheme,
			)
			if err != nil {
				setupLog.Error(err, "unable to initialize cluster discovery")
				os.Exit(1)
			}

			leaseDuration := serverConfig.LeaderElection.LeaseDuration.Duration
			renewDeadline := serverConfig.LeaderElection.RenewDeadline.Duration
			retryPeriod := serverConfig.LeaderElection.RetryPeriod.Duration

			mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
				Scheme:                  scheme,
				Metrics:                 serverConfig.MetricsServer.Options(ctx, deploymentCluster.GetClient()),
				HealthProbeBindAddress:  probeAddr,
				LeaderElection:          enableLeaderElection,
				LeaderElectionID:        leaderElectionIDOverride,
				LeaderElectionNamespace: leaderElectionNamespace,
				LeaseDuration:           &leaseDuration,
				RenewDeadline:           &renewDeadline,
				RetryPeriod:             &retryPeriod,
			})
			if err != nil {
				setupLog.Error(err, "unable to start manager")
				os.Exit(1)
			}

			ipamClients, err := newIPAMClientFactory(serverConfig)
			if err != nil {
				setupLog.Error(err, "unable to reach IPAM")
				os.Exit(1)
			}

			hubCluster, err := newHubCluster(serverConfig)
			if err != nil {
				setupLog.Error(err, "unable to reach the federation hub")
				os.Exit(1)
			}
			registered, err := setupControllers(mgr, serverConfig, ipamClients, hubCluster)
			if err != nil {
				setupLog.Error(err, "unable to set up controllers")
				os.Exit(1)
			}
			setupLog.Info("registered controllers", "controllers", registered)

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				setupLog.Error(err, "unable to set up health check")
				os.Exit(1)
			}
			if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				setupLog.Error(err, "unable to set up ready check")
				os.Exit(1)
			}

			g, ctx := errgroup.WithContext(ctx)
			g.Go(func() error {
				return ignoreCanceled(hubCluster.Start(ctx))
			})
			for _, runnable := range runnables {
				g.Go(func() error {
					return ignoreCanceled(runnable.Start(ctx))
				})
			}

			if runner, ok := provider.(legacyRunnableProvider); ok {
				setupLog.Info("starting cluster discovery provider")
				g.Go(func() error {
					return ignoreCanceled(runner.Run(ctx, mgr))
				})
			}

			setupLog.Info("starting cell controller manager")
			g.Go(func() error {
				return ignoreCanceled(mgr.Start(ctx))
			})

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
// Run(ctx, mgr) shape.
type legacyRunnableProvider interface {
	Run(context.Context, mcmanager.Manager) error
}

func ignoreCanceled(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func loadConfig(path string) (config.CellControllerManager, error) {
	var serverConfig config.CellControllerManager

	var data []byte
	if len(path) > 0 {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return serverConfig, fmt.Errorf("unable to read server config from %q: %w", path, err)
		}
	}

	if err := runtime.DecodeInto(codecs.UniversalDecoder(), data, &serverConfig); err != nil {
		return serverConfig, fmt.Errorf("unable to decode server config: %w", err)
	}

	if err := serverConfig.Validate(); err != nil {
		return serverConfig, fmt.Errorf("invalid server config: %w", err)
	}

	return serverConfig, nil
}

// setupControllers registers every controller a cell runs and returns their
// names.
func setupControllers(
	mgr mcmanager.Manager,
	serverConfig config.CellControllerManager,
	ipamClients controller.IPAMClientFactory,
	hubCluster cluster.Cluster,
) ([]string, error) {
	registrations := controllerRegistrations(mgr, serverConfig, ipamClients, hubCluster)

	registered := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		on, err := registration.setup()
		if err != nil {
			return nil, fmt.Errorf("unable to create controller %q: %w", registration.name, err)
		}
		if on {
			registered = append(registered, registration.name)
		}
	}
	return registered, nil
}

type namedSetup struct {
	name string

	// setup reports whether it registered the controller. Every controller a
	// cell owns registers today, but the startup log is built from what this
	// returns rather than from the list, so a controller that starts declining
	// to register cannot be reported as running.
	setup func() (bool, error)
}

// controllerRegistrations lists every controller a cell's control plane owns.
func controllerRegistrations(
	mgr mcmanager.Manager,
	serverConfig config.CellControllerManager,
	ipamClients controller.IPAMClientFactory,
	hubCluster cluster.Cluster,
) []namedSetup {
	registrations := make([]namedSetup, 0, 4)
	registrations = append(registrations,
		namedSetup{"networkinterfaceclaim", func() (bool, error) {
			return true, (&controller.NetworkInterfaceClaimReconciler{
				Location: serverConfig.Location,
				IPAM:     ipamClients,
			}).SetupWithManager(mgr)
		}},
		namedSetup{"networkinterface", func() (bool, error) {
			return true, (&controller.NetworkInterfaceReconciler{
				Location: serverConfig.Location,
				IPAM:     ipamClients,
			}).SetupWithManager(mgr)
		}},
		namedSetup{"networkcontexthold", func() (bool, error) {
			return true, (&controller.NetworkContextHoldReconciler{
				Location: serverConfig.Location,
			}).SetupWithManager(mgr)
		}},
	)

	// No hub-less branch. A cell with no hub used to register nothing here and
	// report the controller as running anyway, which is how a cell published
	// nothing for weeks without saying so. Setup now rejects a missing hub.
	registrations = append(registrations, namedSetup{"networkinterfacewriteback", func() (bool, error) {
		return true, (&controller.NetworkInterfaceWriteBackReconciler{
			Location:   serverConfig.Location,
			HubCluster: hubCluster,
		}).SetupWithManager(mgr)
	}})

	return registrations
}

// newHubCluster connects a cell to the federation hub it publishes to. A cell
// has no hub-less mode: config validation requires federation.kubeconfigPath,
// so reaching here without one is a bug rather than a deployment choice.
func newHubCluster(serverConfig config.CellControllerManager) (cluster.Cluster, error) {
	restConfig, err := serverConfig.Federation.RestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load the federation hub kubeconfig: %w", err)
	}

	return cluster.New(restConfig, func(o *cluster.Options) {
		o.Scheme = scheme
	})
}

func newIPAMClientFactory(serverConfig config.CellControllerManager) (controller.IPAMClientFactory, error) {
	ipamRestConfig, err := serverConfig.IPAM.RestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load IPAM kubeconfig: %w", err)
	}

	ipamScheme, err := controller.IPAMScheme()
	if err != nil {
		return nil, fmt.Errorf("unable to build IPAM scheme: %w", err)
	}

	ipamClients, err := controller.NewIPAMClientFactory(ipamRestConfig, ipamScheme)
	if err != nil {
		return nil, fmt.Errorf("unable to build IPAM client factory: %w", err)
	}

	return ipamClients, nil
}

// ControllerNames returns every controller this command registers.
func ControllerNames() []string {
	registrations := controllerRegistrations(nil, config.CellControllerManager{}, nil, nil)
	names := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		names = append(names, registration.name)
	}
	return names
}
