// Tracing spans for the runner flows listed in
// docs/operations/observability.md. The tracer is the global one Setup
// installs; without it every span is a no-op, so tests need no wiring.
package runner

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// recordQueueWaitSpan emits the queue-wait span retroactively: it starts at
// task creation and ends now, when the first claim landed.
func recordQueueWaitSpan(ctx context.Context, c *Claim) {
	if c.TaskCreatedAt.IsZero() {
		return
	}
	_, span := observability.Tracer().Start(ctx, "task.queue_wait",
		trace.WithTimestamp(c.TaskCreatedAt),
		trace.WithAttributes(claimAttrs(c)...))
	span.End(trace.WithTimestamp(time.Now()))
}

// startSpan opens a span carrying the attempt identity.
func startSpan(ctx context.Context, name string, c *Claim) (context.Context, trace.Span) {
	return observability.Tracer().Start(ctx, name,
		trace.WithAttributes(claimAttrs(c)...))
}

// endSpan closes span, recording err when non-nil.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func claimAttrs(c *Claim) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("task.id", c.TaskID),
		attribute.String("task.attempt_id", c.AttemptID),
	}
}
