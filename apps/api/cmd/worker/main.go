// Command worker is the task-scheduler skeleton. It runs a heartbeat loop
// with graceful shutdown; task claiming lands with the task domain milestone.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

const heartbeatInterval = 10 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("worker exited", "error", err.Error())
		os.Exit(1)
	}
	logger := observability.NewLogger("worker", cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.LogAttrs(ctx, slog.LevelInfo, "worker started",
		slog.String("event", "worker_started"),
	)
	runLoop(ctx, logger, heartbeatInterval)
	logger.LogAttrs(context.Background(), slog.LevelInfo, "worker shutting down",
		slog.String("event", "worker_shutdown"),
	)
}

// runLoop beats until ctx is cancelled. Extracted for testing.
func runLoop(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logger.LogAttrs(ctx, slog.LevelDebug, "worker heartbeat",
				slog.String("event", "worker_heartbeat"),
			)
		}
	}
}
