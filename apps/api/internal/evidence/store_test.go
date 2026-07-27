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

// TestStoreInsertReplayKeepsFirst: one report per attempt; a recovered
// owner regenerating it is a no-op.
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
