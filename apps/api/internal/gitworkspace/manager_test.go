package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// requireGit skips a test when the git binary is unavailable.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// runGit runs git for test fixtures with a hermetic env plus a fixed identity
// (Manager.Commit sets its own identity, but the fixture commits below do not).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(hardenedEnv(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	logger := observability.NewLogger(io.Discard, "test", slog.LevelError)
	m, err := New(t.TempDir(), logger, observability.NewRegistry())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// buildOrigin creates a bare repository with one commit and returns its path
// and that commit's SHA, to serve as a mirror source.
func buildOrigin(t *testing.T) (originPath, sha string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "initial")
	sha = runGit(t, src, "rev-parse", "HEAD")
	originPath = filepath.Join(dir, "origin.git")
	runGit(t, dir, "clone", "-q", "--bare", src, "origin.git")
	return originPath, sha
}

func TestWorkspaceLifecycle(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)
	origin, base := buildOrigin(t)
	repo := RepoRef{ID: "repo-1", CloneURL: origin}

	ws, err := m.CreateWorktree(ctx, CreateParams{
		Repo: repo, AttemptID: "attempt-1", BaseSHA: base, BranchLabel: "Fix Auth Bug",
	})
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if ws.Branch != "agent-trail/fix-auth-bug" {
		t.Fatalf("branch = %q, want agent-trail/fix-auth-bug", ws.Branch)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "README.md")); err != nil {
		t.Fatalf("worktree missing base file: %v", err)
	}

	// A clean worktree yields ErrNothingToCommit.
	if _, err := m.Commit(ctx, ws, CommitParams{Message: "noop", TaskID: "t"}); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("Commit(clean) err = %v, want ErrNothingToCommit", err)
	}

	if err := os.WriteFile(filepath.Join(ws.Path, "NEW.md"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, err := m.Commit(ctx, ws, CommitParams{
		Message: "add NEW.md", TaskID: "task-1", Provider: "fake", Model: "m-1", RequestedBy: "octocat",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !validSHA(sha) {
		t.Fatalf("commit sha = %q", sha)
	}

	body := runGit(t, ws.Path, "log", "-1", "--format=%B", sha)
	for _, want := range []string{
		"Agent-Trail-Task-ID: task-1",
		"Agent-Trail-Agent-Provider: fake",
		"Agent-Trail-Agent-Model: m-1",
		"Agent-Trail-Requested-By: octocat",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("commit body missing %q:\n%s", want, body)
		}
	}

	stats, err := m.DiffStats(ctx, ws)
	if err != nil {
		t.Fatalf("DiffStats: %v", err)
	}
	if want := (Stats{FilesChanged: 1, Insertions: 3, Deletions: 0}); stats != want {
		t.Fatalf("DiffStats = %+v, want %+v", stats, want)
	}

	if err := m.Push(ctx, ws, PushParams{}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := runGit(t, origin, "rev-parse", "refs/heads/"+ws.Branch); got != sha {
		t.Fatalf("origin %s = %q, want %q", ws.Branch, got, sha)
	}

	if err := m.Remove(ctx, ws); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree present after Remove: %v", err)
	}
}

func TestConcurrentWorkspacesIsolated(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)
	origin, base := buildOrigin(t)
	repo := RepoRef{ID: "repo-1", CloneURL: origin}

	const n = 4
	var wg sync.WaitGroup
	results := make([]Workspace, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = m.CreateWorktree(ctx, CreateParams{
				Repo:        repo,
				AttemptID:   fmt.Sprintf("attempt-%d", i),
				BaseSHA:     base,
				BranchLabel: fmt.Sprintf("slice-%d", i),
			})
		}(i)
	}
	wg.Wait()

	paths := map[string]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("attempt %d: %v", i, errs[i])
		}
		ws := results[i]
		if paths[ws.Path] {
			t.Fatalf("duplicate workspace path %q", ws.Path)
		}
		paths[ws.Path] = true
		// Each worktree is independently writable.
		if err := os.WriteFile(filepath.Join(ws.Path, "mine.txt"), []byte(ws.AttemptID), 0o644); err != nil {
			t.Fatalf("write in %s: %v", ws.Path, err)
		}
	}
	if len(paths) != n {
		t.Fatalf("got %d unique workspaces, want %d", len(paths), n)
	}
}

func TestPruneKeepsActiveWorktree(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)
	origin, base := buildOrigin(t)
	repo := RepoRef{ID: "repo-1", CloneURL: origin}

	ws, err := m.CreateWorktree(ctx, CreateParams{
		Repo: repo, AttemptID: "a1", BaseSHA: base, BranchLabel: "x",
	})
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if err := m.Prune(ctx, repo); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(ws.Path); err != nil {
		t.Fatalf("Prune removed an active worktree: %v", err)
	}
	list := runGit(t, filepath.Join(m.reposDir, repo.ID, "repo.git"), "worktree", "list")
	if !strings.Contains(list, ws.Path) {
		t.Fatalf("active worktree missing from list after Prune:\n%s", list)
	}
}

func TestRemoveKeepsSiblingWorktree(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)
	origin, base := buildOrigin(t)
	repo := RepoRef{ID: "repo-1", CloneURL: origin}

	keep, err := m.CreateWorktree(ctx, CreateParams{
		Repo: repo, AttemptID: "keep", BaseSHA: base, BranchLabel: "keep",
	})
	if err != nil {
		t.Fatalf("CreateWorktree keep: %v", err)
	}
	drop, err := m.CreateWorktree(ctx, CreateParams{
		Repo: repo, AttemptID: "drop", BaseSHA: base, BranchLabel: "drop",
	})
	if err != nil {
		t.Fatalf("CreateWorktree drop: %v", err)
	}

	// Removing one attempt (which runs worktree prune) must not touch a live
	// sibling checkout.
	if err := m.Remove(ctx, drop); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(drop.Path); !os.IsNotExist(err) {
		t.Fatalf("removed worktree still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(keep.Path, "README.md")); err != nil {
		t.Fatalf("Remove damaged the sibling worktree: %v", err)
	}
	list := runGit(t, filepath.Join(m.reposDir, repo.ID, "repo.git"), "worktree", "list")
	if !strings.Contains(list, keep.Path) {
		t.Fatalf("sibling missing from worktree list after a sibling Remove:\n%s", list)
	}
}

