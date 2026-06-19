// Package server implements the Envoy Gateway extension-server gRPC contract
// for the NSO production extension server. It applies TrafficProtectionPolicy
// WAF and Connector tunnel mutations to the full post-translation xDS snapshot
// via the PostTranslateModify hook, replacing the EnvoyPatchPolicy approach.
package server

import (
	"context"
	"log/slog"
	"time"

	pb "github.com/envoyproxy/gateway/proto/extension"
	"go.opentelemetry.io/otel/attribute"
	"sigs.k8s.io/controller-runtime/pkg/client"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
	extmetrics "go.datum.net/network-services-operator/internal/extensionserver/metrics"
	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
	exttracing "go.datum.net/network-services-operator/internal/extensionserver/tracing"
)

// ServerConfig carries the operator configuration needed by the mutation logic.
// Values are sourced from the operator's GatewayConfig at startup.
type ServerConfig struct {
	// Coraza WAF configuration (filter name, library path, plugin settings).
	Coraza mutate.CorazaConfig
	// ConnectorInternalListener is the Envoy internal listener name used for
	// connector tunnel routing. Defaults to "connector-tunnel".
	ConnectorInternalListener string
	// CorazaRouteBaseDirectives are prepended to every per-policy directive
	// list when building Coraza simple_directives. Sourced from
	// GatewayConfig.Coraza.RouteBaseDirectives.
	CorazaRouteBaseDirectives []string
	// LocalReply carries the branded data-plane error-page configuration
	// (branded HTML body, status threshold, runtime key). When disabled or
	// empty, local-reply injection is a no-op. Sourced from
	// GatewayConfig.ErrorPage + the embedded/override HTML body.
	LocalReply mutate.LocalReplyConfig
}

// Server implements pb.EnvoyGatewayExtensionServer for the NSO production
// extension server. All mutation happens in PostTranslateModify.
// Granular hooks (HTTPListener, HTTPRoute, etc.) are left to the embedded
// Unimplemented base and are not registered in the extensionManager config.
type Server struct {
	pb.UnimplementedEnvoyGatewayExtensionServer
	// client is the cache-backed reader from the local edge cluster's
	// ctrl.Manager. All policy reads (TPP, HTTPProxy, Connector, Namespace)
	// are served from the warm local informer cache — no API server calls
	// during hook processing.
	client client.Client
	cfg    ServerConfig
	log    *slog.Logger
}

// New returns a production extension server backed by the given cache client.
// In production, cl is the ctrl.Manager.GetClient() from NewManager().
// In tests, cl is a fake client pre-populated with the test objects.
func New(cl client.Client, cfg ServerConfig, log *slog.Logger) *Server {
	return &Server{client: cl, cfg: cfg, log: log}
}

