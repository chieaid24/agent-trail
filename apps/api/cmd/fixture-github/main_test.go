package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/githubfixture"
)

// allowlisted env so a pre-commit hook's GIT_DIR cannot leak in
func gitClone(url, dest string) (string, error) {
	cmd := exec.Command("git", "clone", "-q", url, dest)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git clone: %w: %s", err, out)
	}
	return string(out), nil
}

func TestFixtureSeedsAndServes(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	dir := t.TempDir()
	origin, _, err := githubfixture.BuildOrigin(dir)
	if err != nil {
		t.Fatal(err)
	}

	f, err := newFixture(db, origin, "http://fixture.invalid")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("healthz before seed = %d, want 503", resp.StatusCode)
	}

	if err := f.seed(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz after seed = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		PROpen     bool   `json:"pr_open"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.TaskID == "" {
		t.Error("verify returned no task id")
	}
	if got.TaskStatus != "queued" {
		t.Errorf("task_status = %q, want queued", got.TaskStatus)
	}
	if got.PROpen {
		t.Error("pr_open = true before any run")
	}

	clone := filepath.Join(t.TempDir(), "clone")
	if _, err := gitClone(srv.URL+"/git/origin.git", clone); err != nil {
		t.Fatalf("clone over /git/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "README.md")); err != nil {
		t.Errorf("cloned repository missing README.md: %v", err)
	}
}
