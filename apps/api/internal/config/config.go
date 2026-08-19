// Package config loads and validates control-plane configuration from the
// environment. Fail fast: a process with bad configuration must not start.
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
)

// Config holds the settings shared by the api, worker, and migrate commands.
type Config struct {
	// APIAddr is the listen address for the HTTP API, e.g. ":8080".
	APIAddr string
	// DatabaseURL is the PostgreSQL connection string. Optional for the api
	// skeleton (readiness reports it unconfigured); required by migrate and
	// the worker.
	DatabaseURL string
	// LogLevel is the minimum level emitted by the structured logger.
	LogLevel slog.Level
	// RunnerLease is how long a claimed task attempt stays owned without an
	// extension (RUNNER_LEASE_SECONDS).
	RunnerLease time.Duration
	// RunnerHeartbeat is the runner registry heartbeat interval
	// (RUNNER_HEARTBEAT_SECONDS).
	RunnerHeartbeat time.Duration
	// RunnerLostAfter is how stale a heartbeat marks a runner lost
	// (RUNNER_LOST_AFTER_SECONDS). Must exceed RunnerHeartbeat.
	RunnerLostAfter time.Duration
	// WorkerPoll is the idle claim-poll interval (WORKER_POLL_SECONDS).
	WorkerPoll time.Duration
	// RunnerType identifies how this worker is hosted in the runner
	// registry: process, docker, or kubernetes (RUNNER_TYPE).
	RunnerType string
	// WorkerMaxTasks caps attempts executed before the worker exits; zero
	// runs forever (WORKER_MAX_TASKS). A Kubernetes Job runner sets 1 so
	// the Job completes and TTL cleanup applies.
	WorkerMaxTasks int
	// WorkerIdleExit stops the worker when no claim arrives for this long;
	// zero never idles out (WORKER_IDLE_EXIT_SECONDS).
	WorkerIdleExit time.Duration
	// WorkspaceRoot is the base directory for the git mirror cache and task
	// worktrees (WORKSPACE_ROOT); must be an absolute path.
	WorkspaceRoot string
	// GitHub App integration; all three set together, or none (the webhook
	// endpoint then answers 503). GitHubAPIBaseURL overrides the API root
	// in tests only.
	GitHubWebhookSecret     string
	GitHubAppID             string
	GitHubAppPrivateKeyPath string
	GitHubAPIBaseURL        string
	// GitHub OAuth user authorization backing dashboard sessions; both set
	// together, or neither (the auth endpoints then answer 503 and the API
	// stays open for localhost development). GitHubOAuthBaseURL overrides
	// the github.com root for tests and GitHub Enterprise
	// (docs/adr/0013-dashboard-sessions.md).
	GitHubOAuthClientID     string
	GitHubOAuthClientSecret string
	GitHubOAuthBaseURL      string
	// GitHubAppSlug builds the GitHub App installation link the dashboard
	// shows (GITHUB_APP_SLUG); optional.
	GitHubAppSlug string
	// AuthPublicOrigin is the browser-facing dashboard origin
	// (AUTH_PUBLIC_ORIGIN). The OAuth redirect URI and post-login redirects
	// derive from it; the API is reached through its /backend proxy
	// (apps/web/next.config.ts).
	AuthPublicOrigin string
	// AuthCookieSecure marks auth cookies Secure (AUTH_COOKIE_SECURE,
	// default false for plain-HTTP localhost development).
	AuthCookieSecure bool
	// AgentProvider selects the agent adapter: "fake" (default) or
	// "claude-code" (AGENT_PROVIDER). The remaining Agent* settings apply only
	// to the Claude Code CLI adapter (docs/architecture/agent-providers.md).
	AgentProvider string
	// AgentCLIPath is the Claude Code executable, resolved from PATH when bare
	// (AGENT_CLI_PATH, default "claude").
	AgentCLIPath string
	// AgentModel is the provider model; empty uses the CLI default (AGENT_MODEL).
	AgentModel string
	// AgentPermissionMode is the Claude Code permission mode
	// (AGENT_PERMISSION_MODE, default "acceptEdits").
	AgentPermissionMode string
	// AgentCLIVersion, when set, pins the CLI version: it must appear in
	// `claude --version` or the worker refuses to start (AGENT_CLI_VERSION).
	AgentCLIVersion string
	// AgentTimeout is the hard per-attempt agent runtime cap
	// (AGENT_TIMEOUT_SECONDS).
	AgentTimeout time.Duration
	// OTLPEndpoint is the plaintext OTLP/gRPC target; "off" disables export.
	OTLPEndpoint string
}

// GitHubEnabled reports whether the GitHub App integration is configured.
func (c Config) GitHubEnabled() bool { return c.GitHubWebhookSecret != "" }

// AuthEnabled reports whether the dashboard session layer is configured.
func (c Config) AuthEnabled() bool { return c.GitHubOAuthClientID != "" }

// Load reads configuration from the environment and validates it.
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
		AgentProvider:           envOr("AGENT_PROVIDER", "fake"),
		AgentCLIPath:            envOr("AGENT_CLI_PATH", "claude"),
		AgentModel:              os.Getenv("AGENT_MODEL"),
		AgentPermissionMode:     envOr("AGENT_PERMISSION_MODE", "acceptEdits"),
		AgentCLIVersion:         os.Getenv("AGENT_CLI_VERSION"),
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
	switch cfg.RunnerType {
	case "process", "docker", "kubernetes":
	default:
		return Config{}, fmt.Errorf("RUNNER_TYPE %q: want process, docker, or kubernetes",
			cfg.RunnerType)
	}
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
		{&cfg.AgentTimeout, "AGENT_TIMEOUT_SECONDS", 2700},
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

// envNonNegativeInt reads an integer >= 0 from the environment.
func envNonNegativeInt(key string, fallback int) (int, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s %q: want an integer >= 0", key, raw)
	}
	return n, nil
}

// envSeconds reads a positive whole-second duration from the environment.
func envSeconds(key string, fallback int) (time.Duration, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s %q: want a positive integer of seconds", key, raw)
	}
	return time.Duration(n) * time.Second, nil
}

// validateGitHub rejects a partial GitHub configuration: a webhook that can
// never act, or credentials without a webhook, is a deployment mistake.
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

// validateAuth rejects a partial OAuth configuration and a malformed
// dashboard origin: a login that can never complete is a deployment mistake.
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

// validateAddr accepts host:port with a numeric port (host may be empty).
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

// envBool reads a strict true/false from the environment.
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
