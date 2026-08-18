package githubfixture

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestGitHandlerServesCloneAndPush: the smart-HTTP fixture supports the two
// operations the runner performs against origin - clone (mirror fetch) and
// branch push.
func TestGitHandlerServesCloneAndPush(t *testing.T) {
	dir := t.TempDir()
	origin, baseSHA, err := BuildOrigin(dir)
	if err != nil {
		t.Fatal(err)
	}

	handler, err := GitHandler(origin, "/git/")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	cloneURL := srv.URL + "/git/origin.git"

	work := filepath.Join(t.TempDir(), "clone")
	if _, err := gitIn(filepath.Dir(work), "clone", "-q", cloneURL, work); err != nil {
		t.Fatalf("clone over http: %v", err)
	}
	head, err := gitIn(work, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if head != baseSHA {
		t.Errorf("cloned HEAD = %s, want %s", head, baseSHA)
	}

	if err := os.WriteFile(filepath.Join(work, "runner.txt"), []byte("pushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"checkout", "-q", "-b", "agent-trail/test-branch"},
		{"add", "-A"},
		{"commit", "-q", "-m", "runner push"},
		{"push", "-q", "origin", "agent-trail/test-branch"},
	} {
		if _, err := gitIn(work, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	pushed, err := gitIn(origin, "rev-parse", "refs/heads/agent-trail/test-branch")
	if err != nil {
		t.Fatalf("pushed branch missing from origin: %v", err)
	}
	if pushed == "" {
		t.Error("pushed branch has no commit")
	}
}
