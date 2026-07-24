package config

import (
	"log/slog"
	"testing"
)

// clearEnv makes the test hermetic against ambient configuration.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"API_ADDR", "DATABASE_URL", "LOG_LEVEL"} {
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
	clearEnv(t)
	t.Setenv("API_ADDR", "8080")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted API_ADDR without a colon")
	}
}

func TestLoadRejectsBadLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "loud")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted invalid LOG_LEVEL")
	}
}
