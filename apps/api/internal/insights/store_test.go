package insights

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

func createTask(t *testing.T, db *sql.DB, title string) task.Task {
	t.Helper()
	created, err := task.NewStore(db).Create(context.Background(), task.CreateParams{
		Title: title, Instructions: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func transition(t *testing.T, db *sql.DB, taskID string, steps ...task.TransitionParams) {
	t.Helper()
	store := task.NewStore(db)
	for _, step := range steps {
		if _, err := store.Transition(context.Background(), taskID, step); err != nil {
			t.Fatalf("transition to %s: %v", step.To, err)
		}
	}
}

func to(statuses ...task.Status) []task.TransitionParams {
	steps := make([]task.TransitionParams, 0, len(statuses))
	for _, status := range statuses {
		steps = append(steps, task.TransitionParams{To: status})
	}
	return steps
}

func attemptID(t *testing.T, db *sql.DB, taskID string, number int) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(context.Background(), `
		SELECT id FROM task_attempts
		WHERE task_id = $1 AND attempt_number = $2`, taskID, number).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertSpan(t *testing.T, db *sql.DB, taskID, attemptID, spanID, name string, start time.Time, duration time.Duration) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO task_spans (task_id, task_attempt_id, trace_id, span_id,
			name, kind, start_time, end_time, status_code)
		VALUES ($1, $2, '0123456789abcdef0123456789abcdef', $3, $4,
			'internal', $5, $6, 'Ok')`,
		taskID, attemptID, spanID, name, start, start.Add(duration)); err != nil {
		t.Fatal(err)
	}
}

func appendEvent(t *testing.T, db *sql.DB, attemptID, eventType string, payload map[string]any) {
	t.Helper()
	if err := task.NewStore(db).AppendAttemptEvent(context.Background(),
		attemptID, eventType, "agent", payload); err != nil {
		t.Fatal(err)
	}
}

func insertValidation(t *testing.T, db *sql.DB, attemptID, name string, status validation.Status, trusted bool) {
	t.Helper()
	if err := validation.NewStore(db).Insert(context.Background(), attemptID, validation.Result{
		Name: name, Category: "unit_test", Command: []string{"go", "test"},
		Status: status, DurationMS: 10, TrustedExecution: trusted,
	}); err != nil {
		t.Fatal(err)
	}
}

func ms(v *int64) int64 {
	if v == nil {
		return -1
	}
	return *v
}

func TestTaskInsightsUnknownTask(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	if _, err := store.TaskInsights(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := store.TaskInsights(t.Context(), "not-a-uuid"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTaskInsightsQueuedTaskHasNoDurations(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Queued")

	got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != created.ID || got.TaskStatus != "queued" || len(got.Attempts) != 1 {
		t.Fatalf("insights = %+v", got)
	}
	a := got.Attempts[0]
	if a.AttemptNumber != 1 || a.Status != "active" || a.StartedAt != nil ||
		a.QueueWaitMS != nil || a.ProvisioningMS != nil || a.AgentSessionMS != nil ||
		a.ValidationMS != nil || a.PublishingMS != nil || a.TotalRuntimeMS != nil ||
		a.Validation != nil || a.Cost != nil {
		t.Fatalf("attempt = %+v", a)
	}
	// task.created + task.queued
	if a.EventCount != 2 || got.Overall.EventCount != 2 {
		t.Fatalf("event counts = %d / %d", a.EventCount, got.Overall.EventCount)
	}
	if got.Overall.AttemptCount != 1 || got.Overall.TotalRuntimeMS != nil ||
		got.Overall.Validation != nil || got.Overall.Cost != nil {
		t.Fatalf("overall = %+v", got.Overall)
	}
}

func TestTaskInsightsPopulatedAttempt(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Populated")
	transition(t, db, created.ID, to(task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview, task.StatusCompleted)...)
	attempt := attemptID(t, db, created.ID, 1)
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	insertSpan(t, db, created.ID, attempt, "aaaaaaaaaaaaaaa1", spanAttempt, start, 12*time.Second)
	insertSpan(t, db, created.ID, attempt, "aaaaaaaaaaaaaaa2", spanProvisioning, start, 1500*time.Millisecond)
	insertSpan(t, db, created.ID, attempt, "aaaaaaaaaaaaaaa3", spanAgentSession, start.Add(2*time.Second), 7*time.Second)
	insertSpan(t, db, created.ID, attempt, "aaaaaaaaaaaaaaa4", spanValidation, start.Add(9*time.Second), 2*time.Second)
	insertSpan(t, db, created.ID, attempt, "aaaaaaaaaaaaaaa5", "task.queue_wait", start.Add(-time.Minute), time.Minute)
	appendEvent(t, db, attempt, eventCostUpdate, map[string]any{"total_cost_usd": 0.01})
	appendEvent(t, db, attempt, eventCostUpdate, map[string]any{"total_cost_usd": 0.0425})
	insertValidation(t, db, attempt, "unit", validation.StatusPassed, true)
	insertValidation(t, db, attempt, "lint", validation.StatusFailed, true)
	insertValidation(t, db, attempt, "agent-smoke", validation.StatusPassed, false)

	got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskStatus != "completed" || len(got.Attempts) != 1 {
		t.Fatalf("insights = %+v", got)
	}
	a := got.Attempts[0]
	if a.Status != "completed" || a.StartedAt == nil || a.CompletedAt == nil || a.FailureCode != nil {
		t.Fatalf("attempt = %+v", a)
	}
	if a.QueueWaitMS == nil || *a.QueueWaitMS < 0 {
		t.Fatalf("queue wait = %v", a.QueueWaitMS)
	}
	if ms(a.TotalRuntimeMS) != 12000 || ms(a.ProvisioningMS) != 1500 ||
		ms(a.AgentSessionMS) != 7000 || ms(a.ValidationMS) != 2000 {
		t.Fatalf("durations = runtime %d provisioning %d agent %d validation %d",
			ms(a.TotalRuntimeMS), ms(a.ProvisioningMS), ms(a.AgentSessionMS), ms(a.ValidationMS))
	}
	if a.PublishingMS == nil || *a.PublishingMS < 0 {
		t.Fatalf("publishing = %v", a.PublishingMS)
	}
	// created, queued, 7 transitions, 2 cost updates
	if a.EventCount != 11 {
		t.Fatalf("event count = %d", a.EventCount)
	}
	if a.Validation == nil || *a.Validation != (ValidationSummary{
		Total: 3, Passed: 2, Failed: 1, Trusted: 2}) {
		t.Fatalf("validation = %+v", a.Validation)
	}
	if a.Cost == nil || a.Cost.TotalUSD != 0.0425 || a.Cost.UpdateCount != 2 {
		t.Fatalf("cost = %+v", a.Cost)
	}
	if ms(got.Overall.TotalRuntimeMS) != 12000 || got.Overall.EventCount != 11 ||
		got.Overall.Validation == nil || got.Overall.Validation.Total != 3 ||
		got.Overall.Cost == nil || got.Overall.Cost.TotalUSD != 0.0425 {
		t.Fatalf("overall = %+v", got.Overall)
	}
}

func TestTaskInsightsRecoveredAttemptSumsSpans(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Recovered")
	transition(t, db, created.ID, to(task.StatusProvisioning)...)
	attempt := attemptID(t, db, created.ID, 1)
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	insertSpan(t, db, created.ID, attempt, "bbbbbbbbbbbbbbb1", spanAttempt, start, 3*time.Second)
	insertSpan(t, db, created.ID, attempt, "bbbbbbbbbbbbbbb2", spanAttempt, start.Add(time.Minute), 4*time.Second)
	insertSpan(t, db, created.ID, attempt, "bbbbbbbbbbbbbbb3", spanProvisioning, start, 500*time.Millisecond)
	insertSpan(t, db, created.ID, attempt, "bbbbbbbbbbbbbbb4", spanProvisioning, start.Add(time.Minute), 250*time.Millisecond)

	got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := got.Attempts[0]
	if a.TotalRuntimeMS != nil || ms(a.ProvisioningMS) != 750 {
		t.Fatalf("durations = runtime %d provisioning %d", ms(a.TotalRuntimeMS), ms(a.ProvisioningMS))
	}
	transition(t, db, created.ID, task.TransitionParams{To: task.StatusFailed})
	ended, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ms(ended.Attempts[0].TotalRuntimeMS) != 7000 {
		t.Fatalf("ended runtime = %d", ms(ended.Attempts[0].TotalRuntimeMS))
	}
}

func TestTaskInsightsFailedAttemptKeepsPartialData(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Failed")
	transition(t, db, created.ID, to(task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating)...)
	transition(t, db, created.ID, task.TransitionParams{
		To: task.StatusFailed, FailureCode: "validation_failed",
		FailureMessage: "2 of 148 tests failed",
	})
	attempt := attemptID(t, db, created.ID, 1)
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	insertSpan(t, db, created.ID, attempt, "ccccccccccccccc1", spanAttempt, start, 5*time.Second)
	insertSpan(t, db, created.ID, attempt, "ccccccccccccccc2", spanAgentSession, start, 4*time.Second)
	insertValidation(t, db, attempt, "unit", validation.StatusFailed, true)
	insertValidation(t, db, attempt, "build", validation.StatusError, true)

	got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := got.Attempts[0]
	if a.Status != "failed" || a.FailureCode == nil || *a.FailureCode != "validation_failed" ||
		a.CompletedAt == nil {
		t.Fatalf("attempt = %+v", a)
	}
	if ms(a.TotalRuntimeMS) != 5000 || ms(a.AgentSessionMS) != 4000 ||
		a.ProvisioningMS != nil || a.ValidationMS != nil || a.PublishingMS != nil {
		t.Fatalf("durations = %+v", a)
	}
	if a.Validation == nil || *a.Validation != (ValidationSummary{
		Total: 2, Failed: 1, Error: 1, Trusted: 2}) {
		t.Fatalf("validation = %+v", a.Validation)
	}
	if a.Cost != nil || got.Overall.Cost != nil {
		t.Fatalf("cost = %+v, want nil without cost events", a.Cost)
	}
}

func TestTaskInsightsMultiAttemptKeepsAssociation(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Retried")
	transition(t, db, created.ID, to(task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview, task.StatusRevisionRequested, task.StatusQueued,
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting)...)
	first := attemptID(t, db, created.ID, 1)
	second := attemptID(t, db, created.ID, 2)
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	insertSpan(t, db, created.ID, first, "ddddddddddddddd1", spanAttempt, start, 10*time.Second)
	insertSpan(t, db, created.ID, first, "ddddddddddddddd2", spanValidation, start, 2*time.Second)
	insertSpan(t, db, created.ID, second, "ddddddddddddddd3", spanProvisioning, start.Add(time.Hour), time.Second)
	appendEvent(t, db, first, eventCostUpdate, map[string]any{"cost_usd": 0.01})
	appendEvent(t, db, first, eventCostUpdate, map[string]any{"cost_usd": 0.015})
	appendEvent(t, db, second, eventCostUpdate, map[string]any{"total_cost_usd": 0.04})
	insertValidation(t, db, first, "unit", validation.StatusPassed, true)
	insertValidation(t, db, second, "unit", validation.StatusTimedOut, true)

	got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskStatus != "executing" || len(got.Attempts) != 2 {
		t.Fatalf("insights = %+v", got)
	}
	a1, a2 := got.Attempts[0], got.Attempts[1]
	if a1.AttemptNumber != 1 || a1.Status != "superseded" || a1.CompletedAt == nil ||
		ms(a1.TotalRuntimeMS) != 10000 || ms(a1.ValidationMS) != 2000 ||
		a1.PublishingMS == nil || a1.ProvisioningMS != nil {
		t.Fatalf("attempt 1 = %+v", a1)
	}
	if a1.Cost == nil || a1.Cost.TotalUSD != 0.025 || a1.Cost.UpdateCount != 2 ||
		a1.Validation == nil || a1.Validation.Passed != 1 {
		t.Fatalf("attempt 1 cost/validation = %+v / %+v", a1.Cost, a1.Validation)
	}
	if a2.AttemptNumber != 2 || a2.Status != "active" || a2.CompletedAt != nil ||
		a2.TotalRuntimeMS != nil || ms(a2.ProvisioningMS) != 1000 ||
		a2.ValidationMS != nil || a2.PublishingMS != nil || a2.QueueWaitMS == nil {
		t.Fatalf("attempt 2 = %+v", a2)
	}
	if a2.Cost == nil || a2.Cost.TotalUSD != 0.04 || a2.Cost.UpdateCount != 1 ||
		a2.Validation == nil || a2.Validation.TimedOut != 1 {
		t.Fatalf("attempt 2 cost/validation = %+v / %+v", a2.Cost, a2.Validation)
	}
	// attempt 2 has no runner.attempt span yet, so the overall runtime stays unknown
	if got.Overall.AttemptCount != 2 || got.Overall.TotalRuntimeMS != nil ||
		got.Overall.Cost == nil || got.Overall.Cost.TotalUSD != 0.065 ||
		got.Overall.Validation == nil || got.Overall.Validation.Total != 2 ||
		got.Overall.EventCount != a1.EventCount+a2.EventCount {
		t.Fatalf("overall = %+v", got.Overall)
	}
}

func TestTaskInsightsPublishingNeedsBothBoundaries(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Publishing")
	transition(t, db, created.ID, to(task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing)...)

	got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts[0].PublishingMS != nil {
		t.Fatalf("publishing = %d, want nil while still publishing", *got.Attempts[0].PublishingMS)
	}
}

func TestTaskInsightsNoAttempts(t *testing.T) {
	db := dbtest.Open(t)
	var id string
	if err := db.QueryRowContext(t.Context(), `INSERT INTO tasks (title, instructions, status, phase) VALUES ('No attempts', 'test', 'queued', 'pending') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, err := NewStore(db).TaskInsights(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts == nil || len(got.Attempts) != 0 || got.Overall.AttemptCount != 0 || got.Overall.TotalRuntimeMS != nil {
		t.Fatalf("insights = %+v", got)
	}
}

func TestTaskInsightsPublishedAttemptHasRecordedRuntime(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Published")
	transition(t, db, created.ID, to(task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting, task.StatusValidating, task.StatusPublishing, task.StatusAwaitingReview)...)
	attempt := attemptID(t, db, created.ID, 1)
	insertSpan(t, db, created.ID, attempt, "eeeeeeeeeeeeeee1", spanAttempt, time.Now(), 3*time.Second)
	for _, status := range []task.Status{task.StatusAwaitingReview, task.StatusRevisionRequested} {
		if status == task.StatusRevisionRequested {
			transition(t, db, created.ID, task.TransitionParams{To: status})
		}
		got, err := NewStore(db).TaskInsights(t.Context(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Attempts[0].Status != "active" || ms(got.Attempts[0].TotalRuntimeMS) != 3000 {
			t.Fatalf("%s runtime = %+v", status, got.Attempts[0])
		}
	}
}

func TestTaskInsightsDistinguishesMissingAndZeroCost(t *testing.T) {
	db := dbtest.Open(t)
	created := createTask(t, db, "Cost presence")
	id := attemptID(t, db, created.ID, 1)
	appendEvent(t, db, id, eventCostUpdate, map[string]any{"duration_ms": 10})
	missing, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Attempts[0].Cost != nil || missing.Overall.Cost != nil {
		t.Fatalf("missing cost = %+v", missing)
	}
	appendEvent(t, db, id, eventCostUpdate, map[string]any{"total_cost_usd": 0})
	reported, err := NewStore(db).TaskInsights(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reported.Attempts[0].Cost == nil || reported.Attempts[0].Cost.TotalUSD != 0 || reported.Attempts[0].Cost.UpdateCount != 1 || reported.Overall.Cost == nil {
		t.Fatalf("reported cost = %+v", reported)
	}
}
