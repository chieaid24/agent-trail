package evidence

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func createAttempt(t *testing.T, db *sql.DB) (taskID, attemptID string) {
	t.Helper()
	tk, err := task.NewStore(db).Create(context.Background(), task.CreateParams{
		Title: "evidence store test", Instructions: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRowContext(context.Background(),
		`SELECT id FROM task_attempts WHERE task_id = $1`, tk.ID).Scan(&attemptID)
	if err != nil {
		t.Fatal(err)
	}
	return tk.ID, attemptID
}

func TestStoreInsertAndGet(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	taskID, attemptID := createAttempt(t, db)

	if _, err := s.GetForTask(ctx, taskID); !errors.Is(err, ErrNoReport) {
		t.Fatalf("err = %v, want ErrNoReport", err)
	}

	report := Generate(Params{Task: task.Task{ID: taskID, Title: "t"}})
	if err := s.Insert(ctx, attemptID, report, Markdown(report)); err != nil {
		t.Fatal(err)
	}

	st, err := s.GetForTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if st.TaskAttemptID != attemptID || st.SchemaVersion != SchemaVersion ||
		st.AttemptNumber != 1 {
		t.Fatalf("stored = %+v", st)
	}
	if !strings.Contains(st.SummaryMarkdown, "# Evidence") {
		t.Fatalf("markdown = %q", st.SummaryMarkdown)
	}
	if !strings.Contains(string(st.Report), `"schema_version": 1`) {
		t.Fatalf("report json = %s", st.Report)
	}
}

func TestStoreInsertReplayKeepsFirst(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	taskID, attemptID := createAttempt(t, db)

	first := Generate(Params{Task: task.Task{ID: taskID, Title: "first"}})
	second := Generate(Params{Task: task.Task{ID: taskID, Title: "second"}})
	if err := s.Insert(ctx, attemptID, first, Markdown(first)); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, attemptID, second, Markdown(second)); err != nil {
		t.Fatal(err)
	}

	st, err := s.GetForTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(st.Report), `"title": "first"`) {
		t.Fatalf("report = %s, want the first insert kept", st.Report)
	}
}

func TestStoreGetUnknownTask(t *testing.T) {
	db := dbtest.Open(t)
	s := NewStore(db)
	if _, err := s.GetForTask(context.Background(),
		"00000000-0000-0000-0000-000000000000"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("err = %v, want task.ErrNotFound", err)
	}
	if _, err := s.GetForTask(context.Background(), "nope"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("err = %v, want task.ErrNotFound", err)
	}
}

func TestGetForAttemptReadsOnlyThatAttempt(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	taskID, attemptID := createAttempt(t, db)

	if _, err := s.GetForAttempt(ctx, attemptID); !errors.Is(err, ErrNoReport) {
		t.Fatalf("err = %v, want ErrNoReport", err)
	}
	first := Generate(Params{Task: task.Task{ID: taskID, Title: "first"}})
	if err := s.Insert(ctx, attemptID, first, Markdown(first)); err != nil {
		t.Fatal(err)
	}
	secondID := superseded(t, db, taskID)
	second := Generate(Params{Task: task.Task{ID: taskID, Title: "second"}})
	if err := s.Insert(ctx, secondID, second, Markdown(second)); err != nil {
		t.Fatal(err)
	}

	st, err := s.GetForAttempt(ctx, attemptID)
	if err != nil || st.AttemptNumber != 1 || !strings.Contains(string(st.Report), `"title": "first"`) {
		t.Fatalf("attempt 1 report = %+v, err = %v", st, err)
	}
	latest, err := s.GetForTask(ctx, taskID)
	if err != nil || latest.AttemptNumber != 2 {
		t.Fatalf("latest report = %+v, err = %v", latest, err)
	}
	if _, err := s.GetForAttempt(ctx, "nope"); !errors.Is(err, task.ErrAttemptNotFound) {
		t.Fatalf("bad id err = %v", err)
	}
}

