package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func testExecutor(s *Store, ts *task.Store) *Executor {
	return &Executor{
		Tasks:         ts,
		Store:         s,
		Adapter:       agent.NewFake(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		LeaseDuration: time.Minute,
	}
}

func timelineTypes(t *testing.T, ts *task.Store, taskID string) []string {
	t.Helper()
	events, err := ts.Events(context.Background(), taskID, 0)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.EventType
	}
	return types
}

func assertSubsequence(t *testing.T, got, want []string) {
	t.Helper()
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Fatalf("timeline %v missing ordered subsequence %v (matched %d)",
			got, want, i)
	}
}

// TestExecuteCompletesFakeTaskEndToEnd is the "fake task completes end to
// end with a full timeline" acceptance criterion.
func TestExecuteCompletesFakeTaskEndToEnd(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if err := testExecutor(s, ts).Execute(ctx, r.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("task status = %s, want completed", got.Status)
	}

	assertSubsequence(t, timelineTypes(t, ts, tk.ID), []string{
		"task.created", "task.queued", "task.provisioning",
		"workspace.provisioning", "workspace.ready", "task.planning",
		"agent.started", "plan.created", "task.executing", "file.changed",
		"command.requested", "command.started", "command.output",
		"command.completed", "agent.message", "agent.completed",
		"task.validating", "cleanup.completed", "validation.started",
		"validation.completed", "task.publishing", "publishing.skipped",
		"task.awaiting_review", "task.completed",
	})

	// The attempt closed with the task and the lease is gone.
	var attemptStatus string
	var leaseOwner *string
	if err := db.QueryRowContext(ctx, `
		SELECT status, lease_owner FROM task_attempts WHERE id = $1`,
		c.AttemptID).Scan(&attemptStatus, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != "completed" {
		t.Errorf("attempt status = %s, want completed", attemptStatus)
	}
	if leaseOwner != nil {
		t.Errorf("lease_owner = %v, want released", *leaseOwner)
	}
}

// TestExecuteRecoversExpiredMidFlightAttempt: a successor claims an attempt
// whose owner died mid-executing and drives it to completed.
func TestExecuteRecoversExpiredMidFlightAttempt(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	dead := mustRegister(t, s)
	successor := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	first, err := s.Claim(ctx, dead.ID, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("claim = %+v, %v", first, err)
	}
	for _, to := range []task.Status{
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting,
	} {
		if _, err := ts.Transition(ctx, tk.ID, task.TransitionParams{
			To: to, Source: "runner",
		}); err != nil {
			t.Fatal(err)
		}
	}
	expireLease(t, db, first.AttemptID)

	c, err := s.Claim(ctx, successor.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("recovery claim = %+v, %v", c, err)
	}
	if c.TaskStatus != task.StatusExecuting {
		t.Fatalf("recovered status = %s, want executing", c.TaskStatus)
	}
	if err := testExecutor(s, ts).Execute(ctx, successor.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("recovered task status = %s, want completed", got.Status)
	}
	// The successor re-ran the agent in a fresh workspace (at-least-once).
	assertSubsequence(t, timelineTypes(t, ts, tk.ID), []string{
		"task.executing", "workspace.provisioning", "workspace.ready",
		"agent.started", "agent.completed", "task.validating",
		"task.completed",
	})
}

// TestExecuteRecoversAttemptPastTheAgent: recovery at validating must not
// re-run the agent, only finish the remaining stages.
func TestExecuteRecoversAttemptPastTheAgent(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	dead := mustRegister(t, s)
	successor := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	first, err := s.Claim(ctx, dead.ID, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("claim = %+v, %v", first, err)
	}
	for _, to := range []task.Status{
		task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating,
	} {
		if _, err := ts.Transition(ctx, tk.ID, task.TransitionParams{
			To: to, Source: "runner",
		}); err != nil {
			t.Fatal(err)
		}
	}
	expireLease(t, db, first.AttemptID)

	c, err := s.Claim(ctx, successor.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("recovery claim = %+v, %v", c, err)
	}
	if err := testExecutor(s, ts).Execute(ctx, successor.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("task status = %s, want completed", got.Status)
	}
	for _, unwanted := range []string{"workspace.provisioning", "agent.started"} {
		for _, e := range timelineTypes(t, ts, tk.ID) {
			if e == unwanted {
				t.Fatalf("timeline re-ran the agent: found %s after recovery at validating", e)
			}
		}
	}
}

// TestExecuteStopsOnCancelledTask: a task cancelled between claim and drive
// is left alone; the executor releases the lease and reports the conflict.
func TestExecuteStopsOnCancelledTask(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if _, err := ts.Cancel(ctx, tk.ID, "operator cancelled"); err != nil {
		t.Fatal(err)
	}

	err = testExecutor(s, ts).Execute(ctx, r.ID, c)
	var invalid *task.InvalidTransitionError
	if !errors.As(err, &invalid) {
		t.Fatalf("Execute = %v, want InvalidTransitionError", err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCancelled {
		t.Fatalf("task status = %s, want cancelled untouched", got.Status)
	}
	var leaseOwner *string
	if err := db.QueryRowContext(ctx, `
		SELECT lease_owner FROM task_attempts WHERE id = $1`,
		c.AttemptID).Scan(&leaseOwner); err != nil {
		t.Fatal(err)
	}
	if leaseOwner != nil {
		t.Errorf("lease_owner = %v, want released", *leaseOwner)
	}
}
