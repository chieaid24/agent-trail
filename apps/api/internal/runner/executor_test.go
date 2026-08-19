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
	"sync"
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

type hangingAdapter struct {
	started   chan string
	cancelled chan struct{}
	finished  chan struct{}
}

func newHangingAdapter() *hangingAdapter {
	return &hangingAdapter{
		started:   make(chan string, 1),
		cancelled: make(chan struct{}),
		finished:  make(chan struct{}),
	}
}

func (a *hangingAdapter) Name() string { return "hanging" }

func (a *hangingAdapter) ValidateConfiguration(context.Context) error { return nil }

func (a *hangingAdapter) Start(ctx context.Context, req agent.Request) (agent.Session, error) {
	a.started <- req.WorkspaceDir
	s := &hangingSession{
		events:    make(chan agent.Event),
		done:      a.finished,
		stop:      make(chan struct{}),
		cancelled: a.cancelled,
	}
	go s.run(ctx)
	return s, nil
}

type hangingSession struct {
	events    chan agent.Event
	done      chan struct{}
	stop      chan struct{}
	cancelled chan struct{}
	stopOnce  sync.Once
}

type resistantAdapter struct {
	started   chan string
	forceStop chan struct{}
	finished  chan struct{}
}

func newResistantAdapter() *resistantAdapter {
	return &resistantAdapter{
		started:   make(chan string, 1),
		forceStop: make(chan struct{}),
		finished:  make(chan struct{}),
	}
}

func (a *resistantAdapter) Name() string { return "resistant" }

func (a *resistantAdapter) ValidateConfiguration(context.Context) error { return nil }

func (a *resistantAdapter) Start(_ context.Context, req agent.Request) (agent.Session, error) {
	a.started <- req.WorkspaceDir
	s := &resistantSession{
		events:    make(chan agent.Event),
		forceStop: a.forceStop,
		finished:  a.finished,
	}
	go s.run()
	return s, nil
}

type resistantSession struct {
	events    chan agent.Event
	forceStop chan struct{}
	finished  chan struct{}
}

var errResistantCancel = errors.New("resistant cancel blocked until forced stop")

func (s *resistantSession) Events() <-chan agent.Event { return s.events }

func (s *resistantSession) Send(context.Context, string) error {
	return errors.New("resistant session takes no input")
}

func (s *resistantSession) Cancel(context.Context) error {
	<-s.forceStop
	return errResistantCancel
}

func (s *resistantSession) Wait(ctx context.Context) (agent.Result, error) {
	select {
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	case <-s.finished:
		return agent.Result{}, errors.New("resistant session force-stopped")
	}
}

func (s *resistantSession) run() {
	defer close(s.finished)
	defer close(s.events)
	s.events <- agent.Event{Type: agent.EventSessionStarted}
	<-s.forceStop
}

func (s *hangingSession) Events() <-chan agent.Event { return s.events }

func (s *hangingSession) Send(context.Context, string) error {
	return errors.New("hanging session takes no input")
}

func (s *hangingSession) Cancel(context.Context) error {
	s.stopOnce.Do(func() {
		close(s.cancelled)
		close(s.stop)
	})
	return nil
}

func (s *hangingSession) Wait(ctx context.Context) (agent.Result, error) {
	select {
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	case <-s.done:
		return agent.Result{}, errors.New("hanging session interrupted")
	}
}

