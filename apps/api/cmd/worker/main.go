// Command worker is the runner host: it registers a process runner, claims
// task attempts with expiring leases, executes them with the fake agent
// adapter (real adapters land with milestone 5), heartbeats the registry,
// and reaps lost runners. Spec: docs/architecture/runner.md.
package main

import (
	"context"
	"database/sql"
	"errors"
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
	store := runner.NewStore(db)
	tasks := task.NewStore(db)
	host := &runner.Host{
		Store: store,
		Executor: &runner.Executor{
			Tasks:         tasks,
			Store:         store,
			Validations:   validation.NewStore(db),
			Evidence:      evidence.NewStore(db),
			Adapter:       agent.NewFake(),
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
	)
	err = host.Run(ctx)
	logger.LogAttrs(context.Background(), slog.LevelInfo, "worker shutting down",
		slog.String("event", "worker_shutdown"),
	)
	return err
}
