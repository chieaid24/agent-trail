package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

func testExecutor(db *sql.DB, s *Store, ts *task.Store) *Executor {
	return &Executor{
		Tasks:         ts,
		Store:         s,
		Validations:   validation.NewStore(db),
		Evidence:      evidence.NewStore(db),
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
	if err := testExecutor(db, s, ts).Execute(ctx, r.ID, c); err != nil {
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
		"task.validating", "validation.started",
		"validation.check.completed", "validation.completed",
		"evidence.generated", "task.publishing", "cleanup.completed",
		"publishing.skipped", "task.awaiting_review", "task.completed",
	})

	// The fake flow's smoke check ran trusted and its measured exit code
	// is stored.
	results, err := validation.NewStore(db).ListForTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("validation results = %d, want 1", len(results))
	}
	smoke := results[0]
	if smoke.Name != "smoke" || smoke.Status != validation.StatusPassed ||
		!smoke.TrustedExecution || smoke.ExitCode == nil || *smoke.ExitCode != 0 {
		t.Fatalf("smoke result = %+v, want passed trusted exit 0", smoke)
	}

	// The evidence report exists and separates trusted from claimed.
	st, err := evidence.NewStore(db).GetForTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rep evidence.Report
	if err := json.Unmarshal(st.Report, &rep); err != nil {
		t.Fatal(err)
	}
	var trusted, claimed int
	for _, v := range rep.Validation {
		if v.TrustedExecution {
			trusted++
		} else {
			claimed++
		}
	}
	if trusted != 1 || claimed != 1 {
		t.Fatalf("evidence validation entries trusted=%d claimed=%d, want 1 and 1",
			trusted, claimed)
	}
	if !strings.Contains(st.SummaryMarkdown, "## Verified by Agent Trail") ||
		!strings.Contains(st.SummaryMarkdown, "not independently verified") {
		t.Fatalf("markdown does not separate trusted from claimed:\n%s",
			st.SummaryMarkdown)
	}

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
	if err := testExecutor(db, s, ts).Execute(ctx, successor.ID, c); err != nil {
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
	if err := testExecutor(db, s, ts).Execute(ctx, successor.ID, c); err != nil {
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

	// The lost workspace is an infrastructure outcome, never a pass, and
	// evidence still exists saying so.
	st, err := evidence.NewStore(db).GetForTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(st.Report), workspaceLostNote) {
		t.Fatalf("evidence report does not flag the lost workspace:\n%s", st.Report)
	}
}

// scriptedAdapter writes the given workspace files, then claims every
// check passed no matter what those checks will measure.
type scriptedAdapter struct {
	files map[string]string
}

func (a *scriptedAdapter) Name() string { return "scripted" }

func (a *scriptedAdapter) ValidateConfiguration(ctx context.Context) error { return nil }

func (a *scriptedAdapter) Start(ctx context.Context, req agent.Request) (agent.Session, error) {
	s := &scriptedSession{events: make(chan agent.Event, 16), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer close(s.events)
		emit := func(t agent.EventType, payload map[string]any) {
			raw, _ := json.Marshal(payload)
			s.events <- agent.Event{Type: t, Timestamp: time.Now().UTC(), Payload: raw}
		}
		emit(agent.EventSessionStarted, map[string]any{"adapter": "scripted"})
		emit(agent.EventPlan, map[string]any{"plan": "1. Write files"})
		for path, content := range a.files {
			full := filepath.Join(req.WorkspaceDir, path)
			if s.err = os.MkdirAll(filepath.Dir(full), 0o755); s.err != nil {
				return
			}
			if s.err = os.WriteFile(full, []byte(content), 0o644); s.err != nil {
				return
			}
			emit(agent.EventFileWritten, map[string]any{"path": path})
		}
		// The claim trusted validation must ignore.
		emit(agent.EventToolCompleted, map[string]any{
			"command": "make", "exit_code": 0, "simulated": true,
		})
		emit(agent.EventSessionCompleted, map[string]any{"summary": "all tests passed"})
		s.result = agent.Result{Summary: "all tests passed"}
	}()
	return s, nil
}

type scriptedSession struct {
	events chan agent.Event
	done   chan struct{}
	result agent.Result
	err    error
}

func (s *scriptedSession) Events() <-chan agent.Event { return s.events }

func (s *scriptedSession) Send(ctx context.Context, message string) error {
	return errors.New("scripted session takes no input")
}

func (s *scriptedSession) Cancel(ctx context.Context) error { return nil }

func (s *scriptedSession) Wait(ctx context.Context) (agent.Result, error) {
	select {
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	case <-s.done:
	}
	return s.result, s.err
}

// TestExecuteTrustedValidationOutcomes covers the milestone-6 acceptance
// criteria: a failing check stays failed no matter what the agent claims,
// and check failures are distinct from infrastructure failures.
func TestExecuteTrustedValidationOutcomes(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	exec := testExecutor(db, s, ts)
	exec.Adapter = &scriptedAdapter{files: map[string]string{
		validation.FileName: `version: 1

validation:
  - name: ok
    category: custom
    command: ["true"]
    timeout_seconds: 30
  - name: failing-tests
    category: unit_test
    command: ["false"]
    timeout_seconds: 30
  - name: broken-infra
    category: build
    command: ["agent-trail-no-such-binary"]
    timeout_seconds: 30
`,
	}}
	if err := exec.Execute(ctx, r.ID, c); err != nil {
		t.Fatal(err)
	}

	results, err := validation.NewStore(db).ListForTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]validation.StoredResult{}
	for _, res := range results {
		byName[res.Name] = res
	}
	if len(byName) != 3 {
		t.Fatalf("validation results = %d, want 3", len(results))
	}
	if r := byName["ok"]; r.Status != validation.StatusPassed ||
		r.ExitCode == nil || *r.ExitCode != 0 || !r.TrustedExecution {
		t.Fatalf("ok = %+v, want passed trusted exit 0", r)
	}
	// "all tests passed" was claimed; the measured exit code stands.
	if r := byName["failing-tests"]; r.Status != validation.StatusFailed ||
		r.ExitCode == nil || *r.ExitCode != 1 || !r.TrustedExecution {
		t.Fatalf("failing-tests = %+v, want failed trusted exit 1", r)
	}
	// A command that never ran is an error, not a failed check.
	if r := byName["broken-infra"]; r.Status != validation.StatusError ||
		r.ExitCode != nil {
		t.Fatalf("broken-infra = %+v, want error with no exit code", r)
	}

	// The evidence report keeps the trusted failure and records the
	// agent's contradicting claim as untrusted.
	st, err := evidence.NewStore(db).GetForTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rep evidence.Report
	if err := json.Unmarshal(st.Report, &rep); err != nil {
		t.Fatal(err)
	}
	var sawTrustedFailure, sawClaim bool
	for _, v := range rep.Validation {
		if v.TrustedExecution && v.Name == "failing-tests" &&
			v.Status == string(validation.StatusFailed) {
			sawTrustedFailure = true
		}
		if !v.TrustedExecution && v.Name == "make" {
			sawClaim = true
		}
	}
	if !sawTrustedFailure || !sawClaim {
		t.Fatalf("evidence trustedFailure=%v claim=%v, want both:\n%s",
			sawTrustedFailure, sawClaim, st.Report)
	}

	// A failed check does not abort the flow: the failure is recorded and
	// the review gate (a human on the draft PR) decides.
	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("task status = %s, want completed", got.Status)
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

	err = testExecutor(db, s, ts).Execute(ctx, r.ID, c)
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
