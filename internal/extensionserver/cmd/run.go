// Package extservercmd implements the "extension-server" subcommand entrypoint.
// It is called from cmd/main.go when the first argument is "extension-server".
package extservercmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	pb "github.com/envoyproxy/gateway/proto/extension"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
	extmetrics "go.datum.net/network-services-operator/internal/extensionserver/metrics"
	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
	extserver "go.datum.net/network-services-operator/internal/extensionserver/server"
	exttls "go.datum.net/network-services-operator/internal/extensionserver/tls"
	exttracing "go.datum.net/network-services-operator/internal/extensionserver/tracing"
)

// envBool returns the boolean value of an environment variable, or def if unset.
func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "":
		return def
	case "0", "false", "FALSE", "False", "no", "NO":
		return false
	default:
		return true
	}
}

// RunServer is the extension-server subcommand entrypoint. Called from
// cmd/main.go when os.Args[1] == "extension-server". It owns flag parsing,
// cache startup, gRPC server lifecycle, and signal handling.
//
// The extension server runs at the edge, co-located with Envoy Gateway.
// It reads policy resources from the LOCAL edge cluster only — NSO replicates
// TrafficProtectionPolicy, HTTPProxy, and Connector resources into the edge
// cluster's downstream ns-<uid> namespaces; no upstream connectivity needed.
//
// Flag surface (must match SRE's config/extension-server/deployment.yaml):
//
//	--grpc-addr=:5005
//	--health-addr=:8080
//	--tls-cert=/tls/tls.crt
//	--tls-key=/tls/tls.key
//	--tls-client-ca=/tls/ca.crt
//	--server-config=/config/config.yaml  (optional; provides Coraza WAF config)
//
// Tracing:
//
//	OTEL_TRACES_ENABLED=true   — enable OTLP span export to the in-cluster Tempo
//	OTEL_EXPORTER_OTLP_ENDPOINT — override Tempo endpoint (default: telemetry-system-tempo.telemetry-system:4317)
//	OTEL_SERVICE_NAME           — override service name (default: nso-extension-server)
func RunServer(args []string) {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fs := flag.NewFlagSet("extension-server", flag.ExitOnError)
	var (
		grpcAddr      string
		healthAddr    string
		tlsCert       string
		tlsKey        string
		tlsClientCA   string
		serverCfgFile string
	)
	fs.StringVar(&grpcAddr, "grpc-addr", ":5005", "gRPC listener address")
	fs.StringVar(&healthAddr, "health-addr", ":8080", "Plain HTTP health endpoint address")
	fs.StringVar(&tlsCert, "tls-cert", "", "Path to server TLS certificate (PEM)")
	fs.StringVar(&tlsKey, "tls-key", "", "Path to server TLS key (PEM)")
	fs.StringVar(&tlsClientCA, "tls-client-ca", "", "Path to client CA certificate (PEM) for mTLS")
	fs.StringVar(&serverCfgFile, "server-config", "", "Path to operator config file (optional; provides Coraza WAF settings)")

	if err := fs.Parse(args); err != nil {
		log.Error("parse flags", "err", err)
		os.Exit(1)
	}

	// --- OTel tracing (optional, default off) ---
	// Enable by setting OTEL_TRACES_ENABLED=true. Non-fatal: the server continues
	// without tracing if the OTLP backend is unreachable. When disabled, the
	// handler's span calls use OTel's global noop tracer (no export cost).
	if envBool("OTEL_TRACES_ENABLED", false) {
		shutdown, err := exttracing.Setup(context.Background())
		if err != nil {
			log.Error("otel tracing setup (continuing without traces)", "err", err)
		} else {
			endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
			if endpoint == "" {
				endpoint = "telemetry-system-tempo.telemetry-system:4317"
			}
			log.Info("otel tracing enabled", "endpoint", endpoint)
			defer func() { _ = shutdown(context.Background()) }()
		}
	}

	// --- Operator config (optional) ---
	// Reads Coraza WAF settings (FilterName, LibraryPath, etc.) and the
	// connector tunnel listener name. The multi-cluster discovery section is
	// NOT used — the extension server reads from the local edge cluster only.
	// If --server-config is omitted, registered defaults are used.
	configScheme := runtime.NewScheme()
	utilruntime.Must(config.AddToScheme(configScheme))
	utilruntime.Must(config.RegisterDefaults(configScheme))
	codecs := serializer.NewCodecFactory(configScheme, serializer.EnableStrict)

	var serverConfig config.NetworkServicesOperator
	var configData []byte
	if serverCfgFile != "" {
		var err error
		configData, err = os.ReadFile(serverCfgFile)
		if err != nil {
			log.Error("read server config", "path", serverCfgFile, "err", err)
			os.Exit(1)
		}
	}
	if err := runtime.DecodeInto(codecs.UniversalDecoder(), configData, &serverConfig); err != nil {
		log.Error("decode server config", "err", err)
		os.Exit(1)
	}

	// --- Cache scheme ---
	// Minimal scheme covering the four types watched by the informer cache.
	cacheScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(cacheScheme)) // corev1.Namespace
	utilruntime.Must(networkingv1alpha.AddToScheme(cacheScheme))
	utilruntime.Must(networkingv1alpha1.AddToScheme(cacheScheme))

	// --- Single-cluster read-only cache manager ---
	// NewManager uses ctrl.GetConfig() (in-cluster config) internally and
	// primes informers for TPP/HTTPProxy/Connector/Namespace before Start.
	// No leader election; all replicas serve from the same warm local cache.
	mgr, err := extcache.NewManager(cacheScheme)
	if err != nil {
		log.Error("create cache manager", "err", err)
		os.Exit(1)
	}

	// --- mTLS config ---
	// LoadServerTLSConfig uses GetCertificate (re-reads on each handshake) so
	// cert-manager rotations are picked up automatically without a pod restart.
	tlsCfg, err := exttls.LoadServerTLSConfig(tlsCert, tlsKey, tlsClientCA)
	if err != nil {
		log.Error("load TLS config", "err", err)
		os.Exit(1)
	}

	// --- Build ServerConfig from operator config ---
	coraza := serverConfig.Gateway.Coraza
	srvCfg := extserver.ServerConfig{
		Coraza: mutate.CorazaConfig{
			Disabled:                    coraza.Disabled,
			FilterName:                  coraza.FilterName,
			LibraryID:                   coraza.LibraryID,
			LibraryPath:                 coraza.LibraryPath,
			PluginName:                  coraza.PluginName,
			ListenerDirectives:          coraza.ListenerDirectives,
			TraceRouteMetadataExtractor: coraza.TraceRouteMetadataExtractor,
		},
		ConnectorInternalListener: serverConfig.Gateway.ConnectorTunnelListenerName(),
		CorazaRouteBaseDirectives: coraza.RouteBaseDirectives,
	}

	// --- gRPC panic recovery interceptor ---
	// If PostTranslateModify panics (e.g. nil pointer in xDS protobuf processing),
	// the interceptor catches the panic, logs it, increments the panic counter, and
	// returns UNAVAILABLE — which IS in EG's retryableStatusCodes list, so EG will
	// retry the failed build rather than propagating a permanent error.
	//
	// Without this interceptor, gRPC-go's internal recovery returns an error that
	// gRPC converts to UNKNOWN (not retryable), and the panic goes unlogged.
	panicHandler := recovery.WithRecoveryHandlerContext(
		func(ctx context.Context, p any) error {
			log.Error("PostTranslateModify panic recovered",
				"panic", fmt.Sprintf("%v", p),
			)
			extmetrics.HookPanicsTotal.Inc()
			return status.Errorf(codes.Unavailable,
				"extension server panic; EG will retry: %v", p)
		},
	)

	// --- gRPC server ---
	// At high gateway counts the merged xDS snapshot exceeds the gRPC default
	// 4 MiB max message size (~7.4 MB at N=1000 gateways, linear scaling).
	// EG's PostTranslateModify request is rejected with RESOURCE_EXHAUSTED
	// before the handler runs, and with failOpen:false EG fails the entire
	// xDS build fleet-wide — silently, with no signal on the existing
	// dashboard. This ceiling is breached at approximately N=540. Raise both
	// recv/send limits generously so the hook can process large snapshots.
	// (EG must be matched via extensionManager.maxMessageSize.)
	const maxMsgSize = 256 << 20 // 256 MiB
	creds := credentials.NewTLS(tlsCfg)
	grpcServer := grpc.NewServer(
		grpc.Creds(creds),
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
		// RPCStatsHandler times the FULL RPC (codec unmarshal + handler + marshal).
		// Must be a stats.Handler — unary interceptors cannot see codec time.
		grpc.StatsHandler(extserver.NewRPCStatsHandler()),
		// otelgrpc server handler: opens the per-RPC OTel server span (covering
		// the full RPC incl. codec) and injects it into the handler context so
		// the manual phase child spans (handler/index_build/mutate/…) nest under
		// it. gRPC supports multiple stats handlers; this composes with the
		// Prometheus RPCStatsHandler above. When tracing is disabled
		// (OTEL_TRACES_ENABLED=false), the noop TracerProvider is used, incurring
		// no export cost (context propagation headers are still parsed).
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		// Interceptor chain order: metrics (outer) → recovery (inner).
		// Metrics outer ensures RESOURCE_EXHAUSTED and other pre-handler codes
		// are captured; recovery inner converts panics to UNAVAILABLE before the
		// metrics interceptor records the outcome code.
		grpc.ChainUnaryInterceptor(
			extmetrics.GRPCServerMetrics.UnaryServerInterceptor(),
			recovery.UnaryServerInterceptor(panicHandler),
		),
	)

	// Extension server reads from the manager's cache-backed client.
	// BuildPolicyIndexFromClient is called per hook invocation; reads are
	// served from the warm local informer cache.
	extSrv := extserver.New(mgr.GetClient(), srvCfg, log.With("component", "extserver"))
	pb.RegisterEnvoyGatewayExtensionServer(grpcServer, extSrv)

	// gRPC health protocol — reports NOT_SERVING until caches sync.
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthSrv)

	// gRPC reflection for tools like grpcurl.
	reflection.Register(grpcServer)

	// --- Health endpoint state ---
	// The plain HTTP /healthz on :health-addr returns 503 until caches sync,
	// then 200. failOpen:false in the EG extensionManager config makes a cold
	// cache a correctness hazard, so readiness MUST gate on sync.
	var cacheReady atomic.Bool

	// --- Signal context ---
	ctx := ctrl.SetupSignalHandler()
	g, gCtx := errgroup.WithContext(ctx)

	// Start the informer cache manager.
	g.Go(func() error {
		if err := mgr.Start(gCtx); err != nil {
			return fmt.Errorf("cache manager: %w", err)
		}
		return nil
	})

	// Wait for cache sync before accepting gRPC connections.
	// This runs in its own goroutine so the errgroup context can be canceled
	// on shutdown even while we're waiting.
	g.Go(func() error {
		log.Info("waiting for informer cache sync")
		if !mgr.GetCache().WaitForCacheSync(gCtx) {
			if gCtx.Err() != nil {
				return nil // graceful shutdown during startup
			}
			return fmt.Errorf("informer cache sync failed")
		}
		log.Info("informer cache synced")
		cacheReady.Store(true)
		extmetrics.CacheSynced.Set(1)
		healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

		// Start gRPC server now that caches are warm.
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", grpcAddr, err)
		}
		log.Info("gRPC extension server listening", "addr", grpcAddr)

		// Watch for context cancellation to stop the gRPC server.
		go func() {
			<-gCtx.Done()
			log.Info("gracefully stopping gRPC server")
			grpcServer.GracefulStop()
		}()

		if err := grpcServer.Serve(lis); err != nil {
			return fmt.Errorf("grpc serve: %w", err)
		}
		return nil
	})

	// Post-startup cache health monitor.
	//
	// WaitForCacheSync is a one-time latch: once all informers' HasSynced()
	// returns true, it stays true permanently, so a periodic WaitForCacheSync
	// call cannot detect post-startup informer health degradation.
	//
	// Instead, we periodically attempt a lightweight List from the
	// cache-backed client. A List error (indicating the cache client is broken,
	// not merely stale) resets cacheReady so the readiness probe returns 503
	// and the pod is removed from the Service endpoints until it recovers.
	//
	// Under normal conditions (API server reachable, informers reconnecting
	// after a blip) the List always succeeds from the local in-memory store,
	// so this check never fires spuriously.
	g.Go(func() error {
		const (
			checkInterval = 30 * time.Second
			checkTimeout  = 5 * time.Second
		)
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-gCtx.Done():
				return nil
			case <-ticker.C:
				if !cacheReady.Load() {
					// Still warming up; the WaitForCacheSync goroutine
					// will set cacheReady to true once sync completes.
					continue
				}
				checkCtx, cancel := context.WithTimeout(gCtx, checkTimeout)
				var nsList corev1.NamespaceList
				listErr := mgr.GetClient().List(checkCtx, &nsList)
				cancel()
				if listErr != nil {
					log.Warn("cache health check failed; marking not ready",
						"err", listErr,
					)
					cacheReady.Store(false)
					extmetrics.CacheSynced.Set(0)
					healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
				} else if !cacheReady.Load() {
					// Recover readiness after a transient cache error.
					log.Info("cache health check recovered; marking ready")
					cacheReady.Store(true)
					extmetrics.CacheSynced.Set(1)
					healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
				}
			}
		}
	})

	// Plain HTTP health + metrics endpoint.
	// /healthz  — liveness/readiness probe (503 until cache syncs).
	// /metrics  — Prometheus text exposition (default registry).
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if cacheReady.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("caches not synced"))
		}
	})
	mux.Handle("/metrics", promhttp.Handler())
	healthServer := &http.Server{
		Addr:    healthAddr,
		Handler: mux,
	}
	g.Go(func() error {
		log.Info("health endpoint listening", "addr", healthAddr)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("health server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-gCtx.Done()
		return healthServer.Close()
	})

	if err := g.Wait(); err != nil {
		log.Error("extension server exiting", "err", err)
		os.Exit(1)
	}
	log.Info("extension server stopped")
}
