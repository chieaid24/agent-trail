// Command worker runs the configured process or Kubernetes backend.
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/config"
	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
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

	store := runner.NewStore(db)
	tasks := task.NewStore(db)
	telemetry, err := observability.Setup("worker", cfg.OTLPEndpoint, logger)
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
	observability.RegisterRunnerResources(metrics, cfg.WorkspaceRoot, logger)
	runnerMetrics := runner.NewMetrics(metrics)

	backend, err := buildBackend(cfg, db, store, tasks, logger, metrics, runnerMetrics)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.LogAttrs(ctx, slog.LevelInfo, "worker started",
		slog.String("event", "worker_started"),
		slog.String("runner_backend", cfg.RunnerType),
		slog.String("agent_provider", cfg.AgentProvider),
	)
	err = backend.Run(ctx)
	logger.LogAttrs(context.Background(), slog.LevelInfo, "worker shutting down",
		slog.String("event", "worker_shutdown"),
	)
	return err
}

func buildBackend(cfg config.Config, db *sql.DB, store *runner.Store, tasks *task.Store,
	logger *slog.Logger, metrics *observability.Registry, runnerMetrics *runner.Metrics,
) (runner.RunnerBackend, error) {
	if cfg.RunnerType == "kubernetes" && cfg.TaskAttemptID == "" {
		template, err := os.ReadFile(cfg.RunnerJobTemplate)
		if err != nil {
			return nil, fmt.Errorf("read runner Job template: %w", err)
		}
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("load in-cluster Kubernetes config: %w", err)
		}
		client, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, fmt.Errorf("create Kubernetes client: %w", err)
		}
		return &runner.KubernetesBackend{
			Jobs:                client.BatchV1().Jobs(cfg.RunnerNamespace),
			Store:               store,
			Tasks:               tasks,
			Logger:              logger,
			Template:            template,
			Namespace:           cfg.RunnerNamespace,
			RunnerImage:         cfg.RunnerImage,
			JobTTL:              cfg.RunnerJobTTL,
			DefaultRuntime:      cfg.DefaultTaskRuntime,
			Poll:                cfg.WorkerPoll,
			WorkerIdleExit:      cfg.WorkerIdleExit,
			AgentProvider:       cfg.AgentProvider,
			AgentCLIPath:        cfg.AgentCLIPath,
			AgentModel:          cfg.AgentModel,
			PermissionMode:      cfg.AgentPermissionMode,
			AgentCLIVersion:     cfg.AgentCLIVersion,
			ConflictLLMEnabled:  cfg.ConflictLLMEnabled,
			ConflictLLMProvider: cfg.ConflictLLMProvider,
			ConflictLLMModel:    cfg.ConflictLLMModel,
			OTLPEndpoint:        cfg.OTLPEndpoint,
			GitHubAPIBase:       cfg.GitHubAPIBaseURL,
		}, nil
	}

	adapter, err := agent.New(agent.Options{
		Provider:       cfg.AgentProvider,
		CLIPath:        cfg.AgentCLIPath,
		Model:          cfg.AgentModel,
		PermissionMode: cfg.AgentPermissionMode,
		PinnedVersion:  cfg.AgentCLIVersion,
		Logger:         logger,
	})
	if err != nil {
		return nil, err
	}
	validateCtx, validateCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer validateCancel()
	if err := adapter.ValidateConfiguration(validateCtx); err != nil {
		return nil, fmt.Errorf("agent adapter %q: %w", adapter.Name(), err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	var workspaces *gitworkspace.Manager
	var publishAPI runner.PublishGitHub
	var repos runner.RepositoryResolver
	var conflicts *conflict.Detector
	if cfg.GitHubEnabled() {
		keyPEM, err := os.ReadFile(cfg.GitHubAppPrivateKeyPath)
		if err != nil {
			return nil, err
		}
		client, err := github.NewClient(cfg.GitHubAppID, keyPEM,
			cfg.GitHubAPIBaseURL, metrics)
		if err != nil {
			return nil, err
		}
		workspaces, err = gitworkspace.New(cfg.WorkspaceRoot, logger, metrics)
		if err != nil {
			return nil, err
		}
		publishAPI = client
		repos = github.NewStore(db)
		semantic, err := conflict.NewSemantic(conflict.SemanticOptions{
			Enabled:  cfg.ConflictLLMEnabled,
			Provider: cfg.ConflictLLMProvider,
			APIKey:   cfg.AnthropicAPIKey,
			Model:    cfg.ConflictLLMModel,
		})
		if err != nil {
			return nil, err
		}
		conflicts = &conflict.Detector{
			Git:      workspaces,
			Records:  conflict.NewStore(db),
			Logger:   logger,
			Semantic: semantic,
		}
	}

	maxTasks := cfg.WorkerMaxTasks
	if cfg.TaskAttemptID != "" {
		maxTasks = 1
	}
	host := &runner.Host{
		Store: store,
		Executor: &runner.Executor{
			Tasks:          tasks,
			Store:          store,
			Validations:    validation.NewStore(db),
			Evidence:       evidence.NewStore(db),
			Adapter:        adapter,
			Logger:         logger,
			Workspaces:     workspaces,
			GitHub:         publishAPI,
			Repos:          repos,
			Conflicts:      conflicts,
			Metrics:        runnerMetrics,
			LeaseDuration:  cfg.RunnerLease,
			DefaultRuntime: cfg.DefaultTaskRuntime,
		},
		Logger:        logger,
		Metrics:       runnerMetrics,
		RunnerType:    cfg.RunnerType,
		HostnameOrPod: hostname,
		AttemptID:     cfg.TaskAttemptID,
		Lease:         cfg.RunnerLease,
		Heartbeat:     cfg.RunnerHeartbeat,
		LostAfter:     cfg.RunnerLostAfter,
		Poll:          cfg.WorkerPoll,
		MaxTasks:      maxTasks,
		IdleExit:      cfg.WorkerIdleExit,
	}

	return &runner.ProcessBackend{Host: host}, nil
}
