package main

import (
	"os"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
)

func TestSliceRunsEndToEnd(t *testing.T) {
	dbtest.Open(t)
	if err := run(os.Getenv("TEST_DATABASE_URL")); err != nil {
		t.Fatal(err)
	}
}
