package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
)

// testDB returns the shared integration-test database (skips without
// TEST_DATABASE_URL; make integration-test provides one).
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.Open(t)
}

func mustCreate(t *testing.T, s *Store) Task {
	t.Helper()
	tk, err := s.Create(context.Background(), CreateParams{
		Title:        "test task",
		Instructions: "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func mustTransition(t *testing.T, s *Store, id string, to Status) Task {
	t.Helper()
	tk, err := s.Transition(context.Background(), id, TransitionParams{To: to})
	if err != nil {
		t.Fatalf("transition to %s: %v", to, err)
	}
	return tk
}

func eventTypes(t *testing.T, s *Store, id string) []string {
	t.Helper()
	events, err := s.Events(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.EventType
	}
	return types
}

func TestCreateQueuesTaskWithTimeline(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)

	if tk.Status != StatusQueued || tk.Phase != PhasePending {
		t.Errorf("status/phase = %s/%s, want queued/pending", tk.Status, tk.Phase)
	}
	if tk.Version != 2 {
		t.Errorf("version = %d, want 2 (created=1, queued=2)", tk.Version)
	}
	if tk.BaseBranch != "main" {
		t.Errorf("base_branch = %q, want main default", tk.BaseBranch)
	}

	got := eventTypes(t, s, tk.ID)
	want := []string{"task.created", "task.queued"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("events = %v, want %v", got, want)
	}

	events, err := s.Events(context.Background(), tk.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.SequenceNumber != int64(i+1) {
			t.Errorf("event %d sequence = %d, want %d", i, e.SequenceNumber, i+1)
		}
		if e.AttemptNumber != 1 {
			t.Errorf("event %d attempt = %d, want 1", i, e.AttemptNumber)
		}
	}
}

func TestHappyPathEmitsOneEventPerTransitionAndBumpsVersion(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)

	path := []Status{StatusProvisioning, StatusPlanning, StatusExecuting,
		StatusValidating, StatusPublishing, StatusAwaitingReview,
		StatusCompleted}
	version := tk.Version
	for _, to := range path {
		next := mustTransition(t, s, tk.ID, to)
		if next.Version != version+1 {
			t.Errorf("version after %s = %d, want %d", to, next.Version, version+1)
		}
		version = next.Version
		if next.Status != to {
			t.Errorf("status = %s, want %s", next.Status, to)
		}
	}

	got := eventTypes(t, s, tk.ID)
	if len(got) != 2+len(path) {
		t.Fatalf("event count = %d, want %d: %v", len(got), 2+len(path), got)
	}
	for i, to := range path {
		if want := TransitionEventType(to); got[2+i] != want {
			t.Errorf("event %d = %s, want %s", 2+i, got[2+i], want)
		}
	}

	final, err := s.Get(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.StartedAt == nil {
		t.Error("started_at not set after provisioning")
	}
	if final.CompletedAt == nil {
		t.Error("completed_at not set after completion")
	}
}

func TestInvalidTransitionRejectedWithoutSideEffects(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)

	_, err := s.Transition(context.Background(), tk.ID,
		TransitionParams{To: StatusExecuting})
	var invalid *InvalidTransitionError
	if err == nil || !strings.Contains(err.Error(), "invalid transition") {
		t.Fatalf("err = %v, want invalid transition", err)
	}
	if !errors.As(err, &invalid) {
		t.Fatalf("err type = %T, want *InvalidTransitionError", err)
	}

	after, err := s.Get(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != tk.Version || after.Status != tk.Status {
		t.Errorf("rejected transition mutated the task: %+v", after)
	}
	if got := eventTypes(t, s, tk.ID); len(got) != 2 {
		t.Errorf("rejected transition emitted an event: %v", got)
	}
}

func TestDuplicateTransitionMessageIsIdempotent(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)

	p := TransitionParams{To: StatusProvisioning, IdempotencyKey: "msg-1"}
	first, err := s.Transition(context.Background(), tk.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Transition(context.Background(), tk.ID, p)
	if err != nil {
		t.Fatalf("replay errored: %v", err)
	}
	if second.Version != first.Version || second.Status != first.Status {
		t.Errorf("replay mutated the task: %+v vs %+v", second, first)
	}
	if got := eventTypes(t, s, tk.ID); len(got) != 3 {
		t.Errorf("replay emitted an event: %v", got)
	}

	// The same key stays a no-op even after further transitions.
	mustTransition(t, s, tk.ID, StatusPlanning)
	replay, err := s.Transition(context.Background(), tk.ID, p)
	if err != nil {
		t.Fatalf("late replay errored: %v", err)
	}
	if replay.Status != StatusPlanning {
		t.Errorf("late replay changed status to %s", replay.Status)
	}
}

func TestExpectedVersionConflict(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)

	_, err := s.Transition(context.Background(), tk.ID, TransitionParams{
		To: StatusProvisioning, ExpectedVersion: tk.Version + 5,
	})
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want *VersionConflictError", err)
	}

	next, err := s.Transition(context.Background(), tk.ID, TransitionParams{
		To: StatusProvisioning, ExpectedVersion: tk.Version,
	})
	if err != nil {
		t.Fatalf("matching expected version rejected: %v", err)
	}
	if next.Version != tk.Version+1 {
		t.Errorf("version = %d, want %d", next.Version, tk.Version+1)
	}
}

