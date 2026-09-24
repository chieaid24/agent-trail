package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

type CreateParams struct {
	Repo        RepoRef
	AttemptID   string
	BaseSHA     string
	BranchLabel string
}

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
	// defence in depth: regex already forbids traversal, but parent must be the workspace root
	if filepath.Dir(path) != m.workDir {
		return Workspace{}, fmt.Errorf("gitworkspace: worktree path for %q escapes the workspace root", p.AttemptID)
	}
	if _, err := os.Stat(path); err == nil {
		return Workspace{}, fmt.Errorf("gitworkspace: workspace %q already exists", p.AttemptID)
	}

	if _, err := m.git.run(ctx, mirror, "worktree", "add", "-b", branch, path, p.BaseSHA); err != nil {
		return Workspace{}, fmt.Errorf("gitworkspace: add worktree: %w", err)
	}
	// symlinked root could still resolve the path outside; unwind the worktree if so
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

// git steps best-effort; dir removal + prune report failure so a fresh CreateWorktree can succeed
func (m *Manager) CleanupStale(ctx context.Context, repo RepoRef, attemptID, branch string) error {
	if !validComponent(repo.ID) {
		return fmt.Errorf("gitworkspace: repository id %q is not a safe path component", repo.ID)
	}
	if !validComponent(attemptID) {
		return fmt.Errorf("gitworkspace: attempt id %q is not a safe path component", attemptID)
	}
	mirror := filepath.Join(m.reposDir, repo.ID, "repo.git")
	path := filepath.Join(m.workDir, attemptID)

	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	_, _ = m.git.run(ctx, mirror, "worktree", "remove", "--force", path)
	removeErr := os.RemoveAll(path)
	if ValidBranch(branch) {
		_, _ = m.git.run(ctx, mirror, "branch", "-D", branch)
	}
	_, pruneErr := m.git.run(ctx, mirror, "worktree", "prune")
	if removeErr != nil || pruneErr != nil {
		m.cleanups.Inc(observability.Label{Key: "outcome", Value: "failed"})
		var err error
		if removeErr != nil {
			err = fmt.Errorf("gitworkspace: remove stale workspace: %w", removeErr)
		}
		if pruneErr != nil {
			err = errors.Join(err, fmt.Errorf("gitworkspace: prune worktrees: %w", pruneErr))
		}
		return err
	}
	m.cleanups.Inc(observability.Label{Key: "outcome", Value: "removed"})
	return nil
}

func (m *Manager) WorkspaceExists(attemptID string) bool {
	if !validComponent(attemptID) {
		return false
	}
	_, err := os.Lstat(filepath.Join(m.workDir, attemptID))
	return err == nil
}

func (m *Manager) Lookup(attemptID string, repo RepoRef, branch, baseSHA string) (Workspace, bool) {
	if !validComponent(attemptID) {
		return Workspace{}, false
	}
	path := filepath.Join(m.workDir, attemptID)
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return Workspace{}, false
	}
	return Workspace{
		AttemptID: attemptID,
		Repo:      repo,
		Path:      path,
		Branch:    branch,
		BaseSHA:   baseSHA,
	}, true
}

// must resolve to exactly that object, never a prefix or another ref
func (m *Manager) verifyBaseSHA(ctx context.Context, mirror, sha string) error {
	out, err := m.git.run(ctx, mirror, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil || out != sha {
		return fmt.Errorf("gitworkspace: %s: %w", sha, ErrBaseSHANotFound)
	}
	return nil
}

// force-remove: committed work already pushed; prune only clears dead entries, never an active checkout
func (m *Manager) Remove(ctx context.Context, w Workspace) error {
	mirror := filepath.Join(m.reposDir, w.Repo.ID, "repo.git")

	lock := m.lockFor(w.Repo.ID)
	lock.Lock()
	defer lock.Unlock()

	if _, err := m.git.run(ctx, mirror, "worktree", "remove", "--force", w.Path); err != nil {
		m.cleanups.Inc(observability.Label{Key: "outcome", Value: "failed"})
		return fmt.Errorf("gitworkspace: remove worktree: %w", err)
	}
	_, _ = m.git.run(ctx, mirror, "branch", "-D", w.Branch)
	if _, err := m.git.run(ctx, mirror, "worktree", "prune"); err != nil {
		m.cleanups.Inc(observability.Label{Key: "outcome", Value: "failed"})
		return fmt.Errorf("gitworkspace: prune worktrees: %w", err)
	}

	m.cleanups.Inc(observability.Label{Key: "outcome", Value: "removed"})
	m.logger.LogAttrs(ctx, slog.LevelInfo, "workspace removed",
		slog.String("event", "workspace_removed"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("repository_id", w.Repo.ID),
		slog.String("task_attempt_id", w.AttemptID),
		slog.String("working_branch", w.Branch),
	)
	return nil
}

// never removes a live checkout; safe to run opportunistically
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

// rejects paths escaping the worktree through a repo-planted symlink
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

// resolve deepest existing ancestor + rejoin remainder: boundary check must see symlinked parents of missing targets
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
