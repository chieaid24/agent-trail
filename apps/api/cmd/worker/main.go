// Command worker is the runner host: it registers a process runner, claims
// task attempts with expiring leases, executes them with the configured agent
// adapter (fake by default, or the Claude Code CLI), heartbeats the registry,
// and reaps lost runners. Spec: docs/architecture/runner.md and
// docs/architecture/agent-providers.md.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exited", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, "worker", cfg.LogLevel)

	if cfg.DatabaseURL == "" {
		return errors.New("worker requires DATABASE_URL")
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	adapter, err := agent.New(agent.Options{
		Provider:       cfg.AgentProvider,
		CLIPath:        cfg.AgentCLIPath,
		Model:          cfg.AgentModel,
		PermissionMode: cfg.AgentPermissionMode,
		PinnedVersion:  cfg.AgentCLIVersion,
		Timeout:        cfg.AgentTimeout,
		Logger:         logger,
	})
	if err != nil {
		return err
	}
	// Fail fast on a misconfigured provider (missing or drifted CLI) before
	// claiming any work, so it surfaces once at startup, not on every attempt.
	validateCtx, validateCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := adapter.ValidateConfiguration(validateCtx); err != nil {
		validateCancel()
		return fmt.Errorf("agent adapter %q: %w", adapter.Name(), err)
	}
	validateCancel()

	store := runner.NewStore(db)
	tasks := task.NewStore(db)
	host := &runner.Host{
		Store: store,
		Executor: &runner.Executor{
			Tasks:         tasks,
			Store:         store,
			Validations:   validation.NewStore(db),
			Evidence:      evidence.NewStore(db),
			Adapter:       adapter,
			Logger:        logger,
			LeaseDuration: cfg.RunnerLease,
		},
		Logger:        logger,
		RunnerType:    "process",
		HostnameOrPod: hostname,
		Lease:         cfg.RunnerLease,
		Heartbeat:     cfg.RunnerHeartbeat,
		LostAfter:     cfg.RunnerLostAfter,
		Poll:          cfg.WorkerPoll,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.LogAttrs(ctx, slog.LevelInfo, "worker started",
		slog.String("event", "worker_started"),
		slog.String("agent_provider", adapter.Name()),
	)
	err = host.Run(ctx)
	logger.LogAttrs(context.Background(), slog.LevelInfo, "worker shutting down",
		slog.String("event", "worker_shutdown"),
	)
	return err
}
