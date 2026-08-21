package config

import (
	"log/slog"
	"testing"
	"time"
)

// clearEnv makes the test hermetic against ambient configuration.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"API_ADDR", "DATABASE_URL", "LOG_LEVEL",
		"RUNNER_LEASE_SECONDS", "RUNNER_HEARTBEAT_SECONDS",
		"RUNNER_LOST_AFTER_SECONDS", "WORKER_POLL_SECONDS",
		"RUNNER_TYPE", "TASK_ATTEMPT_ID", "RUNNER_IMAGE",
		"RUNNER_JOB_TEMPLATE", "RUNNER_NAMESPACE", "RUNNER_TTL_SECONDS",
		"WORKER_MAX_TASKS", "WORKER_IDLE_EXIT_SECONDS",
		"WORKSPACE_ROOT",
		"AGENT_PROVIDER", "AGENT_CLI_PATH", "AGENT_MODEL",
		"AGENT_PERMISSION_MODE", "AGENT_CLI_VERSION", "AGENT_TIMEOUT_SECONDS",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET",
		"GITHUB_OAUTH_BASE_URL", "GITHUB_APP_SLUG",
		"AUTH_PUBLIC_ORIGIN", "AUTH_COOKIE_SECURE",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIAddr != ":8080" {
		t.Errorf("APIAddr = %q, want :8080", cfg.APIAddr)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty", cfg.DatabaseURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.RunnerLease != 60*time.Second || cfg.RunnerHeartbeat != 10*time.Second ||
		cfg.RunnerLostAfter != 30*time.Second || cfg.WorkerPoll != 2*time.Second {
		t.Errorf("runner durations = %v/%v/%v/%v, want 60s/10s/30s/2s",
			cfg.RunnerLease, cfg.RunnerHeartbeat, cfg.RunnerLostAfter, cfg.WorkerPoll)
	}
	if cfg.WorkspaceRoot != "/var/lib/agent-trail" {
		t.Errorf("WorkspaceRoot = %q, want /var/lib/agent-trail", cfg.WorkspaceRoot)
	}
	if cfg.AgentProvider != "fake" || cfg.AgentCLIPath != "claude" ||
		cfg.AgentPermissionMode != "acceptEdits" {
		t.Errorf("agent defaults = %q/%q/%q, want fake/claude/acceptEdits",
			cfg.AgentProvider, cfg.AgentCLIPath, cfg.AgentPermissionMode)
	}
	if cfg.AgentModel != "" || cfg.AgentCLIVersion != "" {
		t.Errorf("agent model/version = %q/%q, want empty", cfg.AgentModel, cfg.AgentCLIVersion)
	}
	if cfg.DefaultTaskRuntime != 2700*time.Second {
		t.Errorf("DefaultTaskRuntime = %v, want 2700s", cfg.DefaultTaskRuntime)
	}
	if cfg.OTLPEndpoint != "localhost:4317" {
		t.Errorf("OTLPEndpoint = %q, want localhost:4317", cfg.OTLPEndpoint)
	}
}

func TestLoadAgentProviderOverrideAndValidation(t *testing.T) {
	clearEnv(t)
	t.Setenv("AGENT_PROVIDER", "claude-code")
	t.Setenv("AGENT_CLI_PATH", "/usr/local/bin/claude")
	t.Setenv("AGENT_MODEL", "claude-sonnet-5")
	t.Setenv("AGENT_TIMEOUT_SECONDS", "600")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AgentProvider != "claude-code" || cfg.AgentCLIPath != "/usr/local/bin/claude" ||
		cfg.AgentModel != "claude-sonnet-5" || cfg.DefaultTaskRuntime != 600*time.Second {
		t.Errorf("agent overrides = %q/%q/%q/%v", cfg.AgentProvider,
			cfg.AgentCLIPath, cfg.AgentModel, cfg.DefaultTaskRuntime)
	}

	clearEnv(t)
	t.Setenv("AGENT_PROVIDER", "gpt")
	if _, err := Load(); err == nil {
		t.Error("AGENT_PROVIDER=gpt accepted, want error")
	}

	clearEnv(t)
	t.Setenv("AGENT_PERMISSION_MODE", "yolo")
	if _, err := Load(); err == nil {
		t.Error("AGENT_PERMISSION_MODE=yolo accepted, want error")
	}
}

func TestLoadRejectsRelativeWorkspaceRoot(t *testing.T) {
	clearEnv(t)
	t.Setenv("WORKSPACE_ROOT", "relative/path")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a relative WORKSPACE_ROOT")
	}
}

func TestLoadRunnerDurationOverridesAndValidation(t *testing.T) {
	clearEnv(t)
	t.Setenv("RUNNER_LEASE_SECONDS", "120")
	t.Setenv("WORKER_POLL_SECONDS", "1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RunnerLease != 2*time.Minute || cfg.WorkerPoll != time.Second {
		t.Errorf("overrides = %v/%v, want 2m/1s", cfg.RunnerLease, cfg.WorkerPoll)
	}

	for _, bad := range []string{"0", "-5", "abc", "1.5"} {
		clearEnv(t)
		t.Setenv("RUNNER_LEASE_SECONDS", bad)
		if _, err := Load(); err == nil {
			t.Errorf("RUNNER_LEASE_SECONDS=%q accepted, want error", bad)
		}
	}

	clearEnv(t)
	t.Setenv("RUNNER_HEARTBEAT_SECONDS", "30")
	t.Setenv("RUNNER_LOST_AFTER_SECONDS", "30")
	if _, err := Load(); err == nil {
		t.Error("lost-after == heartbeat accepted, want error")
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("API_ADDR", "127.0.0.1:9999")
	t.Setenv("DATABASE_URL", "postgres://localhost/agent_trail")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIAddr != "127.0.0.1:9999" {
		t.Errorf("APIAddr = %q", cfg.APIAddr)
	}
	if cfg.DatabaseURL != "postgres://localhost/agent_trail" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
}

func TestLoadRejectsBadAddr(t *testing.T) {
	for _, bad := range []string{"8080", ":http", ":99999", "host:", "host"} {
		t.Run(bad, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("API_ADDR", bad)
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted API_ADDR %q", bad)
			}
		})
	}
}

func TestLoadAcceptsEphemeralPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("API_ADDR", "127.0.0.1:0")
	if _, err := Load(); err != nil {
		t.Fatalf("Load rejected 127.0.0.1:0: %v", err)
	}
}

func TestLoadAuthDefaultsAndOverrides(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AuthEnabled() {
		t.Error("AuthEnabled() = true without OAuth credentials")
	}
	if cfg.AuthPublicOrigin != "http://localhost:3000" {
		t.Errorf("AuthPublicOrigin = %q, want http://localhost:3000", cfg.AuthPublicOrigin)
	}
	if cfg.AuthCookieSecure {
		t.Error("AuthCookieSecure = true, want false default")
	}

	clearEnv(t)
	t.Setenv("GITHUB_OAUTH_CLIENT_ID", "Iv1client")
	t.Setenv("GITHUB_OAUTH_CLIENT_SECRET", "secret")
	t.Setenv("AUTH_PUBLIC_ORIGIN", "https://trail.example.com")
	t.Setenv("AUTH_COOKIE_SECURE", "true")
	t.Setenv("GITHUB_APP_SLUG", "agent-trail")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AuthEnabled() {
		t.Error("AuthEnabled() = false with OAuth credentials")
	}
	if cfg.AuthPublicOrigin != "https://trail.example.com" ||
		!cfg.AuthCookieSecure || cfg.GitHubAppSlug != "agent-trail" {
		t.Errorf("auth overrides = %q/%v/%q", cfg.AuthPublicOrigin,
			cfg.AuthCookieSecure, cfg.GitHubAppSlug)
	}
}

func TestLoadRejectsPartialOAuthConfig(t *testing.T) {
	for _, key := range []string{"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET"} {
		clearEnv(t)
		t.Setenv(key, "only-one")
		if _, err := Load(); err == nil {
			t.Errorf("Load accepted %s without its pair", key)
		}
	}
}

func TestLoadRejectsBadAuthOrigin(t *testing.T) {
	for _, bad := range []string{"localhost:3000", "http://localhost:3000/app",
		"http://localhost:3000?x=1", "://nope"} {
		clearEnv(t)
		t.Setenv("AUTH_PUBLIC_ORIGIN", bad)
		if _, err := Load(); err == nil {
			t.Errorf("Load accepted AUTH_PUBLIC_ORIGIN %q", bad)
		}
	}
}

func TestLoadRejectsBadCookieSecure(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_COOKIE_SECURE", "yes")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted AUTH_COOKIE_SECURE=yes")
	}
}

func TestLoadRejectsBadLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "loud")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted invalid LOG_LEVEL")
	}
}

func TestLoadRunnerTypeAndWorkerCaps(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RunnerType != "process" {
		t.Errorf("RunnerType = %q, want process", cfg.RunnerType)
	}
	if cfg.WorkerMaxTasks != 0 {
		t.Errorf("WorkerMaxTasks = %d, want 0", cfg.WorkerMaxTasks)
	}
	if cfg.WorkerIdleExit != 0 {
		t.Errorf("WorkerIdleExit = %v, want 0", cfg.WorkerIdleExit)
	}

	t.Setenv("RUNNER_TYPE", "kubernetes")
	t.Setenv("RUNNER_IMAGE", "agent-trail/runner:test")
	t.Setenv("WORKER_MAX_TASKS", "1")
	t.Setenv("WORKER_IDLE_EXIT_SECONDS", "90")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RunnerType != "kubernetes" {
		t.Errorf("RunnerType = %q, want kubernetes", cfg.RunnerType)
	}
	if cfg.RunnerImage != "agent-trail/runner:test" ||
		cfg.RunnerJobTemplate != "/etc/agent-trail/runner-job.yaml" ||
		cfg.RunnerNamespace != "agent-trail-runners" || cfg.RunnerJobTTL != 5*time.Minute {
		t.Errorf("kubernetes config = %q/%q/%q/%v", cfg.RunnerImage,
			cfg.RunnerJobTemplate, cfg.RunnerNamespace, cfg.RunnerJobTTL)
	}
	if cfg.WorkerMaxTasks != 1 {
		t.Errorf("WorkerMaxTasks = %d, want 1", cfg.WorkerMaxTasks)
	}
	if cfg.WorkerIdleExit != 90*time.Second {
		t.Errorf("WorkerIdleExit = %v, want 90s", cfg.WorkerIdleExit)
	}

	for key, bad := range map[string]string{
		"RUNNER_TYPE":              "vm",
		"RUNNER_TTL_SECONDS":       "0",
		"WORKER_MAX_TASKS":         "-1",
		"WORKER_IDLE_EXIT_SECONDS": "ten",
	} {
		t.Run(key, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Errorf("Load accepted %s=%q", key, bad)
			}
		})
	}

	clearEnv(t)
	t.Setenv("RUNNER_TYPE", "kubernetes")
	if _, err := Load(); err == nil {
		t.Error("kubernetes controller accepted an empty RUNNER_IMAGE")
	}

	clearEnv(t)
	t.Setenv("RUNNER_TYPE", "kubernetes")
	t.Setenv("TASK_ATTEMPT_ID", "attempt-id")
	if _, err := Load(); err != nil {
		t.Fatalf("kubernetes task runner config: %v", err)
	}
}