func (s *hangingSession) run(ctx context.Context) {
	defer close(s.done)
	defer close(s.events)
	select {
	case s.events <- agent.Event{Type: agent.EventSessionStarted}:
	case <-ctx.Done():
		return
	case <-s.stop:
		return
	}
	select {
	case <-ctx.Done():
	case <-s.stop:
	}
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

func TestExecuteTimesOutHangingSession(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	maxRuntime := 1
	tk, err := ts.Create(ctx, task.CreateParams{
		Title:             "timeout task",
		Instructions:      "hang",
		MaxRuntimeSeconds: &maxRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	adapter := newHangingAdapter()
	exec := testExecutor(db, s, ts)
	exec.Adapter = adapter
	exec.DefaultRuntime = time.Hour

	started := time.Now()
	err = exec.Execute(ctx, r.ID, c)
	if !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute = %v, want ErrAttemptFailed", err)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("timeout elapsed = %s, want about 1s", elapsed)
	}
	select {
	case <-adapter.cancelled:
	default:
		t.Fatal("session was not cancelled")
	}
	assertSessionStopped(t, adapter)

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusTimedOut || got.FailureCode == nil ||
		*got.FailureCode != "task_runtime_exceeded" || got.FailureMessage == nil {
		t.Fatalf("timed-out task = %+v", got)
	}
	assertAttemptSettled(t, db, c.AttemptID, "timed_out")
	assertWorkspaceRemoved(t, <-adapter.started)
}

func TestExecuteUsesDefaultRuntimeForHangingSession(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)
	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	adapter := newHangingAdapter()
	exec := testExecutor(db, s, ts)
	exec.Adapter = adapter
	exec.DefaultRuntime = time.Second

	if err := exec.Execute(ctx, r.ID, c); !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute = %v, want ErrAttemptFailed", err)
	}
	assertSessionStopped(t, adapter)
	got, err := ts.Get(ctx, tk.ID)
	if err != nil || got.Status != task.StatusTimedOut {
		t.Fatalf("task = %+v, %v; want timed_out", got, err)
	}
	assertAttemptSettled(t, db, c.AttemptID, "timed_out")
	assertWorkspaceRemoved(t, <-adapter.started)
}

