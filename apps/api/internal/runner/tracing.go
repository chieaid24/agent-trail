// Package runner traces the flows in docs/operations/observability.md.
package runner

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// recordQueueWaitSpan covers creation through the first claim.
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
	recordSpanError(span, err)
	span.End()
}

func recordSpanError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

func claimAttrs(c *Claim) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("task.id", c.TaskID),
		attribute.String("task.attempt_id", c.AttemptID),
	}
}