func TestCancelFromEveryNonTerminalStatus(t *testing.T) {
	s := NewStore(testDB(t))

	// Walk fresh tasks to each non-terminal status, then cancel.
	paths := map[Status][]Status{
		StatusQueued:            {},
		StatusProvisioning:      {StatusProvisioning},
		StatusPlanning:          {StatusProvisioning, StatusPlanning},
		StatusExecuting:         {StatusProvisioning, StatusPlanning, StatusExecuting},
		StatusValidating:        {StatusProvisioning, StatusPlanning, StatusExecuting, StatusValidating},
		StatusPublishing:        {StatusProvisioning, StatusPlanning, StatusExecuting, StatusValidating, StatusPublishing},
		StatusAwaitingReview:    {StatusProvisioning, StatusPlanning, StatusExecuting, StatusValidating, StatusPublishing, StatusAwaitingReview},
		StatusRevisionRequested: {StatusProvisioning, StatusPlanning, StatusExecuting, StatusValidating, StatusPublishing, StatusAwaitingReview, StatusRevisionRequested},
	}
	for at, path := range paths {
		tk := mustCreate(t, s)
		for _, to := range path {
			mustTransition(t, s, tk.ID, to)
		}
		cancelled, err := s.Cancel(context.Background(), tk.ID, "operator request")
		if err != nil {
			t.Errorf("cancel from %s: %v", at, err)
			continue
		}
		if cancelled.Status != StatusCancelled || cancelled.CancelRequestedAt == nil {
			t.Errorf("cancel from %s: status=%s cancel_requested_at=%v",
				at, cancelled.Status, cancelled.CancelRequestedAt)
		}
	}
}

func TestCancelIsIdempotentButRejectsOtherTerminalStates(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)

	first, err := s.Cancel(context.Background(), tk.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Cancel(context.Background(), tk.ID, "")
	if err != nil {
		t.Fatalf("double cancel errored: %v", err)
	}
	if again.Version != first.Version {
		t.Errorf("double cancel bumped version %d -> %d", first.Version, again.Version)
	}

	done := mustCreate(t, s)
	for _, to := range []Status{StatusProvisioning, StatusPlanning,
		StatusExecuting, StatusValidating, StatusPublishing,
		StatusAwaitingReview, StatusCompleted} {
		mustTransition(t, s, done.ID, to)
	}
	var invalid *InvalidTransitionError
	if _, err := s.Cancel(context.Background(), done.ID, ""); !errors.As(err, &invalid) {
		t.Errorf("cancel of completed task: err = %v, want InvalidTransitionError", err)
	}
}

func TestFailureRecordsCodeAndMessage(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)
	mustTransition(t, s, tk.ID, StatusProvisioning)

	failed, err := s.Transition(context.Background(), tk.ID, TransitionParams{
		To:             StatusFailed,
		FailureCode:    "provision_error",
		FailureMessage: "runner image pull failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.FailureCode == nil || *failed.FailureCode != "provision_error" {
		t.Errorf("failure_code = %v", failed.FailureCode)
	}
	if failed.FailureMessage == nil || *failed.FailureMessage != "runner image pull failed" {
		t.Errorf("failure_message = %v", failed.FailureMessage)
	}
	if failed.CompletedAt == nil {
		t.Error("completed_at not set on failure")
	}
}

