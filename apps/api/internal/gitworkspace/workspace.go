package gitworkspace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// CreateParams describes a worktree to provision for one task attempt.
type CreateParams struct {
	Repo RepoRef
	// AttemptID names the worktree directory; it must be a safe path component.
	AttemptID string
	// BaseSHA is the full lowercase-hex commit the worktree is pinned to; it is
	// verified to exist in the mirror before checkout.
	BaseSHA string
	// BranchLabel seeds the working branch and is sanitized under BranchPrefix.
	BranchLabel string
}

// CreateWorktree ensures the repository mirror is present and current, verifies
// the base commit exists, and cuts a fresh worktree on a sanitized working
// branch pinned to that commit. Two attempts on the same repository get
// isolated worktree directories and distinct branches.
func (m *Manager) CreateWorktree(ctx context.Context, p CreateParams) (Workspace, error) {
	if !validComponent(p.AttemptID) {
		return Workspace{}, fmt.Errorf("gitworkspace: attempt id %q is not a safe path component", p.AttemptID)
	}
	if !validSHA(p.BaseSHA) {
		return Workspace{}, fmt.Errorf("gitworkspace: base sha %q must be a 40-character lowercase hex commit", p.BaseSHA)
	}
	branch, err := SanitizeBranch(p.BranchLabel)
	if err != nil {
		return Workspace{}, err
	}

	mirror, err := m.EnsureMirror(ctx, p.Repo)
	if err != nil {
		return Workspace{}, err
	}

	lock := m.lockFor(p.Repo.ID)
	lock.Lock()
	defer lock.Unlock()

	if err := m.verifyBaseSHA(ctx, mirror, p.BaseSHA); err != nil {
		return Workspace{}, err
	}

	path := filepath.Join(m.workDir, p.AttemptID)
	// Defence in depth: the component regex already forbids traversal, but a
	// path whose parent is not the workspace root must never be checked out.
	if filepath.Dir(path) != m.workDir {
		return Workspace{}, fmt.Errorf("gitworkspace: worktree path for %q escapes the workspace root", p.AttemptID)
	}
	if _, err := os.Stat(path); err == nil {
		return Workspace{}, fmt.Errorf("gitworkspace: workspace %q already exists", p.AttemptID)
	}

	if _, err := m.git.run(ctx, mirror, "worktree", "add", "-b", branch, path, p.BaseSHA); err != nil {
		return Workspace{}, fmt.Errorf("gitworkspace: add worktree: %w", err)
	}
	// A symlinked workspace root could still let the created path resolve
	// outside root; unwind the worktree if it does.
	if err := m.assertWithinRoot(path); err != nil {
		_, _ = m.git.run(ctx, mirror, "worktree", "remove", "--force", path)
		return Workspace{}, err
	}

	m.logger.LogAttrs(ctx, slog.LevelInfo, "workspace created",
		slog.String("event", "workspace_created"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("repository_id", p.Repo.ID),
		slog.String("task_attempt_id", p.AttemptID),
		slog.String("working_branch", branch),
		slog.String("base_commit_sha", p.BaseSHA),
	)
	return Workspace{
		AttemptID: p.AttemptID,
		Repo:      p.Repo,
		Path:      path,
		Branch:    branch,
		BaseSHA:   p.BaseSHA,
	}, nil
}

// verifyBaseSHA confirms sha names an existing commit in the mirror and
// resolves to exactly that object (never a prefix or a different ref).
func (m *Manager) verifyBaseSHA(ctx context.Context, mirror, sha string) error {
	out, err := m.git.run(ctx, mirror, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil || out != sha {
		return fmt.Errorf("gitworkspace: %s: %w", sha, ErrBaseSHANotFound)
	}
	return nil
}

// Remove tears down a completed attempt's worktree and deletes its working
// branch, then prunes stale administrative entries. It force-removes the
// worktree: a finished attempt's committed work is already pushed, so leftover
// build artifacts must not block cleanup. worktree prune only clears entries
// whose directories are already gone, so it never touches an active checkout.
func (m *Manager) Remove(ctx context.Context, w Workspace) error {
	mirror := filepath.Join(m.reposDir, w.Repo.ID, "repo.git")

	lock := m.lockFor(w.Repo.ID)
	lock.Lock()
	defer lock.Unlock()

	if _, err := m.git.run(ctx, mirror, "worktree", "remove", "--force", w.Path); err != nil {
		return fmt.Errorf("gitworkspace: remove worktree: %w", err)
	}
	// Idempotent: ignore a missing branch so a retried cleanup still succeeds.
	_, _ = m.git.run(ctx, mirror, "branch", "-D", w.Branch)
	if _, err := m.git.run(ctx, mirror, "worktree", "prune"); err != nil {
		return fmt.Errorf("gitworkspace: prune worktrees: %w", err)
	}

	m.cleanups.Inc()
	m.logger.LogAttrs(ctx, slog.LevelInfo, "workspace removed",
		slog.String("event", "workspace_removed"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("repository_id", w.Repo.ID),
		slog.String("task_attempt_id", w.AttemptID),
		slog.String("working_branch", w.Branch),
	)
	return nil
}

// Prune clears administrative entries for worktrees whose directories are gone.
// It never removes a live checkout, so it is safe to run opportunistically.
func (m *Manager) Prune(ctx context.Context, repo RepoRef) error {
	if !validComponent(repo.ID) {
		return fmt.Errorf("gitworkspace: repository id %q is not a safe path component", repo.ID)
	}
	mirror := filepath.Join(m.reposDir, repo.ID, "repo.git")
	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := m.git.run(ctx, mirror, "worktree", "prune"); err != nil {
		return fmt.Errorf("gitworkspace: prune worktrees: %w", err)
	}
	return nil
}

// Contains reports whether target, after symlink resolution, lies inside the
// workspace. A caller enforcing a filesystem boundary uses it to reject a path
// that escapes the worktree through a symlink planted in the repository.
func (w Workspace) Contains(target string) (bool, error) {
	base, err := filepath.EvalSymlinks(w.Path)
	if err != nil {
		return false, fmt.Errorf("gitworkspace: resolve workspace: %w", err)
	}
	resolved, err := resolveExisting(target)
	if err != nil {
		return false, err
	}
	return resolved == base || strings.HasPrefix(resolved, base+string(os.PathSeparator)), nil
}

// assertWithinRoot fails if path resolves (through any symlink) to a location
// outside the workspace root.
func (m *Manager) assertWithinRoot(path string) error {
	root, err := filepath.EvalSymlinks(m.workDir)
	if err != nil {
		return fmt.Errorf("gitworkspace: resolve workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("gitworkspace: resolve workspace path: %w", err)
	}
	if real != root && !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return fmt.Errorf("gitworkspace: workspace path %q resolves outside the workspace root", path)
	}
	return nil
}

// resolveExisting resolves the symlinks of target's deepest existing ancestor
// and rejoins the not-yet-created remainder, so a boundary check still sees a
// symlinked parent even when target itself does not exist.
func resolveExisting(target string) (string, error) {
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("gitworkspace: target %q must be an absolute path", target)
	}
	cur := filepath.Clean(target)
	remainder := ""
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, remainder), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("gitworkspace: cannot resolve %q", target)
		}
		remainder = filepath.Join(filepath.Base(cur), remainder)
		cur = parent
	}
}
