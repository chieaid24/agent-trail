// fail fast: a process with bad configuration must not start
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type Config struct {
	APIAddr string
	// optional for api (readiness reports unconfigured); required by migrate + worker
	DatabaseURL        string
	LogLevel           slog.Level
	RunnerLease        time.Duration
	RunnerHeartbeat    time.Duration
	RunnerLostAfter    time.Duration
	WorkerPoll         time.Duration
	DefaultTaskRuntime time.Duration
	RunnerType         string
	TaskAttemptID      string
	RunnerImage        string
	RunnerJobTemplate  string
	RunnerNamespace    string
	RunnerJobTTL       time.Duration
	// zero runs forever; k8s job runner sets 1 so the job completes and ttl cleanup applies
	WorkerMaxTasks int
	WorkerIdleExit time.Duration
	WorkspaceRoot  string
	// all three set together or none (unset -> webhook 503); api base url is a test-only override
	GitHubWebhookSecret     string
	GitHubAppID             string
	GitHubAppPrivateKeyPath string
	GitHubAPIBaseURL        string
	// both set together or neither (unset -> auth 503, api open for localhost dev)
	GitHubOAuthClientID     string
	GitHubOAuthClientSecret string
	GitHubOAuthBaseURL      string
	GitHubAppSlug           string
	AuthPublicOrigin        string
	AuthCookieSecure        bool
	AgentProvider           string
	AgentCLIPath            string
	AgentModel              string
	AgentPermissionMode     string
	AgentCLIVersion         string
	ConflictLLMEnabled      bool
	ConflictLLMProvider     string
	ConflictLLMModel        string
	// never logged
	AnthropicAPIKey string
	// "off" disables export
	OTLPEndpoint string
}

func (c Config) GitHubEnabled() bool { return c.GitHubWebhookSecret != "" }

func (c Config) AuthEnabled() bool { return c.GitHubOAuthClientID != "" }