// walks the task to review and requests a revision; returns the new active attempt id
func superseded(t *testing.T, db *sql.DB, taskID string) string {
	t.Helper()
	ctx := context.Background()
	ts := task.NewStore(db)
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview} {
		if _, err := ts.Transition(ctx, taskID, task.TransitionParams{To: to}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	_, _, err := ts.RequestRevision(ctx, taskID, task.RevisionParams{
		Instructions: "revise", BaseCommitSHA: strings.Repeat("b", 40),
		RequestedByLogin: "alice", TriggerCommentID: 1, MaxAttempts: 5,
		IdempotencyKey: "revise:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = db.QueryRowContext(ctx,
		`SELECT id FROM task_attempts WHERE task_id = $1 AND status = 'active'`, taskID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAttemptHistoryPerAttempt(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	ts := task.NewStore(db)
	taskID, firstID := createAttempt(t, db)

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE task_attempts SET base_commit_sha = $2, final_commit_sha = $3 WHERE id = $1`,
		firstID, strings.Repeat("a", 40), strings.Repeat("b", 40))
	exec(`INSERT INTO validation_results
		(task_attempt_id, name, category, command_json, status, duration_ms, trusted_execution)
		VALUES ($1, 'unit', 'unit_test', '["go","test"]', 'passed', 10, true),
			($1, 'lint', 'lint', '["lint"]', 'failed', 5, true),
			($1, 'claimed', 'custom', '["x"]', 'passed', 1, false)`, firstID)
	for _, payload := range []map[string]any{{"cost_usd": 0.25}, {"total_cost_usd": 0.4}} {
		if err := ts.AppendAttemptEvent(ctx, firstID, "agent.cost_update", "agent", payload); err != nil {
			t.Fatal(err)
		}
	}
	secondID := superseded(t, db, taskID)
	exec(`INSERT INTO validation_results
		(task_attempt_id, name, category, command_json, status, duration_ms, trusted_execution)
		VALUES ($1, 'unit', 'unit_test', '["go","test"]', 'passed', 10, true),
			($1, 'slow', 'custom', '["x"]', 'timed_out', 1, true)`, secondID)

	history, err := s.AttemptHistory(ctx, taskID)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %+v, err = %v", history, err)
	}
	first, second := history[0], history[1]
	if first.Number != 1 || first.Status != "superseded" ||
		first.BaseCommit != strings.Repeat("a", 40) || first.FinalCommit != strings.Repeat("b", 40) ||
		first.Validation != "failed" || first.CostUSD == nil || *first.CostUSD != 0.4 {
		t.Fatalf("attempt 1 history = %+v", first)
	}
	if second.Number != 2 || second.Status != "active" || second.BaseCommit != strings.Repeat("b", 40) ||
		second.FinalCommit != "" || second.Validation != "error" || second.CostUSD != nil {
		t.Fatalf("attempt 2 history = %+v", second)
	}

	bare, _ := createAttempt(t, db)
	history, err = s.AttemptHistory(ctx, bare)
	if err != nil || len(history) != 1 || history[0].Validation != "" || history[0].CostUSD != nil {
		t.Fatalf("bare history = %+v, err = %v", history, err)
	}
	if _, err := s.AttemptHistory(ctx, "nope"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("bad id err = %v", err)
	}
}

func TestStoreGetForTaskAttempt(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	tasks := task.NewStore(db)
	tk, err := tasks.Create(ctx, task.CreateParams{Title: "attempt evidence", Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview} {
		if _, err := tasks.Transition(ctx, tk.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatal(err)
		}
	}
	_, second, err := tasks.RequestRevision(ctx, tk.ID, task.RevisionParams{
		Instructions: "revise", BaseCommitSHA: strings.Repeat("c", 40),
		RequestedByLogin: "alice", TriggerCommentID: 7, MaxAttempts: 5,
		IdempotencyKey: "revise:7",
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := tasks.Attempts(ctx, tk.ID)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("attempts = %d, err = %v", len(attempts), err)
	}
	first := attempts[0]

	if _, err := s.GetForTaskAttempt(ctx, tk.ID, 1); !errors.Is(err, ErrNoReport) {
		t.Fatalf("attempt 1 before insert: err = %v, want ErrNoReport", err)
	}
	if _, err := s.GetForTaskAttempt(ctx, tk.ID, 3); !errors.Is(err, task.ErrAttemptNotFound) {
		t.Fatalf("attempt 3: err = %v, want ErrAttemptNotFound", err)
	}
	if _, err := s.GetForTaskAttempt(ctx, "3b241101-e2bb-4255-8caf-4136c566a999", 1); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("unknown task: err = %v, want ErrNotFound", err)
	}

	one := Generate(Params{Task: task.Task{ID: tk.ID, Title: "one"}})
	two := Generate(Params{Task: task.Task{ID: tk.ID, Title: "two"}})
	if err := s.Insert(ctx, first.ID, one, "attempt one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, second.ID, two, "attempt two"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetForTaskAttempt(ctx, tk.ID, 1)
	if err != nil || got.TaskAttemptID != first.ID || got.AttemptNumber != 1 ||
		got.SummaryMarkdown != "attempt one" {
		t.Fatalf("attempt 1 = %+v, err = %v", got, err)
	}
	got, err = s.GetForTaskAttempt(ctx, tk.ID, 2)
	if err != nil || got.TaskAttemptID != second.ID || got.AttemptNumber != 2 {
		t.Fatalf("attempt 2 = %+v, err = %v", got, err)
	}
	latest, err := s.GetForTask(ctx, tk.ID)
	if err != nil || latest.AttemptNumber != 2 {
		t.Fatalf("latest = %+v, err = %v", latest, err)
	}
}
