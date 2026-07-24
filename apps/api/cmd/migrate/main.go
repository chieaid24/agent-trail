// Command migrate applies the embedded SQL migrations with goose.
//
// Usage: migrate <up|status|version>
// DATABASE_URL must be set.
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
	"github.com/pressly/goose/v3"

	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("migrate exited", "error", err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: migrate <up|status|version>")
	}
	command := args[0]
	switch command {
	case "up", "status", "version":
	default:
		return fmt.Errorf("unknown command %q: want up, status, or version", command)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	logger := observability.NewLogger(os.Stdout, "migrate", cfg.LogLevel)

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return err
	}

	traceID := observability.NewTraceID()
	logger.LogAttrs(ctx, slog.LevelInfo, "running migration command",
		slog.String("event", "migrate_start"),
		slog.String("trace_id", traceID),
		slog.String("command", command),
	)

	switch command {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return err
		}
		for _, r := range results {
			logger.LogAttrs(ctx, slog.LevelInfo, "applied migration",
				slog.String("event", "migration_applied"),
				slog.String("trace_id", traceID),
				slog.Int64("version", r.Source.Version),
				slog.String("path", r.Source.Path),
			)
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "migrations complete",
			slog.String("event", "migrate_done"),
			slog.String("trace_id", traceID),
			slog.Int("applied", len(results)),
		)
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			state := "pending"
			if !s.AppliedAt.IsZero() {
				state = "applied"
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "migration status",
				slog.String("event", "migration_status"),
				slog.String("trace_id", traceID),
				slog.Int64("version", s.Source.Version),
				slog.String("path", s.Source.Path),
				slog.String("state", state),
			)
		}
	case "version":
		version, err := provider.GetDBVersion(ctx)
		if err != nil {
			return err
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "database version",
			slog.String("event", "migration_version"),
			slog.String("trace_id", traceID),
			slog.Int64("version", version),
		)
	}
	return nil
}
