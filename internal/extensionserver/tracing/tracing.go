// Package tracing wires OpenTelemetry trace export for the NSO extension server.
// It provides per-request span trees (RPC span + index_build / mutate child spans)
// exported via OTLP/gRPC to the in-cluster trace backend (Tempo), letting operators
// see which phase owns the long tail on slow builds — something aggregate Prometheus
// histograms cannot show.
//
// Tracing is gated by the OTEL_TRACES_ENABLED environment variable (default: false).
// When disabled (or when the OTLP backend is unreachable), the server falls back to
// OTel's noop tracer and continues serving without any tracing overhead or export
// errors.  Enable for:
//   - Initial rollout / behaviour validation against the perf prototype.
//   - Incidents where hook p99 > 3× hook p50 (tail divergence unexplained by phase
//     histograms alone).
//   - Scale validation runs above N=2000 (new territory beyond measured range).
package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the instrumentation scope name used for manual handler spans.
// Keep in sync with the prototype so trace searches in Tempo show both.
const TracerName = "go.datum.net/network-services-operator/extensionserver"

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Setup installs a global TracerProvider that exports spans via OTLP/gRPC to
// the in-cluster trace backend (Tempo accepts OTLP on :4317) using a
// BatchSpanProcessor. Returns a shutdown func that flushes the processor.
//
// Environment overrides:
//   - OTEL_EXPORTER_OTLP_ENDPOINT (default: telemetry-system-tempo.telemetry-system:4317)
//   - OTEL_SERVICE_NAME           (default: nso-extension-server)
//
// AlwaysSample is used by default: normal call volume is low (1–5 builds/minute
// per spec change) and we must NOT drop the rare slow tail builds we need to
// diagnose. Switch to a head-based sampler (10–20%) under continuous high-volume
// churn (>100 builds/minute) by setting OTEL_TRACES_SAMPLER=parentbased_traceidratio
// and OTEL_TRACES_SAMPLER_ARG=0.1.
//
// Setup is non-fatal: if the OTLP exporter cannot connect, the server continues
// without tracing. The caller must check the returned error but may proceed.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	endpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "telemetry-system-tempo.telemetry-system:4317")
	svc := envOr("OTEL_SERVICE_NAME", "nso-extension-server")

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(), // in-cluster, no TLS on the OTLP receiver
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(svc),
			attribute.String("component", "extension-server"),
		),
	)
	if err != nil {
		return nil, err
	}

	// BatchSpanProcessor: batches spans before OTLP export for low per-call
	// overhead. The batch processor flushes on Shutdown (returned func).
	bsp := sdktrace.NewBatchSpanProcessor(exp)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// Tracer returns the extension-server tracer for manual handler spans.
// When no TracerProvider has been configured (tracing disabled), this returns
// OTel's global noop tracer — span creation and attribute setting compile and
// run without export cost or errors.
func Tracer() trace.Tracer { return otel.Tracer(TracerName) }
