// Command seed loads demo tasks in representative states so the dashboard
// and API have data to show. Idempotent: it refuses to run against a
// database that already has tasks. DATABASE_URL must be set.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed exited", "error", err.Error())
		os.Exit(1)
	}
}

// demoTask pairs create params with the statuses to walk the task through.
type demoTask struct {
	title        string
	instructions string
	repository   int
	path         []task.TransitionParams
}

func demoTasks() []demoTask {
	return []demoTask{
		{
			title:        "Demo: fix flaky login test",
			instructions: "Investigate the flaky login integration test and make it deterministic.",
			repository:   0,
			path: []task.TransitionParams{
				{To: task.StatusProvisioning}, {To: task.StatusPlanning},
				{To: task.StatusExecuting}, {To: task.StatusValidating},
				{To: task.StatusPublishing}, {To: task.StatusAwaitingReview},
				{To: task.StatusCompleted},
			},
		},
		{
			title:        "Demo: add pagination to the audit log",
			instructions: "Add cursor pagination to the audit log endpoint.",
			repository:   -1,
			// Stays queued: shows the pending column.
		},
		{
			title:        "Demo: upgrade the TLS library",
			instructions: "Upgrade the TLS dependency and run the full test suite.",
			repository:   1,
			path: []task.TransitionParams{
				{To: task.StatusProvisioning}, {To: task.StatusPlanning},
				{To: task.StatusExecuting}, {To: task.StatusValidating},
				{
					To:             task.StatusFailed,
					FailureCode:    "validation_failed",
					FailureMessage: "2 of 148 tests failed after the upgrade",
				},
			},
		},
		{
			title:        "Demo: rename the billing module",
			instructions: "Rename billing to invoicing across the codebase.",
			repository:   -1,
			path: []task.TransitionParams{
				{To: task.StatusProvisioning}, {To: task.StatusPlanning},
				{To: task.StatusExecuting},
			},
		},
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	logger := observability.NewLogger(os.Stdout, "seed", cfg.LogLevel)

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var existing int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks`).Scan(&existing); err != nil {
		return fmt.Errorf("count tasks (did migrations run?): %w", err)
	}
	if existing > 0 {
		logger.LogAttrs(ctx, slog.LevelInfo, "database already has tasks; nothing to seed",
			slog.String("event", "seed_skipped"),
			slog.Int("existing_tasks", existing),
		)
		return nil
	}

	organizationID, repositoryIDs, err := seedRepositories(ctx, db)
	if err != nil {
		return err
	}
	store := task.NewStore(db)
	for _, d := range demoTasks() {
		params := task.CreateParams{Title: d.title, Instructions: d.instructions}
		if d.repository >= 0 {
			params.OrganizationID = &organizationID
			params.RepositoryID = &repositoryIDs[d.repository]
		}
		t, err := store.Create(ctx, params)
		if err != nil {
			return fmt.Errorf("create %q: %w", d.title, err)
		}
		for _, step := range d.path {
			if t, err = store.Transition(ctx, t.ID, step); err != nil {
				return fmt.Errorf("transition %q to %s: %w", d.title, step.To, err)
			}
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "seeded task",
			slog.String("event", "seed_task_created"),
			slog.String("task_id", t.ID),
			slog.String("status", string(t.Status)),
		)
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "seed complete",
		slog.String("event", "seed_done"),
		slog.Int("tasks", len(demoTasks())),
	)
	return nil
}

func seedRepositories(ctx context.Context, db *sql.DB) (string, []string, error) {
	var organizationID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO organizations
			(name, slug, github_account_id, github_account_login,
			 github_account_type)
		VALUES ('Agent Trail', 'agent-trail', 118775203, 'chieaid24', 'User')
		RETURNING id`).Scan(&organizationID)
	if err != nil {
		return "", nil, fmt.Errorf("seed organization: %w", err)
	}

	type repository struct {
		githubID int64
		name     string
		settings string
	}
	repositories := []repository{
		{965381124, "agent-trail",
			`{"default_policy":"restricted","validation_file":".agent-trail/validation.yaml"}`},
		{965381125, "runner-images",
			`{"default_policy":"platform default","validation_file":".agent-trail/validation.yaml"}`},
	}
	ids := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		var id string
		err := db.QueryRowContext(ctx, `
			INSERT INTO repositories
				(organization_id, github_repository_id, owner, name,
				 full_name, clone_url, settings_json)
			VALUES ($1, $2, 'chieaid24', $3, 'chieaid24/' || $3,
				'https://github.com/chieaid24/' || $3 || '.git', $4)
			RETURNING id`, organizationID, repository.githubID,
			repository.name, repository.settings).Scan(&id)
		if err != nil {
			return "", nil, fmt.Errorf("seed repository %s: %w", repository.name, err)
		}
		ids = append(ids, id)
	}
	return organizationID, ids, nil
}
