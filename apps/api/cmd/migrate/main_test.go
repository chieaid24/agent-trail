package main

import (
	"os"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
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
// database is available (make integration-test); otherwise it skips. The
// dbtest harness serializes this against the other packages' DB tests.
func TestRunUpAgainstRealDatabase(t *testing.T) {
	dbtest.Open(t)
	t.Setenv("DATABASE_URL", os.Getenv("TEST_DATABASE_URL"))
	for _, cmd := range []string{"up", "status", "version"} {
		if err := run([]string{cmd}); err != nil {
			t.Fatalf("run(%s): %v", cmd, err)
		}
	}
}
