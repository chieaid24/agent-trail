package main

import (
	"os"
	"strings"
	"testing"
)

func TestRunRejectsBadArgs(t *testing.T) {
	for _, args := range [][]string{{}, {"up", "extra"}, {"sideways"}} {
		if err := run(args); err == nil {
			t.Errorf("run(%v) succeeded, want error", args)
		}
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := run([]string{"up"})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want DATABASE_URL error", err)
	}
}

// TestRunUpAgainstRealDatabase exercises the full migration path when a test
// database is available (make integration-test); otherwise it skips.
func TestRunUpAgainstRealDatabase(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv("DATABASE_URL", url)
	for _, cmd := range []string{"up", "status", "version"} {
		if err := run([]string{cmd}); err != nil {
			t.Fatalf("run(%s): %v", cmd, err)
		}
	}
}
