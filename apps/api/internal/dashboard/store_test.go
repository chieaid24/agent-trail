package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func seedRepository(t *testing.T, db *sql.DB) (string, string) {
	t.Helper()
	ctx := context.Background()
	var organizationID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO organizations
			(name, slug, github_account_id, github_account_login,
			 github_account_type)
		VALUES ('Agent Trail', 'agent-trail', 101, 'agent-trail', 'Organization')
		RETURNING id`).Scan(&organizationID)
	if err != nil {
		t.Fatal(err)
	}
	var repositoryID string
	err = db.QueryRowContext(ctx, `
		INSERT INTO repositories
			(organization_id, github_repository_id, owner, name, full_name,
			 default_branch, clone_url, settings_json)
		VALUES ($1, 201, 'agent-trail', 'control-plane',
			'agent-trail/control-plane', 'trunk',
			'https://github.com/agent-trail/control-plane.git',
			'{"default_policy":"restricted","validation_file":".ci/checks.yaml"}')
		RETURNING id`, organizationID).Scan(&repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	return organizationID, repositoryID
}

func createRepositoryTask(t *testing.T, db *sql.DB, organizationID, repositoryID, title string) task.Task {
	t.Helper()
	result, err := task.NewStore(db).Create(context.Background(), task.CreateParams{
		Title: title, Instructions: "test", OrganizationID: &organizationID,
		RepositoryID: &repositoryID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func transition(t *testing.T, store *task.Store, taskID string, statuses ...task.Status) {
	t.Helper()
	ctx := context.Background()
	for _, status := range statuses {
		if _, err := store.Transition(ctx, taskID, task.TransitionParams{To: status}); err != nil {
			t.Fatalf("transition to %s: %v", status, err)
		}
	}
}

func TestOrganizationsAndRepositories(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	organizationID, repositoryID := seedRepository(t, db)
	tasks := task.NewStore(db)

	active := createRepositoryTask(t, db, organizationID, repositoryID, "Active task")
	finished := createRepositoryTask(t, db, organizationID, repositoryID, "Finished task")
	transition(t, tasks, finished.ID,
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting,
		task.StatusValidating, task.StatusPublishing, task.StatusAwaitingReview,
		task.StatusCompleted)

	store := NewStore(db)
	organizations, err := store.ListOrganizations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(organizations) != 1 || organizations[0].RepositoryCount != 1 ||
		organizations[0].EnabledRepositoryCount != 1 {
		t.Fatalf("organizations = %#v", organizations)
	}

	organization, err := store.GetOrganization(ctx, organizationID)
	if err != nil {
		t.Fatal(err)
	}
	if organization.Name != "Agent Trail" {
		t.Fatalf("organization = %#v", organization)
	}

	repositories, err := store.ListRepositories(ctx, organizationID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].ActiveTaskCount != 1 ||
		repositories[0].Settings.DefaultPolicy != "restricted" {
		t.Fatalf("repositories = %#v", repositories)
	}

	detail, err := store.GetRepository(ctx, repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.DefaultBranch != "trunk" || detail.Settings.ValidationFile != ".ci/checks.yaml" {
		t.Fatalf("detail = %#v", detail)
	}
	if detail.Metrics.TotalTasks != 2 || detail.Metrics.ActiveTasks != 1 ||
		detail.Metrics.CompletedTasks != 1 || detail.Metrics.CompletionRate == nil ||
		*detail.Metrics.CompletionRate != 1 {
		t.Fatalf("metrics = %#v", detail.Metrics)
	}
	if len(detail.ActiveTasks) != 1 || detail.ActiveTasks[0].ID != active.ID ||
		len(detail.RecentTasks) != 2 {
		t.Fatalf("tasks = active %#v recent %#v", detail.ActiveTasks, detail.RecentTasks)
	}

	settings, err := store.GetRepositorySettings(ctx, repositoryID)
	if err != nil || settings.DefaultPolicy != "restricted" {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
}

func TestRepositoryDefaultsAndMissingResources(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	organizationID, repositoryID := seedRepository(t, db)
	if _, err := db.ExecContext(ctx,
		`UPDATE repositories SET settings_json = '{}' WHERE id = $1`, repositoryID); err != nil {
		t.Fatal(err)
	}

	settings, err := NewStore(db).GetRepositorySettings(ctx, repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.DefaultPolicy != defaultPolicy ||
		settings.ValidationFile != defaultValidationFile {
		t.Fatalf("settings = %#v", settings)
	}

	store := NewStore(db)
	if _, err := store.GetOrganization(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("organization error = %v", err)
	}
	if _, err := store.GetRepository(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("repository error = %v", err)
	}
	_ = organizationID
}

func TestRunnerDetail(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	organizationID, repositoryID := seedRepository(t, db)
	taskStore := task.NewStore(db)
	work := createRepositoryTask(t, db, organizationID, repositoryID, "Runner task")
	runnerStore := runner.NewStore(db)
	registered, err := runnerStore.Register(ctx, runner.RegisterParams{
		Type: "process", HostnameOrPod: "runner-1", Capacity: 2,
		Labels: map[string]string{
			"cpu_percent": "42.5", "memory_percent": "61", "disk_percent": "bad",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := runnerStore.Claim(ctx, registered.ID, 60000000000)
	if err != nil || claim == nil {
		t.Fatalf("claim = %#v, err = %v", claim, err)
	}

	store := NewStore(db)
	detail, err := store.GetRunner(ctx, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ActiveTaskCount != 1 || len(detail.CurrentTasks) != 1 ||
		detail.CurrentTasks[0].ID != work.ID || detail.Resources.CPUPercent == nil ||
		*detail.Resources.CPUPercent != 42.5 || detail.Resources.DiskPercent != nil {
		t.Fatalf("runner detail = %#v", detail)
	}

	transition(t, taskStore, work.ID,
		task.StatusProvisioning, task.StatusPlanning, task.StatusExecuting,
		task.StatusFailed)
	detail, err = store.GetRunner(ctx, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.CurrentTasks) != 0 || len(detail.RecentFailures) != 1 ||
		detail.RecentFailures[0].ID != work.ID {
		t.Fatalf("runner after failure = %#v", detail)
	}

	runners, err := store.ListRunners(ctx)
	if err != nil || len(runners) != 1 {
		t.Fatalf("runners = %#v, err = %v", runners, err)
	}
	if _, err := store.GetRunner(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("runner error = %v", err)
	}
}
