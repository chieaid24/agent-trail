package validation

import (
	"context"
	"database/sql"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func createAttempt(t *testing.T, db *sql.DB) (taskID, attemptID string) {
	t.Helper()
	tk, err := task.NewStore(db).Create(context.Background(), task.CreateParams{
		Title: "validation store test", Instructions: "x",
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

func intPtr(v int) *int { return &v }

func TestStoreInsertAndList(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	taskID, attemptID := createAttempt(t, db)

	results := []Result{
		{Name: "unit", Category: "unit_test", Command: []string{"go", "test"},
			Status: StatusFailed, ExitCode: intPtr(2), DurationMS: 120,
			Summary: "3 tests failed", TrustedExecution: true},
		{Name: "build", Category: "build", Command: []string{"go", "build"},
			Status: StatusPassed, ExitCode: intPtr(0), DurationMS: 80,
			TrustedExecution: true},
	}
	for _, r := range results {
		if err := s.Insert(ctx, attemptID, r); err != nil {
			t.Fatal(err)
		}
	}

	byTask, err := s.ListForTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byTask) != 2 {
		t.Fatalf("ListForTask = %d results, want 2", len(byTask))
	}
	got := byTask[0]
	if got.Name != "unit" || got.Status != StatusFailed ||
		got.ExitCode == nil || *got.ExitCode != 2 ||
		!got.TrustedExecution || got.AttemptNumber != 1 ||
		string(got.Command) != `["go", "test"]` {
		t.Fatalf("stored = %+v", got)
	}

	byAttempt, err := s.ListForAttempt(ctx, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byAttempt) != 2 || byAttempt[1].Name != "build" {
		t.Fatalf("ListForAttempt = %+v", byAttempt)
	}
}

// TestStoreInsertReplayIsNoOp: a zombie owner replaying a check cannot
// overwrite the recorded outcome.
func TestStoreInsertReplayIsNoOp(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	taskID, attemptID := createAttempt(t, db)

	first := Result{Name: "unit", Category: "unit_test",
		Command: []string{"go", "test"}, Status: StatusFailed,
		ExitCode: intPtr(1), DurationMS: 10, TrustedExecution: true}
	replay := first
	replay.Status = StatusPassed
	replay.ExitCode = intPtr(0)
	if err := s.Insert(ctx, attemptID, first); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, attemptID, replay); err != nil {
		t.Fatal(err)
	}

	stored, err := s.ListForTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Status != StatusFailed {
		t.Fatalf("stored = %+v, want the first failed result only", stored)
	}
}

func TestStoreListUnknownTask(t *testing.T) {
	db := dbtest.Open(t)
	s := NewStore(db)
	if _, err := s.ListForTask(context.Background(),
		"00000000-0000-0000-0000-000000000000"); err != task.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.ListForTask(context.Background(), "not-a-uuid"); err != task.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
