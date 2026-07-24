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
)

// Config holds the settings shared by the api, worker, and migrate commands.
type Config struct {
	// APIAddr is the listen address for the HTTP API, e.g. ":8080".
	APIAddr string
	// DatabaseURL is the PostgreSQL connection string. Optional for the api
	// skeleton (readiness reports it unconfigured); required by migrate.
	DatabaseURL string
	// LogLevel is the minimum level emitted by the structured logger.
	LogLevel slog.Level
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		APIAddr:     envOr("API_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	if err := validateAddr(cfg.APIAddr); err != nil {
		return Config{}, err
	}

	level, err := parseLogLevel(envOr("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level
	return cfg, nil
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