// PostTranslateModify applies the TPP/WAF and Connector mutation families to
// the full xDS snapshot and returns the complete (mutated) resource set.
// Secrets are passed through unchanged — the response replaces EG's entire
// resource set, so every list must be present in the response.
//
// Mutation ordering (must match NSO EPP ordering to preserve config-dump
// parity for the A/B gate):
//  1. InjectCorazaListenerFilters — inject disabled Coraza into ALL HCMs.
//  2. ApplyTPPRouteConfig         — per-route WAF config for governed routes.
//  3. ReplaceConnectorClusters    — replace online-connector clusters with
//     STATIC internal-upstream clusters.
//  4. ApplyConnectorRoutes        — prepend CONNECT routes, append target domains.
func (s *Server) PostTranslateModify(
	ctx context.Context,
	req *pb.PostTranslateModifyRequest,
) (*pb.PostTranslateModifyResponse, error) {
	start := time.Now()
	outcome := outcomeSuccess
	defer func() {
		elapsed := time.Since(start).Seconds()
		extmetrics.HookDuration.WithLabelValues(outcome).Observe(elapsed)
		extmetrics.HookCallsTotal.WithLabelValues(outcome).Inc()
	}()

	// "handler" span: child of the otelgrpc RPC server span opened by
	// otelgrpc.NewServerHandler() (registered in cmd/run.go). The gap between
	// the otelgrpc RPC span and this span in the Tempo waterfall is the gRPC
	// request unmarshal (before) and response marshal (after). When tracing is
	// disabled (OTEL_TRACES_ENABLED unset), exttracing.Tracer() returns OTel's
	// global noop tracer — span operations compile and run with no export cost.
	tr := exttracing.Tracer()
	ctx, hspan := tr.Start(ctx, "handler")
	defer hspan.End()

	clusters := req.GetClusters()
	listeners := req.GetListeners()
	routes := req.GetRoutes()

	totalRoutes := 0
	for _, rc := range routes {
		for _, vh := range rc.GetVirtualHosts() {
			totalRoutes += len(vh.GetRoutes())
		}
	}
	// Size attributes so a tail trace is self-explaining: "this build had N
	// clusters / M routes".
	hspan.SetAttributes(
		attribute.Int("xds.clusters", len(clusters)),
		attribute.Int("xds.listeners", len(listeners)),
		attribute.Int("xds.route_configs", len(routes)),
		attribute.Int("xds.routes_total", totalRoutes),
	)

	// --- index_build phase ---
	// Build the per-call policy index from the warm local informer cache.
	// All reads are in-memory — no API server round-trips.
	// Timed separately from the mutation loops so regression in cache or
	// protobuf template construction is attributable independently.
	ibStart := time.Now()
	ctx, ibspan := tr.Start(ctx, "index_build")
	idx, err := extcache.BuildPolicyIndexFromClient(ctx, s.client, s.cfg.CorazaRouteBaseDirectives)
	ibspan.End()
	extmetrics.PhaseDuration.WithLabelValues("index_build").Observe(time.Since(ibStart).Seconds())
	if err != nil {
		s.log.Error("build policy index", "err", err)
		hspan.RecordError(err)
		outcome = outcomeError
		return nil, err
	}

	var (
		hcmCount        int
		localReplyCount int
		tppCount        int
		vhCount         int
		replaced        map[string]*extcache.ConnectorInfo
		connOffline     map[string]*extcache.ConnectorInfo
	)

	// --- mutate phase ---
	// All xDS iteration: WAF HCM injection + per-route TPP config + connector
	// cluster/route rewiring. Timed as a single phase.
	//
	// NOTE: proto pack/unpack cost accumulated inside the mutation loops
	// (phase="anypb" in the perf prototype) is NOT separately isolatable here
	// because the production mutate package functions do not expose a Timers
	// accumulator. The full mutate duration subsumes anypb cost. To separate
	// it, the mutate package would need to be refactored to thread through
	// timing hooks (matching the prototype's InjectCorazaListenerFiltersT API).
	mutStart := time.Now()
	mctx, mspan := tr.Start(ctx, "mutate")

	// --- TPP / WAF family ---
	_, tppListenersSpan := tr.Start(mctx, "tpp.listeners")
	for _, l := range listeners {
		n, mutErr := mutate.InjectCorazaListenerFilters(l, &s.cfg.Coraza)
		if mutErr != nil {
			s.log.Error("inject coraza listener filter", "listener", l.GetName(), "err", mutErr)
			tppListenersSpan.RecordError(mutErr)
			tppListenersSpan.End()
			mspan.RecordError(mutErr)
			mspan.End()
			extmetrics.PhaseDuration.WithLabelValues("mutate").Observe(time.Since(mutStart).Seconds())
			hspan.RecordError(mutErr)
			outcome = outcomeError
			return nil, mutErr
		}
		hcmCount += n

		// Attach the branded error page (local_reply_config) to the same
		// RDS-based HCMs. No-op when disabled or no body is configured. Like the
		// Coraza injector, this only errors on a genuinely malformed HCM — with
		// failOpen:false on the downstream EG a returned error blocks the xDS
		// update, so the injector is fail-safe by construction (missing/empty
		// content is a no-op, not an error).
		lr, lrErr := mutate.InjectLocalReplyConfig(l, &s.cfg.LocalReply)
		if lrErr != nil {
			s.log.Error("inject local reply config", "listener", l.GetName(), "err", lrErr)
			tppListenersSpan.RecordError(lrErr)
			tppListenersSpan.End()
			mspan.RecordError(lrErr)
			mspan.End()
			extmetrics.PhaseDuration.WithLabelValues("mutate").Observe(time.Since(mutStart).Seconds())
			hspan.RecordError(lrErr)
			outcome = outcomeError
			return nil, lrErr
		}
		localReplyCount += lr
	}
	tppListenersSpan.SetAttributes(
		attribute.Int("hcm.injected", hcmCount),
		attribute.Int("hcm.local_reply_applied", localReplyCount),
	)
	tppListenersSpan.End()

	_, tppRoutesSpan := tr.Start(mctx, "tpp.routes")
	for _, rc := range routes {
		n, mutErr := mutate.ApplyTPPRouteConfig(rc, idx, &s.cfg.Coraza)
		if mutErr != nil {
			s.log.Error("apply tpp route config", "route_config", rc.GetName(), "err", mutErr)
			tppRoutesSpan.RecordError(mutErr)
			tppRoutesSpan.End()
			mspan.RecordError(mutErr)
			mspan.End()
			extmetrics.PhaseDuration.WithLabelValues("mutate").Observe(time.Since(mutStart).Seconds())
			hspan.RecordError(mutErr)
			outcome = outcomeError
			return nil, mutErr
		}
		tppCount += n
	}
	tppRoutesSpan.SetAttributes(attribute.Int("routes.tpp_applied", tppCount))
	tppRoutesSpan.End()

	// --- Connector family ---
	// Replace clusters BEFORE adding CONNECT routes so route wiring sees the
	// final cluster set. Apply connector routes AFTER TPP so CONNECT routes
	// do not receive Coraza per-route config (matching NSO EPP ordering).
	_, connClustersSpan := tr.Start(mctx, "connector.clusters")
	replaced, connOffline, err = mutate.ReplaceConnectorClusters(
		clusters, idx, s.cfg.ConnectorInternalListener,
	)
	connClustersSpan.SetAttributes(attribute.Int("clusters.replaced", len(replaced)))
	connClustersSpan.End()
	if err != nil {
		s.log.Error("replace connector clusters", "err", err)
		mspan.RecordError(err)
		mspan.End()
		extmetrics.PhaseDuration.WithLabelValues("mutate").Observe(time.Since(mutStart).Seconds())
		hspan.RecordError(err)
		outcome = outcomeError
		return nil, err
	}

	_, connRoutesSpan := tr.Start(mctx, "connector.routes")
	for _, rc := range routes {
		n, mutErr := mutate.ApplyConnectorRoutes(rc, idx, replaced, connOffline)
		if mutErr != nil {
			s.log.Error("apply connector routes", "route_config", rc.GetName(), "err", mutErr)
			connRoutesSpan.RecordError(mutErr)
			connRoutesSpan.End()
			mspan.RecordError(mutErr)
			mspan.End()
			extmetrics.PhaseDuration.WithLabelValues("mutate").Observe(time.Since(mutStart).Seconds())
			hspan.RecordError(mutErr)
			outcome = outcomeError
			return nil, mutErr
		}
		vhCount += n
	}
	connRoutesSpan.SetAttributes(attribute.Int("vhosts.connector_applied", vhCount))
	connRoutesSpan.End()
	mspan.End()

	extmetrics.PhaseDuration.WithLabelValues("mutate").Observe(time.Since(mutStart).Seconds())

	// Record per-mutation-family counters. These accumulate across builds; use
	// rate() in Prometheus / PromQL to derive per-build averages.
	extmetrics.WAFHCMMutationsTotal.Add(float64(hcmCount))
	extmetrics.LocalReplyMutationsTotal.Add(float64(localReplyCount))
	extmetrics.WAFRouteMutationsTotal.Add(float64(tppCount))
	extmetrics.ConnectorClustersTotal.Add(float64(len(replaced)))
	extmetrics.ConnectorRoutesTotal.Add(float64(vhCount))

	s.log.Info("PostTranslateModify",
		"clusters", len(clusters),
		"listeners", len(listeners),
		"route_configs", len(routes),
		"hcm_filters_injected", hcmCount,
		"local_reply_applied", localReplyCount,
		"routes_tpp_applied", tppCount,
		"clusters_replaced", len(replaced),
		"clusters_offline", len(connOffline),
		"vhosts_connector_applied", vhCount,
	)

	return &pb.PostTranslateModifyResponse{
		Clusters:  clusters,
		Secrets:   req.GetSecrets(), // pass through unchanged
		Listeners: listeners,
		Routes:    routes,
	}, nil
}
