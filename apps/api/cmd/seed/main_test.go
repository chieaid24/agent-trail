package main

import (
	"os"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
)

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want DATABASE_URL error", err)
	}
}

// TestRunSeedsOnceAgainstRealDatabase exercises seeding and its idempotence
// when a test database is available (make integration-test); otherwise skips.
func TestRunSeedsOnceAgainstRealDatabase(t *testing.T) {
	db := dbtest.Open(t) // skips without TEST_DATABASE_URL
	t.Setenv("DATABASE_URL", os.Getenv("TEST_DATABASE_URL"))

	// The demo set plus the two-task conflict pair.
	want := len(demoTasks()) + 2

	if err := run(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("tasks = %d, want %d", count, want)
	}

	// The conflict pair rests at awaiting_review (unclaimable, non-terminal)
	// with one stored warning between its members.
	var pairCount int
	if err := db.QueryRow(`
		SELECT count(*) FROM tasks
		WHERE title IN ($1, $2) AND status = 'awaiting_review'`,
		conflictTaskATitle, conflictTaskBTitle).Scan(&pairCount); err != nil {
		t.Fatal(err)
	}
	if pairCount != 2 {
		t.Fatalf("awaiting_review pair tasks = %d, want 2", pairCount)
	}
	var conflictCount int
	if err := db.QueryRow(`SELECT count(*) FROM task_conflicts`).Scan(&conflictCount); err != nil {
		t.Fatal(err)
	}
	if conflictCount != 1 {
		t.Fatalf("task_conflicts = %d, want 1", conflictCount)
	}
	if err := db.QueryRow(`SELECT count(*) FROM repositories`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("repositories = %d, want 2", count)
	}

	// Second run must not duplicate.
	if err := run(); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("tasks after second run = %d, want %d", count, want)
	}
}
