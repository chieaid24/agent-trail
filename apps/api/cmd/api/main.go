// Command api serves the Agent Trail control-plane HTTP API.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/auth"
	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
	"github.com/chieaid24/agent-trail/apps/api/internal/dashboard"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/httpapi"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
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

	telemetry, err := observability.Setup("api", cfg.OTLPEndpoint, logger)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			logger.LogAttrs(shutdownCtx, slog.LevelWarn, "telemetry shutdown failed",
				slog.String("event", "otel_shutdown_failed"),
				slog.String("error", err.Error()),
			)
		}
	}()
	metrics := telemetry.Metrics

	var pinger httpapi.DBPinger
	var tasks httpapi.TaskService
	var validations httpapi.ValidationService
	var evidenceReports httpapi.EvidenceService
	var taskStore *task.Store
	var apiOptions []httpapi.Option
	if db != nil {
		pinger = db
		taskStore = task.NewStore(db)
		tasks = taskStore
		validations = validation.NewStore(db)
		evidenceReports = evidence.NewStore(db)
		apiOptions = append(apiOptions,
			httpapi.WithDashboard(dashboard.NewStore(db)),
			httpapi.WithConflicts(conflict.NewStore(db)))
	}

	// Sessions live in the database; refuse a half-configured session
	// layer instead of silently serving without one.
	if cfg.AuthEnabled() && db == nil {
		return errors.New(
			"GITHUB_OAUTH_CLIENT_ID and GITHUB_OAUTH_CLIENT_SECRET are set " +
				"but DATABASE_URL is not; sessions need the database")
	}
	if cfg.AuthEnabled() {
		oauthClient := auth.NewOAuthClient(cfg.GitHubOAuthClientID,
			cfg.GitHubOAuthClientSecret, cfg.GitHubOAuthBaseURL,
			cfg.GitHubAPIBaseURL, metrics)
		installURL := ""
		if cfg.GitHubAppSlug != "" {
			installURL = "https://github.com/apps/" + cfg.GitHubAppSlug +
				"/installations/new"
		}
		authService := auth.NewService(oauthClient, auth.NewStore(db), logger,
			installURL)
		apiOptions = append(apiOptions, httpapi.WithAuth(authService,
			cfg.AuthPublicOrigin, cfg.AuthCookieSecure))
	}
	var webhook http.Handler
	var processor *github.Processor
	// The webhook needs both the GitHub credentials and the database.
	if cfg.GitHubEnabled() && db != nil {
		keyPEM, err := os.ReadFile(cfg.GitHubAppPrivateKeyPath)
		if err != nil {
			return err
		}
		client, err := github.NewClient(cfg.GitHubAppID, keyPEM,
			cfg.GitHubAPIBaseURL, metrics)
		if err != nil {
			return err
		}
		ghStore := github.NewStore(db)
		processor = github.NewProcessor(ghStore, taskStore, client, logger, metrics)
		webhook = github.NewWebhook([]byte(cfg.GitHubWebhookSecret), ghStore,
			processor, logger, metrics)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr: cfg.APIAddr,
		Handler: httpapi.New(logger, pinger, tasks, validations,
			evidenceReports, webhook, metrics.Handler(), apiOptions...).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		// Request contexts derive from the signal context, so long-lived
		// handlers (the SSE stream) end when shutdown starts instead of
		// pinning Shutdown to its deadline.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

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
	if processor != nil {
		processor.Wait() // in-flight deliveries are bounded by processTimeout
	}
	return nil
}
