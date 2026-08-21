package observability

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

type otlpReceiver struct {
	collectormetricspb.UnimplementedMetricsServiceServer
	collectortracepb.UnimplementedTraceServiceServer
	metrics chan struct{}
	traces  chan struct{}
}

func (r *otlpReceiver) Export(context.Context, *collectormetricspb.ExportMetricsServiceRequest) (*collectormetricspb.ExportMetricsServiceResponse, error) {
	select {
	case r.metrics <- struct{}{}:
	default:
	}
	return &collectormetricspb.ExportMetricsServiceResponse{}, nil
}

func (r *otlpReceiver) ExportTrace(context.Context, *collectortracepb.ExportTraceServiceRequest) (*collectortracepb.ExportTraceServiceResponse, error) {
	select {
	case r.traces <- struct{}{}:
	default:
	}
	return &collectortracepb.ExportTraceServiceResponse{}, nil
}

type traceReceiver struct{ *otlpReceiver }

type captureSpanExporter struct {
	spans chan int
}

func (e *captureSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.spans <- len(spans)
	return nil
}

func (*captureSpanExporter) Shutdown(context.Context) error { return nil }

func (r traceReceiver) Export(ctx context.Context, req *collectortracepb.ExportTraceServiceRequest) (*collectortracepb.ExportTraceServiceResponse, error) {
	return r.ExportTrace(ctx, req)
}

func TestSetupExportsMetricsAndTraces(t *testing.T) {
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	receiver := &otlpReceiver{
		metrics: make(chan struct{}, 1),
		traces:  make(chan struct{}, 1),
	}
	collectormetricspb.RegisterMetricsServiceServer(server, receiver)
	collectortracepb.RegisterTraceServiceServer(server, traceReceiver{receiver})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	telemetry, err := Setup("test", listener.Addr().String(), logger)
	if err != nil {
		t.Fatal(err)
	}
	telemetry.Metrics.Counter("agent_trail_test_total", "Test counter.").Inc()
	_, span := Tracer().Start(t.Context(), "test.span")
	span.End()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := telemetry.Metrics.provider.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	for name, ch := range map[string]<-chan struct{}{
		"metrics": receiver.metrics,
		"traces":  receiver.traces,
	} {
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatalf("no OTLP %s export received: %v", name, ctx.Err())
		}
	}
}

func TestSetupExportsSpansLocallyWithOTLPOff(t *testing.T) {
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	exporter := &captureSpanExporter{spans: make(chan int, 1)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	telemetry, err := Setup("test", "off", logger, WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	_, span := Tracer().Start(t.Context(), "local.span")
	span.End()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := telemetry.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case count := <-exporter.spans:
		if count != 1 {
			t.Fatalf("exported spans = %d, want 1", count)
		}
	case <-ctx.Done():
		t.Fatalf("local span export missing: %v", ctx.Err())
	}
}
