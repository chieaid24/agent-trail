package observability

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerName scopes every span the control plane emits.
const TracerName = "agent-trail"

// Telemetry owns one process's metrics and trace exporters.
type Telemetry struct {
	Metrics   *Registry
	shutdowns []func(context.Context) error
}

// Setup wires Prometheus plus optional plaintext OTLP/gRPC export.
func Setup(service, endpoint string, logger *slog.Logger) (*Telemetry, error) {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.LogAttrs(context.Background(), slog.LevelWarn, "telemetry export error",
			slog.String("event", "otel_error"),
			slog.String("error", err.Error()),
		)
	}))

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("agent-trail-"+service),
	))
	if err != nil {
		return nil, err
	}

	exportOff := endpoint == "" || strings.EqualFold(endpoint, "off")

	var readers []sdkmetric.Reader
	var tel Telemetry
	if !exportOff {
		metricExp, err := otlpmetricgrpc.New(context.Background(),
			otlpmetricgrpc.WithEndpoint(endpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}
		readers = append(readers, sdkmetric.NewPeriodicReader(metricExp))
	}

	reg, err := newRegistry(res, readers)
	if err != nil {
		return nil, err
	}
	tel.Metrics = reg
	tel.shutdowns = append(tel.shutdowns, reg.shutdown)

	if !exportOff {
		traceExp, err := otlptracegrpc.New(context.Background(),
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{}))
		tel.shutdowns = append(tel.shutdowns, tp.Shutdown)
	}
	return &tel, nil
}

// Shutdown flushes pending telemetry; call on binary exit.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range t.shutdowns {
		errs = append(errs, fn(ctx))
	}
	return errors.Join(errs...)
}

// Tracer returns the control-plane tracer; a no-op unless Setup ran.
func Tracer() trace.Tracer { return otel.Tracer(TracerName) }

// WithTraceParent binds ctx to an existing 32-hex correlation ID.
func WithTraceParent(ctx context.Context, traceID string) context.Context {
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		return ctx
	}
	// A valid remote parent also needs a non-zero span ID.
	var sid trace.SpanID
	binary.BigEndian.PutUint64(sid[:], binary.BigEndian.Uint64(tid[8:])|1)
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))
}