func TestExecuteHoldsLeaseUntilSessionEventuallyStops(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)
	c, err := s.Claim(ctx, r.ID, 2*time.Second)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	adapter := newResistantAdapter()
	exec := testExecutor(db, s, ts)
	exec.Adapter = adapter
	exec.DefaultRuntime = time.Second
	exec.SessionStopTimeout = 100 * time.Millisecond
	exec.LeaseDuration = 2 * time.Second
	done := make(chan error, 1)
	go func() { done <- exec.Execute(ctx, r.ID, c) }()
	workspace := <-adapter.started

	select {
	case err := <-done:
		t.Fatalf("Execute returned while provider was running: %v", err)
	case <-time.After(3 * time.Second):
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("workspace was removed before provider termination: %v", err)
	}
	var leaseOwner *string
	var leaseExpiresAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT lease_owner, lease_expires_at FROM task_attempts WHERE id = $1`, c.AttemptID).
		Scan(&leaseOwner, &leaseExpiresAt); err != nil {
		t.Fatal(err)
	}
	if leaseOwner == nil || *leaseOwner != r.ID {
		t.Fatalf("lease_owner = %v, want %s", leaseOwner, r.ID)
	}
	if !leaseExpiresAt.After(time.Now()) {
		t.Fatalf("lease expired while provider was running: %s", leaseExpiresAt)
	}

	close(adapter.forceStop)
	select {
	case err := <-done:
		if !errors.Is(err, ErrSessionStopFailed) || !errors.Is(err, ErrAttemptFailed) ||
			!errors.Is(err, errResistantCancel) {
			t.Fatalf("Execute = %v, want shutdown, attempt, and cancel errors", err)
		}
	case <-time.After(time.Second):
		t.Fatal("executor did not finish after provider termination")
	}
	got, err := ts.Get(ctx, tk.ID)
	if err != nil || got.Status != task.StatusTimedOut {
		t.Fatalf("task = %+v, %v; want timed_out", got, err)
	}
	assertAttemptSettled(t, db, c.AttemptID, "timed_out")
	assertWorkspaceRemoved(t, workspace)
}

func TestExecuteRecoveryKeepsOriginalRuntimeDeadline(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dead := mustRegister(t, s)
	successor := mustRegister(t, s)
	maxRuntime := 1
	tk, err := ts.Create(ctx, task.CreateParams{
		Title:             "recovered timeout task",
		Instructions:      "hang",
		MaxRuntimeSeconds: &maxRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Claim(ctx, dead.ID, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	for _, to := range []task.Status{
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting,
	} {
		if _, err := ts.Transition(ctx, tk.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE task_attempts
		SET started_at = now() - interval '2 seconds',
			lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, first.AttemptID); err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(ctx, successor.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("recovery claim = %+v, %v", c, err)
	}
	adapter := newHangingAdapter()
	exec := testExecutor(db, s, ts)
	exec.Adapter = adapter

	started := time.Now()
	if err := exec.Execute(ctx, successor.ID, c); !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute = %v, want ErrAttemptFailed", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("recovered expired deadline took %s", elapsed)
	}
	got, err := ts.Get(ctx, tk.ID)
	if err != nil || got.Status != task.StatusTimedOut {
		t.Fatalf("task = %+v, %v; want timed_out", got, err)
	}
	assertAttemptSettled(t, db, c.AttemptID, "timed_out")
	select {
	case workspace := <-adapter.started:
		t.Fatalf("expired recovery started adapter in %q", workspace)
	default:
	}
	leftovers, err := filepath.Glob(filepath.Join(tmp, "agent-trail-attempt-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("recovery workspaces = %v, %v; want none", leftovers, err)
	}
}

func TestExecuteTimesOutRecoveredRepositorylessReview(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	dead := mustRegister(t, s)
	successor := mustRegister(t, s)
	maxRuntime := 1
	tk, err := ts.Create(ctx, task.CreateParams{
		Title:             "recovered review timeout task",
		Instructions:      "already executed",
		MaxRuntimeSeconds: &maxRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Claim(ctx, dead.ID, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	for _, to := range []task.Status{
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting,
		task.StatusValidating, task.StatusPublishing, task.StatusAwaitingReview,
	} {
		if _, err := ts.Transition(ctx, tk.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE task_attempts
		SET started_at = now() - interval '2 seconds',
			lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, first.AttemptID); err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(ctx, successor.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("recovery claim = %+v, %v", c, err)
	}

	if err := testExecutor(db, s, ts).Execute(ctx, successor.ID, c); !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute = %v, want ErrAttemptFailed", err)
	}
	got, err := ts.Get(ctx, tk.ID)
	if err != nil || got.Status != task.StatusTimedOut {
		t.Fatalf("task = %+v, %v; want timed_out", got, err)
	}
	assertAttemptSettled(t, db, c.AttemptID, "timed_out")
}

func TestExecuteCancellationInterruptsHangingSession(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)
	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	adapter := newHangingAdapter()
	exec := testExecutor(db, s, ts)
	exec.Adapter = adapter
	exec.DefaultRuntime = time.Minute
	done := make(chan error, 1)
	go func() { done <- exec.Execute(ctx, r.ID, c) }()
	workspace := <-adapter.started

	cancelledAt := time.Now()
	if _, err := ts.Cancel(ctx, tk.ID, "operator cancelled"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not stop promptly")
	}
	if elapsed := time.Since(cancelledAt); elapsed > time.Second {
		t.Fatalf("cancellation took %s, want under 1s", elapsed)
	}
	select {
	case <-adapter.cancelled:
	default:
		t.Fatal("session was not cancelled")
	}
	assertSessionStopped(t, adapter)
	got, err := ts.Get(ctx, tk.ID)
	if err != nil || got.Status != task.StatusCancelled {
		t.Fatalf("task = %+v, %v; want cancelled", got, err)
	}
	assertAttemptSettled(t, db, c.AttemptID, "cancelled")
	assertWorkspaceRemoved(t, workspace)
}

func assertAttemptSettled(t *testing.T, db *sql.DB, attemptID, wantStatus string) {
	t.Helper()
	var status string
	var leaseOwner *string
	if err := db.QueryRowContext(context.Background(), `
		SELECT status, lease_owner FROM task_attempts WHERE id = $1`, attemptID).
		Scan(&status, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || leaseOwner != nil {
		t.Fatalf("attempt status/lease = %s/%v, want %s/nil", status, leaseOwner, wantStatus)
	}
}

func assertWorkspaceRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace %q still exists: %v", path, err)
	}
}

func assertSessionStopped(t *testing.T, adapter *hangingAdapter) {
	t.Helper()
	select {
	case <-adapter.finished:
	case <-time.After(time.Second):
		t.Fatal("session did not finish")
	}
}
