// Package config loads and validates control-plane configuration from the
// environment. Fail fast: a process with bad configuration must not start.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
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
	// GitHub App integration; all three set together, or none (the webhook
	// endpoint then answers 503). GitHubAPIBaseURL overrides the API root
	// in tests only.
	GitHubWebhookSecret     string
	GitHubAppID             string
	GitHubAppPrivateKeyPath string
	GitHubAPIBaseURL        string
}

// GitHubEnabled reports whether the GitHub App integration is configured.
func (c Config) GitHubEnabled() bool { return c.GitHubWebhookSecret != "" }

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		APIAddr:                 envOr("API_ADDR", ":8080"),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		GitHubWebhookSecret:     os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubAppID:             os.Getenv("GITHUB_APP_ID"),
		GitHubAppPrivateKeyPath: os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"),
		GitHubAPIBaseURL:        os.Getenv("GITHUB_API_BASE_URL"),
	}

	if err := validateAddr(cfg.APIAddr); err != nil {
		return Config{}, err
	}
	if err := validateGitHub(cfg); err != nil {
		return Config{}, err
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
