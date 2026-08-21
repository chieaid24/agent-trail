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
	processor := newTaskContextProcessor(sdktrace.NewSimpleSpanProcessor(store))
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
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
	if len(trace.Spans) != 2 {
		t.Fatalf("span count = %d, want 2 task-scoped spans", len(trace.Spans))
	}
	parentSpan, childSpan := trace.Spans[0], trace.Spans[1]
	if parentSpan.Name != "task.execute" || parentSpan.TaskAttemptID == nil || *parentSpan.TaskAttemptID != attemptID {
		t.Fatalf("parent span = %+v", parentSpan)
	}
	if childSpan.Name != "task.validate" || childSpan.ParentSpanID == nil ||
		*childSpan.ParentSpanID != parentSpan.SpanID || childSpan.TaskAttemptID == nil ||
		*childSpan.TaskAttemptID != attemptID {
		t.Fatalf("child span = %+v", childSpan)
	}
	if childSpan.Attributes["runner.id"] != "runner-1" {
		t.Fatalf("child attributes = %+v", childSpan.Attributes)
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

func TestTaskSpanIdentityAndAttemptOwnershipConstraints(t *testing.T) {
	db := dbtest.Open(t)
	tasks := task.NewStore(db)
	first, err := tasks.Create(t.Context(), task.CreateParams{
		Title: "First trace", Instructions: "Record the first trace.",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tasks.Create(t.Context(), task.CreateParams{
		Title: "Second trace", Instructions: "Record the second trace.",
	})
	if err != nil {
		t.Fatal(err)
	}
	var firstAttempt, secondAttempt string
	if err := db.QueryRowContext(t.Context(),
		`SELECT id FROM task_attempts WHERE task_id = $1`, first.ID).Scan(&firstAttempt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(),
		`SELECT id FROM task_attempts WHERE task_id = $1`, second.ID).Scan(&secondAttempt); err != nil {
		t.Fatal(err)
	}

	const insert = `INSERT INTO task_spans (
		task_id, task_attempt_id, trace_id, span_id, name, kind,
		start_time, end_time, status_code
	) VALUES ($1, $2, $3, $4, 'operation', 'internal', now(), now(), 'Unset')`
	if _, err := db.ExecContext(t.Context(), insert, first.ID, secondAttempt,
		"11111111111111111111111111111111", "aaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("cross-task attempt reference was accepted")
	}
	for _, traceID := range []string{
		"11111111111111111111111111111111",
		"22222222222222222222222222222222",
	} {
		if _, err := db.ExecContext(t.Context(), insert, first.ID, firstAttempt,
			traceID, "aaaaaaaaaaaaaaaa"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM task_spans WHERE task_id = $1`, first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("stored spans = %d, want 2 trace-scoped identities", count)
	}
}
