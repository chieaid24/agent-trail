// Command api serves the Agent Trail control-plane HTTP API.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/httpapi"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api exited", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, "api", cfg.LogLevel)

	var db *sql.DB
	if cfg.DatabaseURL != "" {
		db, err = sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer db.Close()
	}

	var pinger httpapi.DBPinger
	var tasks httpapi.TaskService
	if db != nil {
		pinger = db
		tasks = task.NewStore(db)
	}
	srv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           httpapi.New(logger, pinger, tasks).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		logger.LogAttrs(ctx, slog.LevelInfo, "api listening",
			slog.String("event", "api_listening"),
			slog.String("addr", cfg.APIAddr),
		)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.LogAttrs(context.Background(), slog.LevelInfo, "api shutting down",
		slog.String("event", "api_shutdown"),
	)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
