package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func TestTraceStoreExportsAndListsTaskSpans(t *testing.T) {
	db := dbtest.Open(t)
	created, err := task.NewStore(db).Create(t.Context(), task.CreateParams{
		Title: "Trace storage", Instructions: "Record the task trace.",
	})
	if err != nil {
		t.Fatal(err)
	}
	var attemptID string
	if err := db.QueryRowContext(t.Context(),
		`SELECT id FROM task_attempts WHERE task_id = $1`, created.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}

	store := NewTraceStore(db)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(store))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, parent := provider.Tracer(TracerName).Start(t.Context(), "task.execute",
		trace.WithAttributes(
			attribute.String("task.id", created.ID),
			attribute.String("task.attempt_id", attemptID),
			attribute.String("runner.id", "runner-1"),
		))
	_, child := provider.Tracer(TracerName).Start(ctx, "task.validate")
	child.End()
	parent.End()

	trace, err := store.ListTaskSpans(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.Spans) != 1 {
		t.Fatalf("span count = %d, want 1 task-scoped span", len(trace.Spans))
	}
	span := trace.Spans[0]
	if span.Name != "task.execute" || span.TaskAttemptID == nil || *span.TaskAttemptID != attemptID {
		t.Fatalf("span = %+v", span)
	}
	if span.Attributes["runner.id"] != "runner-1" {
		t.Fatalf("attributes = %+v", span.Attributes)
	}
}

func TestTraceStoreReturnsEmptyTrace(t *testing.T) {
	db := dbtest.Open(t)
	created, err := task.NewStore(db).Create(t.Context(), task.CreateParams{
		Title: "No trace", Instructions: "Leave trace empty.",
	})
	if err != nil {
		t.Fatal(err)
	}
	trace, err := NewTraceStore(db).ListTaskSpans(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if trace.Spans == nil || len(trace.Spans) != 0 {
		t.Fatalf("spans = %#v, want non-nil empty slice", trace.Spans)
	}
}