func TestRevisionStartsSecondAttempt(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)
	for _, to := range []Status{StatusProvisioning, StatusPlanning,
		StatusExecuting, StatusValidating, StatusPublishing,
		StatusAwaitingReview, StatusRevisionRequested} {
		mustTransition(t, s, tk.ID, to)
	}
	requeued := mustTransition(t, s, tk.ID, StatusQueued)
	if requeued.Status != StatusQueued {
		t.Fatalf("status = %s", requeued.Status)
	}

	events, err := s.Events(context.Background(), tk.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "task.queued" || last.AttemptNumber != 2 || last.SequenceNumber != 1 {
		t.Errorf("last event = %s attempt %d seq %d, want task.queued attempt 2 seq 1",
			last.EventType, last.AttemptNumber, last.SequenceNumber)
	}

	db := s.db
	var active int
	err = db.QueryRow(`SELECT count(*) FROM task_attempts
		WHERE task_id = $1 AND status = 'active'`, tk.ID).Scan(&active)
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Errorf("active attempts = %d, want 1", active)
	}
	var superseded int
	err = db.QueryRow(`SELECT count(*) FROM task_attempts
		WHERE task_id = $1 AND status = 'superseded'`, tk.ID).Scan(&superseded)
	if err != nil {
		t.Fatal(err)
	}
	if superseded != 1 {
		t.Errorf("superseded attempts = %d, want 1", superseded)
	}
}

func TestTransitionEventPayloadCarriesFromAndTo(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)
	mustTransition(t, s, tk.ID, StatusProvisioning)

	events, err := s.Events(context.Background(), tk.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[len(events)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["from"] != "queued" || payload["to"] != "provisioning" {
		t.Errorf("payload = %v", payload)
	}
}

func TestActivityEventsAreAppendOnlyAtTheDatabase(t *testing.T) {
	db := testDB(t)
	s := NewStore(db)
	tk := mustCreate(t, s)

	if _, err := db.Exec(`UPDATE activity_events SET event_type = 'tampered'`); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE err = %v, want append-only rejection", err)
	}
	if _, err := db.Exec(`DELETE FROM activity_events`); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Errorf("DELETE err = %v, want append-only rejection", err)
	}
	// Task deletion is blocked too: the cascade would delete history.
	if _, err := db.Exec(`DELETE FROM tasks WHERE id = $1`, tk.ID); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Errorf("task DELETE err = %v, want append-only rejection", err)
	}
}

func TestDatabaseRejectsInvalidRowsDirectly(t *testing.T) {
	db := testDB(t)

	// Unknown status.
	_, err := db.Exec(`INSERT INTO tasks (title, instructions, status, phase)
		VALUES ('x', 'y', 'sideways', 'pending')`)
	if err == nil {
		t.Error("unknown status accepted")
	}
	// Phase inconsistent with status.
	_, err = db.Exec(`INSERT INTO tasks (title, instructions, status, phase)
		VALUES ('x', 'y', 'executing', 'pending')`)
	if err == nil {
		t.Error("inconsistent phase accepted")
	}
	// Terminal status without completed_at.
	_, err = db.Exec(`INSERT INTO tasks (title, instructions, status, phase)
		VALUES ('x', 'y', 'completed', 'terminal')`)
	if err == nil {
		t.Error("terminal status without completed_at accepted")
	}
	// Empty title.
	_, err = db.Exec(`INSERT INTO tasks (title, instructions) VALUES ('', 'y')`)
	if err == nil {
		t.Error("empty title accepted")
	}
}

