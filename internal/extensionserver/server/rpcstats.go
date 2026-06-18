package server

import (
	"context"
	"strings"

	"google.golang.org/grpc/stats"

	extmetrics "go.datum.net/network-services-operator/internal/extensionserver/metrics"
)

const (
	// postTranslateModifyMethod is the gRPC method suffix we time; health/reflection
	// probes are filtered out so they don't skew the nso_extension_rpc_duration_seconds
	// histogram.
	postTranslateModifyMethod = "PostTranslateModify"

	// outcomeSuccess and outcomeError are the two label values for the outcome
	// dimension on nso_extension_hook_duration_seconds,
	// nso_extension_hook_calls_total, and nso_extension_rpc_duration_seconds.
	outcomeSuccess = "success"
	outcomeError   = "error"
)

type rpcStatsKey struct{}

type rpcStatsHolder struct{ tracked bool }

// RPCStatsHandler is a grpc.StatsHandler that records
// nso_extension_rpc_duration_seconds for PostTranslateModify calls. Register
// it via grpc.StatsHandler(server.NewRPCStatsHandler()) in cmd/run.go alongside
// the existing credentials and recovery interceptor.
//
// Why a stats.Handler and not a unary interceptor:
// Interceptors run AFTER the request is decoded and return BEFORE the response
// is encoded, so they cannot see the gRPC codec (marshal/unmarshal) time.
// The stats.Handler HandleRPC(End) span brackets the entire RPC including the
// codec — this is the codec-inclusive measurement whose delta versus
// nso_extension_hook_duration_seconds (interceptor-level) isolates the large
// (~7.4 MB at N=1000) proto marshal/unmarshal cost.
type RPCStatsHandler struct{}

// NewRPCStatsHandler returns a stats handler for full-RPC timing on
// PostTranslateModify calls.
func NewRPCStatsHandler() *RPCStatsHandler { return &RPCStatsHandler{} }

func (h *RPCStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	if strings.Contains(info.FullMethodName, postTranslateModifyMethod) {
		return context.WithValue(ctx, rpcStatsKey{}, &rpcStatsHolder{tracked: true})
	}
	return ctx
}

func (h *RPCStatsHandler) HandleRPC(ctx context.Context, s stats.RPCStats) {
	holder, ok := ctx.Value(rpcStatsKey{}).(*rpcStatsHolder)
	if !ok || !holder.tracked {
		return
	}
	if end, ok := s.(*stats.End); ok {
		outcome := outcomeSuccess
		if end.Error != nil {
			outcome = outcomeError
		}
		// End.BeginTime/EndTime bracket the whole RPC: decode + handler + encode.
		extmetrics.RPCDuration.WithLabelValues(outcome).Observe(
			end.EndTime.Sub(end.BeginTime).Seconds(),
		)
	}
}

func (h *RPCStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (h *RPCStatsHandler) HandleConn(context.Context, stats.ConnStats) {}
