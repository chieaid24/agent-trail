package conflict

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func createRepository(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	var orgID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO organizations (name, slug, github_account_id,
			github_account_login, github_account_type)
		VALUES ('Test Org', 'test-org', 1, 'test-org', 'Organization')
		RETURNING id`).Scan(&orgID)
	if err != nil {
		t.Fatal(err)
	}
	var repoID string
	err = db.QueryRowContext(ctx, `
		INSERT INTO repositories (organization_id, github_repository_id,
			owner, name, full_name, clone_url)
		VALUES ($1, 1, 'test-org', 'demo', 'test-org/demo',
			'https://example.com/test-org/demo.git')
		RETURNING id`, orgID).Scan(&repoID)
	if err != nil {
		t.Fatal(err)
	}
	return repoID
}

func createRepoTask(t *testing.T, db *sql.DB, repoID, title, finalSHA string) task.Task {
	t.Helper()
	ctx := context.Background()
	tk, err := task.NewStore(db).Create(ctx, task.CreateParams{
		Title: title, Instructions: "x", RepositoryID: &repoID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finalSHA != "" {
		_, err = db.ExecContext(ctx, `
			UPDATE task_attempts
			SET base_commit_sha = $2, final_commit_sha = $3
			WHERE task_id = $1`, tk.ID, strings.Repeat("0", 40), finalSHA)
		if err != nil {
			t.Fatal(err)
		}
	}
	return tk
}

func completeTask(t *testing.T, db *sql.DB, taskID string) {
	t.Helper()
	ctx := context.Background()
	store := task.NewStore(db)
	for _, to := range []task.Status{
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting,
		task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview, task.StatusCompleted,
	} {
		if _, err := store.Transition(ctx, taskID, task.TransitionParams{To: to}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
}

func TestActiveSiblings(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	repoID := createRepository(t, db)

	self := createRepoTask(t, db, repoID, "self", strings.Repeat("a", 40))
	published := createRepoTask(t, db, repoID, "published sibling", strings.Repeat("b", 40))
	createRepoTask(t, db, repoID, "unpublished sibling", "")
	finished := createRepoTask(t, db, repoID, "finished sibling", strings.Repeat("c", 40))
	completeTask(t, db, finished.ID)

	siblings, err := s.ActiveSiblings(ctx, repoID, self.ID)
	if err != nil {
		t.Fatalf("ActiveSiblings: %v", err)
	}
	want := []Sibling{{
		TaskID: published.ID, Title: "published sibling",
		BaseSHA: strings.Repeat("0", 40), FinalSHA: strings.Repeat("b", 40),
	}}
	if !reflect.DeepEqual(siblings, want) {
		t.Errorf("siblings = %+v, want %+v", siblings, want)
	}
}

func TestUpsertListAndDelete(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	repoID := createRepository(t, db)

	a := createRepoTask(t, db, repoID, "task a", "")
	b := createRepoTask(t, db, repoID, "task b", "")

	kinds := []Kind{KindFileOverlap, KindMergeConflict}
	files := []string{"app.go"}
	if err := s.Reconcile(ctx, repoID, a.ID, []Detection{{
		OtherTaskID: b.ID, Kinds: kinds, Files: files,
	}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	fromA, err := s.ListForTask(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListForTask(a): %v", err)
	}
	if len(fromA) != 1 || fromA[0].OtherTaskID != b.ID ||
		fromA[0].OtherTaskTitle != "task b" ||
		!reflect.DeepEqual(fromA[0].Kinds, kinds) ||
		!reflect.DeepEqual(fromA[0].Files, files) {
		t.Errorf("fromA = %+v", fromA)
	}
	fromB, err := s.ListForTask(ctx, b.ID)
	if err != nil {
		t.Fatalf("ListForTask(b): %v", err)
	}
	if len(fromB) != 1 || fromB[0].OtherTaskID != a.ID {
		t.Errorf("fromB = %+v", fromB)
	}

	if err := s.Reconcile(ctx, repoID, b.ID, []Detection{{
		OtherTaskID: a.ID, Kinds: []Kind{KindFileOverlap}, Files: []string{"lib.go"},
	}}); err != nil {
		t.Fatalf("Reconcile(update): %v", err)
	}
	fromA, err = s.ListForTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromA) != 1 || !reflect.DeepEqual(fromA[0].Kinds, []Kind{KindFileOverlap}) ||
		!reflect.DeepEqual(fromA[0].Files, []string{"lib.go"}) {
		t.Errorf("after update fromA = %+v", fromA)
	}

	if err := s.Reconcile(ctx, repoID, a.ID, nil); err != nil {
		t.Fatalf("Reconcile(clean): %v", err)
	}
	fromA, err = s.ListForTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromA) != 0 {
		t.Errorf("after delete fromA = %+v, want empty", fromA)
	}
}

func TestListForTaskHidesTerminalPairs(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	repoID := createRepository(t, db)

	a := createRepoTask(t, db, repoID, "task a", "")
	b := createRepoTask(t, db, repoID, "task b", "")
	if err := s.Reconcile(ctx, repoID, a.ID, []Detection{{
		OtherTaskID: b.ID, Kinds: []Kind{KindFileOverlap},
	}}); err != nil {
		t.Fatal(err)
	}

	completeTask(t, db, b.ID)
	fromA, err := s.ListForTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromA) != 0 {
		t.Errorf("terminal pair still listed: %+v", fromA)
	}
}

func TestReconcileRollsBackTheWholeTaskSet(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	s := NewStore(db)
	repoID := createRepository(t, db)
	a := createRepoTask(t, db, repoID, "task a", "")
	b := createRepoTask(t, db, repoID, "task b", "")
	c := createRepoTask(t, db, repoID, "task c", "")
	if err := s.Reconcile(ctx, repoID, a.ID, []Detection{{
		OtherTaskID: b.ID, Kinds: []Kind{KindFileOverlap}, Files: []string{"old.go"},
	}}); err != nil {
		t.Fatal(err)
	}

	err := s.Reconcile(ctx, repoID, a.ID, []Detection{
		{OtherTaskID: b.ID, Kinds: []Kind{KindFileOverlap}, Files: []string{"new.go"}},
		{OtherTaskID: c.ID, Kinds: []Kind{"unknown"}},
	})
	if err == nil {
		t.Fatal("invalid replacement must fail")
	}
	conflicts, err := s.ListForTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].OtherTaskID != b.ID ||
		!reflect.DeepEqual(conflicts[0].Files, []string{"old.go"}) {
		t.Fatalf("conflicts after rollback = %+v", conflicts)
	}
}

func TestReconcileRejectsEmptyKinds(t *testing.T) {
	s := NewStore(nil)
	if err := s.Reconcile(context.Background(), "r", "a", []Detection{{
		OtherTaskID: "b",
	}}); err == nil {
		t.Error("empty kinds must be rejected")
	}
}

func TestListForTaskRejectsUnknownTask(t *testing.T) {
	db := dbtest.Open(t)
	_, err := NewStore(db).ListForTask(context.Background(),
		"3b241101-e2bb-4255-8caf-4136c566a962")
	if !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("err = %v, want task.ErrNotFound", err)
	}
}

func TestConflictConstraints(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	repoA := createRepository(t, db)
	a := createRepoTask(t, db, repoA, "task a", "")

	var repoB string
	err := db.QueryRowContext(ctx, `
		INSERT INTO repositories (organization_id, github_repository_id,
			owner, name, full_name, clone_url)
		SELECT organization_id, 2, owner, 'other', owner || '/other',
			'https://example.com/test-org/other.git'
		FROM repositories WHERE id = $1
		RETURNING id`, repoA).Scan(&repoB)
	if err != nil {
		t.Fatal(err)
	}
	b := createRepoTask(t, db, repoB, "task b", "")
	if err := NewStore(db).Reconcile(ctx, repoA, a.ID, []Detection{{
		OtherTaskID: b.ID, Kinds: []Kind{KindFileOverlap},
	}}); err == nil {
		t.Fatal("cross-repository pair must fail")
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO task_conflicts
			(repository_id, task_a_id, task_b_id, kinds, files)
		VALUES ($1, LEAST($2::uuid, $3::uuid), GREATEST($2::uuid, $3::uuid),
			'["unknown"]', '[]')`, repoA, a.ID,
		createRepoTask(t, db, repoA, "task c", "").ID)
	if err == nil {
		t.Fatal("unknown conflict kind must fail")
	}

	c := createRepoTask(t, db, repoA, "task c", "")
	if err := NewStore(db).Reconcile(ctx, repoA, a.ID, []Detection{{
		OtherTaskID: c.ID, Kinds: []Kind{KindFileOverlap},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET repository_id = $1 WHERE id = $2`,
		repoB, c.ID); err == nil {
		t.Fatal("moving a task must not invalidate an existing conflict pair")
	}
}
