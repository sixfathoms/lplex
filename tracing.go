package lplex

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.28.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TracingConfig configures OpenTelemetry distributed tracing.
type TracingConfig struct {
	// Enabled enables tracing. When false, a no-op tracer is used.
	Enabled bool

	// Endpoint is the OTLP gRPC collector endpoint (e.g. "localhost:4317").
	Endpoint string

	// ServiceName identifies this service in traces (e.g. "lplex-server", "lplex-cloud").
	ServiceName string

	// ServiceVersion is the build version.
	ServiceVersion string

	// SampleRatio controls probabilistic sampling (0.0 to 1.0).
	// 1.0 = sample everything, 0.01 = sample 1%.
	SampleRatio float64
}

// InitTracing sets up the OpenTelemetry TracerProvider and returns a shutdown
// function. If tracing is disabled, sets a no-op provider and returns a no-op
// shutdown.
func InitTracing(ctx context.Context, cfg TracingConfig) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled || cfg.Endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	sampler := sdktrace.AlwaysSample()
	if cfg.SampleRatio > 0 && cfg.SampleRatio < 1 {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRatio)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