func Load() (Config, error) {
	cfg := Config{
		APIAddr:                 envOr("API_ADDR", ":8080"),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		GitHubWebhookSecret:     os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubAppID:             os.Getenv("GITHUB_APP_ID"),
		GitHubAppPrivateKeyPath: os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"),
		GitHubAPIBaseURL:        os.Getenv("GITHUB_API_BASE_URL"),
		GitHubOAuthClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		GitHubOAuthClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		GitHubOAuthBaseURL:      os.Getenv("GITHUB_OAUTH_BASE_URL"),
		GitHubAppSlug:           os.Getenv("GITHUB_APP_SLUG"),
		AuthPublicOrigin:        envOr("AUTH_PUBLIC_ORIGIN", "http://localhost:3000"),
		WorkspaceRoot:           envOr("WORKSPACE_ROOT", "/var/lib/agent-trail"),
		RunnerType:              envOr("RUNNER_TYPE", "process"),
		TaskAttemptID:           os.Getenv("TASK_ATTEMPT_ID"),
		RunnerImage:             os.Getenv("RUNNER_IMAGE"),
		RunnerJobTemplate:       envOr("RUNNER_JOB_TEMPLATE", "/etc/agent-trail/runner-job.yaml"),
		RunnerNamespace:         "agent-trail-runners",
		AgentProvider:           envOr("AGENT_PROVIDER", "fake"),
		AgentCLIPath:            envOr("AGENT_CLI_PATH", "claude"),
		AgentModel:              os.Getenv("AGENT_MODEL"),
		AgentPermissionMode:     envOr("AGENT_PERMISSION_MODE", "acceptEdits"),
		AgentCLIVersion:         os.Getenv("AGENT_CLI_VERSION"),
		ConflictLLMProvider:     envOr("CONFLICT_LLM_PROVIDER", "fake"),
		ConflictLLMModel:        envOr("CONFLICT_LLM_MODEL", "claude-sonnet-4-6"),
		AnthropicAPIKey:         os.Getenv("ANTHROPIC_API_KEY"),
		OTLPEndpoint:            envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
	}

	if err := validateAddr(cfg.APIAddr); err != nil {
		return Config{}, err
	}
	if !filepath.IsAbs(cfg.WorkspaceRoot) {
		return Config{}, fmt.Errorf("WORKSPACE_ROOT %q must be an absolute path", cfg.WorkspaceRoot)
	}
	if err := validateGitHub(cfg); err != nil {
		return Config{}, err
	}
	if err := validateAuth(cfg); err != nil {
		return Config{}, err
	}
	secure, err := envBool("AUTH_COOKIE_SECURE", false)
	if err != nil {
		return Config{}, err
	}
	cfg.AuthCookieSecure = secure
	conflictLLMEnabled, err := envBool("CONFLICT_LLM_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cfg.ConflictLLMEnabled = conflictLLMEnabled
	switch cfg.RunnerType {
	case "process", "kubernetes":
	default:
		return Config{}, fmt.Errorf("RUNNER_TYPE %q: want process or kubernetes",
			cfg.RunnerType)
	}
	if cfg.RunnerType == "kubernetes" && cfg.TaskAttemptID == "" && cfg.RunnerImage == "" {
		return Config{}, fmt.Errorf("RUNNER_IMAGE is required for the kubernetes controller")
	}
	if cfg.TaskAttemptID != "" && !task.IsUUID(cfg.TaskAttemptID) {
		return Config{}, fmt.Errorf("TASK_ATTEMPT_ID must be a UUID")
	}
	ttl, err := envSeconds("RUNNER_TTL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	cfg.RunnerJobTTL = ttl
	maxTasks, err := envNonNegativeInt("WORKER_MAX_TASKS", 0)
	if err != nil {
		return Config{}, err
	}
	cfg.WorkerMaxTasks = maxTasks
	idleExit, err := envNonNegativeInt("WORKER_IDLE_EXIT_SECONDS", 0)
	if err != nil {
		return Config{}, err
	}
	cfg.WorkerIdleExit = time.Duration(idleExit) * time.Second
	switch cfg.AgentProvider {
	case "fake", "claude-code":
	default:
		return Config{}, fmt.Errorf("AGENT_PROVIDER %q: want fake or claude-code",
			cfg.AgentProvider)
	}
	switch cfg.ConflictLLMProvider {
	case "fake", "anthropic":
	default:
		return Config{}, fmt.Errorf("CONFLICT_LLM_PROVIDER %q: want fake or anthropic",
			cfg.ConflictLLMProvider)
	}
	switch cfg.AgentPermissionMode {
	case "default", "acceptEdits", "plan", "bypassPermissions":
	default:
		return Config{}, fmt.Errorf(
			"AGENT_PERMISSION_MODE %q: want default, acceptEdits, plan, or bypassPermissions",
			cfg.AgentPermissionMode)
	}

	level, err := parseLogLevel(envOr("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	for _, d := range []struct {
		dst      *time.Duration
		key      string
		fallback int
	}{
		{&cfg.RunnerLease, "RUNNER_LEASE_SECONDS", 60},
		{&cfg.RunnerHeartbeat, "RUNNER_HEARTBEAT_SECONDS", 10},
		{&cfg.RunnerLostAfter, "RUNNER_LOST_AFTER_SECONDS", 30},
		{&cfg.WorkerPoll, "WORKER_POLL_SECONDS", 2},
		{&cfg.DefaultTaskRuntime, "AGENT_TIMEOUT_SECONDS", 2700},
	} {
		secs, err := envSeconds(d.key, d.fallback)
		if err != nil {
			return Config{}, err
		}
		*d.dst = secs
	}
	if cfg.RunnerLostAfter <= cfg.RunnerHeartbeat {
		return Config{}, fmt.Errorf(
			"RUNNER_LOST_AFTER_SECONDS (%s) must exceed RUNNER_HEARTBEAT_SECONDS (%s)",
			cfg.RunnerLostAfter, cfg.RunnerHeartbeat)
	}
	return cfg, nil
}

func envNonNegativeInt(key string, fallback int) (int, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s %q: want an integer >= 0", key, raw)
	}
	return n, nil
}

func envSeconds(key string, fallback int) (time.Duration, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s %q: want a positive integer of seconds", key, raw)
	}
	return time.Duration(n) * time.Second, nil
}

func validateGitHub(cfg Config) error {
	set := 0
	for _, v := range []string{cfg.GitHubWebhookSecret, cfg.GitHubAppID,
		cfg.GitHubAppPrivateKeyPath} {
		if v != "" {
			set++
		}
	}
	if set != 0 && set != 3 {
		return fmt.Errorf("GITHUB_WEBHOOK_SECRET, GITHUB_APP_ID, and " +
			"GITHUB_APP_PRIVATE_KEY_PATH must be set together")
	}
	return nil
}

func validateAuth(cfg Config) error {
	if (cfg.GitHubOAuthClientID != "") != (cfg.GitHubOAuthClientSecret != "") {
		return fmt.Errorf("GITHUB_OAUTH_CLIENT_ID and GITHUB_OAUTH_CLIENT_SECRET " +
			"must be set together")
	}
	origin, err := url.Parse(cfg.AuthPublicOrigin)
	if err != nil || origin.Scheme == "" || origin.Host == "" ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return fmt.Errorf("AUTH_PUBLIC_ORIGIN %q: want scheme://host[:port] "+
			"with no path", cfg.AuthPublicOrigin)
	}
	return nil
}

func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("API_ADDR %q: %w", addr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("API_ADDR %q: port must be 0-65535", addr)
	}
	return nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s %q: want true or false", key, raw)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL %q: want debug, info, warn, or error", s)
	}
}