func TestGetAndListAndEventsNotFound(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()

	if _, err := s.Get(ctx, "3b241101-e2bb-4255-8caf-4136c566a962"); err != ErrNotFound {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
	if _, err := s.Get(ctx, "not-a-uuid"); err != ErrNotFound {
		t.Errorf("Get malformed = %v, want ErrNotFound", err)
	}
	if _, err := s.Events(ctx, "3b241101-e2bb-4255-8caf-4136c566a962", 0); err != ErrNotFound {
		t.Errorf("Events missing = %v, want ErrNotFound", err)
	}

	a := mustCreate(t, s)
	time.Sleep(10 * time.Millisecond) // distinct created_at for ordering
	b := mustCreate(t, s)

	all, err := s.List(ctx, ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != b.ID || all[1].ID != a.ID {
		t.Errorf("list order wrong: %v", ids(all))
	}

	queued, err := s.List(ctx, ListParams{Status: StatusQueued})
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 {
		t.Errorf("queued filter = %d tasks, want 2", len(queued))
	}
	none, err := s.List(ctx, ListParams{Status: StatusFailed})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("failed filter = %d tasks, want 0", len(none))
	}
}

func ids(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestEventsAfterReturnsSuffixAcrossAttempts(t *testing.T) {
	s := NewStore(testDB(t))
	tk := mustCreate(t, s)
	for _, to := range []Status{StatusProvisioning, StatusPlanning,
		StatusExecuting, StatusValidating, StatusPublishing,
		StatusAwaitingReview, StatusRevisionRequested} {
		mustTransition(t, s, tk.ID, to)
	}
	mustTransition(t, s, tk.ID, StatusQueued) // starts attempt 2

	ctx := context.Background()
	all, err := s.Events(ctx, tk.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 3 || all[len(all)-1].AttemptNumber != 2 {
		t.Fatalf("timeline shape unexpected: %d events, last attempt %d",
			len(all), all[len(all)-1].AttemptNumber)
	}

	// A cursor at any position must yield exactly the remaining suffix.
	for i, e := range all {
		after, err := s.EventsAfter(ctx, tk.ID, e.AttemptNumber, e.SequenceNumber, 0)
		if err != nil {
			t.Fatalf("cursor %d: %v", i, err)
		}
		if len(after) != len(all)-i-1 {
			t.Fatalf("cursor %d:%d returned %d events, want %d",
				e.AttemptNumber, e.SequenceNumber, len(after), len(all)-i-1)
		}
		for j, got := range after {
			if got.ID != all[i+1+j].ID {
				t.Fatalf("cursor %d: event %d = %s, want %s",
					i, j, got.ID, all[i+1+j].ID)
			}
		}
	}

	// Zero cursor replays the whole timeline.
	fromStart, err := s.EventsAfter(ctx, tk.ID, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromStart) != len(all) {
		t.Errorf("zero cursor = %d events, want %d", len(fromStart), len(all))
	}

	if _, err := s.EventsAfter(ctx, "3b241101-e2bb-4255-8caf-4136c566a962", 0, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown task error = %v, want ErrNotFound", err)
	}
}

func TestEnsureGitContextFirstWriterWins(t *testing.T) {
	ts := NewStore(dbtest.Open(t))
	ctx := context.Background()
	tk, err := ts.Create(ctx, CreateParams{Title: "git context", Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}

	sha1 := "1111111111111111111111111111111111111111"
	base, branch, err := ts.EnsureGitContext(ctx, tk.ID, sha1, "agent-trail/first")
	if err != nil || base != sha1 || branch != "agent-trail/first" {
		t.Fatalf("EnsureGitContext = %q, %q, %v", base, branch, err)
	}

	// A recovered owner re-resolving keeps the original values.
	sha2 := "2222222222222222222222222222222222222222"
	base, branch, err = ts.EnsureGitContext(ctx, tk.ID, sha2, "agent-trail/second")
	if err != nil || base != sha1 || branch != "agent-trail/first" {
		t.Fatalf("second EnsureGitContext = %q, %q, %v", base, branch, err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommitSHA == nil || *got.BaseCommitSHA != sha1 ||
		got.WorkingBranch == nil || *got.WorkingBranch != "agent-trail/first" {
		t.Fatalf("stored context = %+v", got)
	}
	if got.Version <= tk.Version {
		t.Fatalf("version = %d, want > %d", got.Version, tk.Version)
	}
}

func TestEnsureGitContextUnknownTask(t *testing.T) {
	ts := NewStore(dbtest.Open(t))
	_, _, err := ts.EnsureGitContext(context.Background(),
		"00000000-0000-0000-0000-000000000000",
		"1111111111111111111111111111111111111111", "agent-trail/x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
