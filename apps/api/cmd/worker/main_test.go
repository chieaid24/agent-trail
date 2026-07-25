package main

import (
	"strings"
	"testing"
)

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("run() = %v, want DATABASE_URL error", err)
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	t.Setenv("RUNNER_LOST_AFTER_SECONDS", "5")
	t.Setenv("RUNNER_HEARTBEAT_SECONDS", "10")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "RUNNER_LOST_AFTER_SECONDS") {
		t.Fatalf("run() = %v, want lost-after validation error", err)
	}
}
