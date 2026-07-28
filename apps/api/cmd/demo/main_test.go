package main

import (
	"os"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
)

// TestDemoRunsEndToEnd drives the full scripted demo against the test
// database. dbtest.Open holds the shared advisory lock, migrates, and
// truncates; it skips without TEST_DATABASE_URL.
func TestDemoRunsEndToEnd(t *testing.T) {
	dbtest.Open(t)
	if err := run(os.Getenv("TEST_DATABASE_URL")); err != nil {
		t.Fatal(err)
	}
}
