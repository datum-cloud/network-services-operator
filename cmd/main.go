package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"golang.org/x/sync/errgroup"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	"github.com/multicluster-runtime/multicluster-runtime/pkg/multicluster"
	mckind "github.com/multicluster-runtime/multicluster-runtime/providers/kind"
	mcsingle "github.com/multicluster-runtime/multicluster-runtime/providers/single"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/controller"
	"go.datum.net/network-services-operator/internal/providers"
	mcdatum "go.datum.net/network-services-operator/internal/providers/datum"
	"go.datum.net/network-services-operator/internal/validation"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(networkingv1alpha.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var leaderElectionNamespace string
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var clusterDiscoveryMode string
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&leaderElectionNamespace, "leader-elect-namespace", "", "The namespace to use for leader election.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&clusterDiscoveryMode, "cluster-discovery-mode", "single",
		"Method to discover clusters. Allowed values are: "+strings.Join(providers.AllowedProviders, ","))
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization

		// TODO(user): If CertDir, CertName, and KeyName are not specified, controller-runtime will automatically
		// generate self-signed certificates for the metrics server. While convenient for development and testing,
		// this setup is not recommended for production.
	}

	cfg := ctrl.GetConfigOrDie()
	var localManager manager.Manager
	var err error

	var provider interface {
		multicluster.Provider
		// TODO(jreese) see if Run should be defined in the Provider interface
		Run(context.Context, mcmanager.Manager) error
	}
	var singleCluster cluster.Cluster

	switch clusterDiscoveryMode {
	case providers.ProviderSingle:
		singleCluster, err = cluster.New(cfg, func(o *cluster.Options) {
			o.Scheme = scheme
		})
		if err != nil {
			setupLog.Error(err, "failed creating cluster")
			os.Exit(1)
		}
		provider = mcsingle.New("single", singleCluster)

	case providers.ProviderDatum:
		localManager, err = manager.New(cfg, manager.Options{
			Client: client.Options{
				Cache: &client.CacheOptions{
					Unstructured: true,
				},
			},
		})
		if err != nil {
			setupLog.Error(err, "unable to set up overall controller manager")
			os.Exit(1)
		}

		provider, err = mcdatum.New(localManager, mcdatum.Options{
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.Scheme = scheme
				},
			},
		})
		if err != nil {
			setupLog.Error(err, "unable to create datum project provider")
			os.Exit(1)
		}

	case providers.ProviderKind:
		provider = mckind.New(mckind.Options{
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.Scheme = scheme
				},
			},
		})

	default:
		setupLog.Error(fmt.Errorf(
			"unsupported cluster discovery mode. Got %q, expected one of %s",
			clusterDiscoveryMode,
			strings.Join(providers.AllowedProviders, ","),
		), "")
		os.Exit(1)
	}

	setupLog.Info("cluster discovery mode", "mode", clusterDiscoveryMode)

	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsServerOptions,
		WebhookServer:           webhookServer,
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "6a7d51cc.datumapis.com",
		LeaderElectionNamespace: leaderElectionNamespace,
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
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.NetworkReconciler{}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Network")
		os.Exit(1)
	}
	if err = (&controller.NetworkBindingReconciler{}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NetworkBinding")
		os.Exit(1)
	}
	if err = (&controller.NetworkContextReconciler{}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NetworkContext")
		os.Exit(1)
	}
	if err = (&controller.NetworkPolicyReconciler{}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NetworkPolicy")
		os.Exit(1)
	}
	if err = (&controller.SubnetReconciler{}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Subnet")
		os.Exit(1)
	}
	if err = (&controller.SubnetClaimReconciler{}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SubnetClaim")
		os.Exit(1)
	}
	if err = (&controller.GatewayReconciler{
		ValidationOpts: validation.GatewayValidationOptions{
			RoutesFromSameNamespaceOnly: true,
			PermitCertificateRefs:       true,
			ValidPortNumbers:            []int{80, 443},
			ValidProtocolTypes: []gatewayv1.ProtocolType{
				gatewayv1.HTTPProtocolType,
				gatewayv1.HTTPSProtocolType,
			},
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Gateway")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	if clusterDiscoveryMode == providers.ProviderSingle {
		setupLog.Info("engaging cluster for single cluster provider")
		// Pending feedback on https://github.com/multicluster-runtime/multicluster-runtime/pull/17#issue-2911191237
		// to determine if the provider's Run function should be calling Engage
		if err := mgr.Engage(ctx, "single", singleCluster); err != nil {
			setupLog.Error(err, "failed engaging cluster")
			os.Exit(1)
		}
	}

	g, ctx := errgroup.WithContext(ctx)
	if localManager != nil {
		setupLog.Info("starting local manager")
		g.Go(func() error {
			return ignoreCanceled(localManager.Start(ctx))
		})
	}

	setupLog.Info("starting cluster discovery provider")
	g.Go(func() error {
		return ignoreCanceled(provider.Run(ctx, mgr))
	})

	if singleCluster != nil {
		setupLog.Info("starting cluster for single cluster provider")
		g.Go(func() error {
			return ignoreCanceled(singleCluster.Start(ctx))
		})
	}

	setupLog.Info("starting multicluster manager")
	g.Go(func() error {
		return ignoreCanceled(mgr.Start(ctx))
	})

	if err := g.Wait(); err != nil {
		setupLog.Error(err, "unable to start")
		os.Exit(1)
	}
}

func ignoreCanceled(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