func TestNoShellInterpolationInCloneURL(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)

	// A clone URL laced with shell metacharacters. Under a shell this would
	// create the sentinel; with argument arrays git gets it as one literal
	// operand, fails to clone, and no command runs.
	sentinel := filepath.Join(t.TempDir(), "pwned")
	repo := RepoRef{
		ID:       "repo-1",
		CloneURL: "/no/such/repo$(touch " + sentinel + ");`touch " + sentinel + "`",
	}
	if _, err := m.EnsureMirror(ctx, repo); err == nil {
		t.Fatal("EnsureMirror accepted a bogus clone URL")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("shell metacharacters in the clone URL were executed")
	}
}

func TestCreateWorktreeRejectsUnknownBase(t *testing.T) {
	requireGit(t)
	m := newTestManager(t)
	origin, _ := buildOrigin(t)
	_, err := m.CreateWorktree(context.Background(), CreateParams{
		Repo:        RepoRef{ID: "repo-1", CloneURL: origin},
		AttemptID:   "attempt-x",
		BaseSHA:     "0123456789abcdef0123456789abcdef01234567",
		BranchLabel: "x",
	})
	if !errors.Is(err, ErrBaseSHANotFound) {
		t.Fatalf("err = %v, want ErrBaseSHANotFound", err)
	}
}

func TestCreateWorktreeRejectsUnsafeAttemptID(t *testing.T) {
	m := newTestManager(t)
	repo := RepoRef{ID: "repo-1", CloneURL: "file:///unused"}
	for _, bad := range []string{"../evil", "a/b", "..", ""} {
		_, err := m.CreateWorktree(context.Background(), CreateParams{
			Repo:        repo,
			AttemptID:   bad,
			BaseSHA:     "0123456789abcdef0123456789abcdef01234567",
			BranchLabel: "x",
		})
		if err == nil {
			t.Fatalf("attempt id %q was accepted", bad)
		}
	}
}

func TestPushGuards(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	ok := Workspace{AttemptID: "a", Repo: RepoRef{ID: "r"}, Path: "/nonexistent", Branch: "agent-trail/ok"}

	// Guards reject before git runs, so no real remote is needed.
	if err := m.Push(ctx, Workspace{Branch: "main", Path: "/nonexistent"}, PushParams{}); !errors.Is(err, ErrForbiddenBranch) {
		t.Fatalf("protected branch err = %v, want ErrForbiddenBranch", err)
	}
	if err := m.Push(ctx, ok, PushParams{Remote: "upstream"}); !errors.Is(err, ErrForbiddenRemote) {
		t.Fatalf("foreign remote err = %v, want ErrForbiddenRemote", err)
	}
	if err := m.Push(ctx, ok, PushParams{Force: true}); !errors.Is(err, ErrForcePushDenied) {
		t.Fatalf("force err = %v, want ErrForcePushDenied", err)
	}
}

func TestWorkspaceContainsRejectsSymlinkEscape(t *testing.T) {
	requireGit(t)
	m := newTestManager(t)
	origin, base := buildOrigin(t)
	ws, err := m.CreateWorktree(context.Background(), CreateParams{
		Repo: RepoRef{ID: "repo-1", CloneURL: origin}, AttemptID: "a1", BaseSHA: base, BranchLabel: "x",
	})
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// A path within the worktree is contained, even before it exists.
	if ok, err := ws.Contains(filepath.Join(ws.Path, "sub", "file.txt")); err != nil || !ok {
		t.Fatalf("Contains(inside) = %v, %v; want true, nil", ok, err)
	}

	// A symlink escaping the worktree is not contained.
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(ws.Path, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if ok, err := ws.Contains(filepath.Join(ws.Path, "escape", "loot.txt")); err != nil || ok {
		t.Fatalf("Contains(escape) = %v, %v; want false, nil", ok, err)
	}
}

func TestHeadAndLookup(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)
	origin, base := buildOrigin(t)
	repo := RepoRef{ID: "repo-head", CloneURL: origin}

	w, err := m.CreateWorktree(ctx, CreateParams{
		Repo: repo, AttemptID: "attempt-head", BaseSHA: base, BranchLabel: "head test",
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := m.Head(ctx, w)
	if err != nil || head != base {
		t.Fatalf("Head = %q, %v; want %q", head, err, base)
	}

	got, ok := m.Lookup("attempt-head", repo, w.Branch, base)
	if !ok || got.Path != w.Path || got.Branch != w.Branch {
		t.Fatalf("Lookup = %+v, %v", got, ok)
	}
	if _, ok := m.Lookup("attempt-gone", repo, w.Branch, base); ok {
		t.Fatal("Lookup found a workspace that does not exist")
	}

	// After a commit, Head moves past the base.
	if err := os.WriteFile(filepath.Join(w.Path, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, err := m.Commit(ctx, w, CommitParams{Message: "change", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	head, err = m.Head(ctx, w)
	if err != nil || head != sha || head == base {
		t.Fatalf("Head after commit = %q, %v; want %q", head, err, sha)
	}
}
