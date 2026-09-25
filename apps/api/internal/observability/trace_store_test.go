package observability

import (
	"context"
	"errors"
	"strings"
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
	if childSpan.Attributes["task.id"] != created.ID {
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

func TestTraceStoreListsSpansForOneAttempt(t *testing.T) {
	db := dbtest.Open(t)
	tasks := task.NewStore(db)
	created, err := tasks.Create(t.Context(), task.CreateParams{
		Title: "Attempt trace", Instructions: "Record each attempt's trace.",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview} {
		if _, err := tasks.Transition(t.Context(), created.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatal(err)
		}
	}
	_, second, err := tasks.RequestRevision(t.Context(), created.ID, task.RevisionParams{
		Instructions: "revise", BaseCommitSHA: strings.Repeat("c", 40),
		RequestedByLogin: "alice", TriggerCommentID: 7, MaxAttempts: 5,
		IdempotencyKey: "revise:7",
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := tasks.Attempts(t.Context(), created.ID)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("attempts = %d, err = %v", len(attempts), err)
	}

	store := NewTraceStore(db)
	processor := newTaskContextProcessor(sdktrace.NewSimpleSpanProcessor(store))
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	for _, a := range []struct {
		attemptID string
		name      string
	}{{attempts[0].ID, "runner.attempt.one"}, {second.ID, "runner.attempt.two"}} {
		_, span := provider.Tracer(TracerName).Start(t.Context(), a.name,
			trace.WithAttributes(
				attribute.String("task.id", created.ID),
				attribute.String("task.attempt_id", a.attemptID),
			))
		span.End()
	}

	all, err := store.ListTaskSpans(t.Context(), created.ID)
	if err != nil || len(all.Spans) != 2 {
		t.Fatalf("all spans = %d, err = %v", len(all.Spans), err)
	}
	one, err := store.ListTaskAttemptSpans(t.Context(), created.ID, 1)
	if err != nil || len(one.Spans) != 1 || one.Spans[0].Name != "runner.attempt.one" {
		t.Fatalf("attempt 1 spans = %+v, err = %v", one.Spans, err)
	}
	two, err := store.ListTaskAttemptSpans(t.Context(), created.ID, 2)
	if err != nil || len(two.Spans) != 1 || two.Spans[0].Name != "runner.attempt.two" {
		t.Fatalf("attempt 2 spans = %+v, err = %v", two.Spans, err)
	}
	if _, err := store.ListTaskAttemptSpans(t.Context(), created.ID, 3); !errors.Is(err, task.ErrAttemptNotFound) {
		t.Fatalf("attempt 3: err = %v, want ErrAttemptNotFound", err)
	}
}
