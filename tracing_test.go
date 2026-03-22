package lplex

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestInitTracingDisabled(t *testing.T) {
	shutdown, err := InitTracing(context.Background(), TracingConfig{
		Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	// Should NOT set an SDK provider (should be noop).
	tp := otel.GetTracerProvider()
	if _, ok := tp.(*sdktrace.TracerProvider); ok {
		t.Error("expected non-SDK provider when tracing is disabled")
	}
}

func TestInitTracingNoEndpoint(t *testing.T) {
	shutdown, err := InitTracing(context.Background(), TracingConfig{
		Enabled:  true,
		Endpoint: "", // empty = disabled
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
}

func TestTracerReturnsNonNil(t *testing.T) {
	shutdown, err := InitTracing(context.Background(), TracingConfig{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	tracer := Tracer("test")
	if tracer == nil {
		t.Error("Tracer should return non-nil")
	}
}
