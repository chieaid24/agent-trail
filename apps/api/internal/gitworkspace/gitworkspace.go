// Package gitworkspace provisions and cleans up isolated git worktrees for
// task attempts (docs/architecture/git-workspaces.md). It keeps a bare mirror
// cache per repository, cuts one worktree per attempt from a verified base
// commit, sanitizes branch names under the agent-trail/ prefix, enforces a
// push policy (agent-trail/* only, no force pushes) in code rather than by
// prompt, records commits with Agent-Trail trailers, and removes worktrees
// with git-aware cleanup that never prunes an active checkout.
//
// Every external git call goes through argument arrays; the package never
// spawns a shell, so no input is interpreted by one. Clone URLs may carry a
// credential, so they are never logged and are redacted out of error output.
//
// Security limitation: the fetch lock that serializes mirror clones and
// fetches is process-local. A single control-plane/runner process per host
// (the MVP shape) is safe; sharing one on-disk mirror cache across processes
// on the same host would need a cross-process file lock, deferred until a
// deployment needs it.
package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// ErrBaseSHANotFound reports that a requested base commit is absent from the
// repository mirror, so no worktree can be pinned to it.
var ErrBaseSHANotFound = errors.New("gitworkspace: base commit not found")

// shaRe matches a full lowercase-hex commit SHA, the same shape the
// task_attempts.base_commit_sha CHECK constraint enforces.
var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// safeComponent matches an identifier usable as a single filesystem path
// component: it forbids "/", "\", ".", "..", and any control or shell-special
// character, so a repository or attempt id cannot traverse out of its root.
var safeComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// RepoRef identifies a repository to mirror.
type RepoRef struct {
	// ID is the stable repository identifier. It names the on-disk mirror
	// directory, so it must be a safe path component.
	ID string
	// CloneURL is the mirror source. It may embed a credential; the package
	// never logs it and redacts it from errors.
	CloneURL string
}

// Workspace is a provisioned worktree for one task attempt.
type Workspace struct {
	AttemptID string
	Repo      RepoRef
	// Path is the worktree directory the agent reads and writes.
	Path string
	// Branch is the working branch, always under BranchPrefix.
	Branch string
	// BaseSHA is the commit the worktree was created at.
	BaseSHA string
}

// Manager owns the on-disk mirror cache and worktrees under a single root.
type Manager struct {
	root     string
	reposDir string
	workDir  string
	git      runner
	logger   *slog.Logger
	cleanups *observability.Counter

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-repository fetch/worktree lock
}

// New returns a Manager rooted at root (e.g. /var/lib/agent-trail), creating
// its repos/ and workspaces/ subdirectories. The root must be absolute so a
// worktree path can be checked against it.
func New(root string, logger *slog.Logger, metrics *observability.Registry) (*Manager, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("gitworkspace: root %q must be an absolute path", root)
	}
	root = filepath.Clean(root)
	m := &Manager{
		root:     root,
		reposDir: filepath.Join(root, "repos"),
		workDir:  filepath.Join(root, "workspaces"),
		logger:   logger,
		cleanups: metrics.Counter("agent_trail_workspace_cleanup_total",
			"Workspaces cleaned up."),
		locks: map[string]*sync.Mutex{},
	}
	for _, d := range []string{m.reposDir, m.workDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("gitworkspace: create %s: %w", d, err)
		}
	}
	return m, nil
}

// EnsureMirror clones repo into the bare mirror cache on first use and fetches
// updates on later calls, returning the mirror path. The per-repository lock
// serializes concurrent callers so two attempts never clone or fetch the same
// cache at once.
func (m *Manager) EnsureMirror(ctx context.Context, repo RepoRef) (string, error) {
	if !validComponent(repo.ID) {
		return "", fmt.Errorf("gitworkspace: repository id %q is not a safe path component", repo.ID)
	}
	if repo.CloneURL == "" {
		return "", errors.New("gitworkspace: repository clone url is empty")
	}
	mirror := filepath.Join(m.reposDir, repo.ID, "repo.git")

	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err == nil {
		// Refresh the stored remote URL first: it may embed a short-lived
		// credential recorded by an earlier clone, and a fetch or a later
		// push through an expired one fails.
		if _, err := m.git.run(ctx, mirror, "remote", "set-url", "origin", "--", repo.CloneURL); err != nil {
			return "", fmt.Errorf("gitworkspace: refresh remote url: %w", err)
		}
		// The agent-trail/* namespace is excluded: those branches are born
		// locally (worktree add) and pushed out, so the local refs are the
		// source of truth - and fetching one back would be refused anyway
		// while its worktree has it checked out.
		if _, err := m.git.run(ctx, mirror, "fetch", "--prune", "--refmap=", "origin",
			"+refs/*:refs/*", "^refs/heads/"+BranchPrefix+"*"); err != nil {
			return "", fmt.Errorf("gitworkspace: fetch mirror: %w", err)
		}
		if _, err := m.git.run(ctx, mirror, "fetch", "--prune", "--refmap=", "origin",
			"+refs/heads/"+BranchPrefix+"*:refs/remotes/origin/"+BranchPrefix+"*"); err != nil {
			return "", fmt.Errorf("gitworkspace: fetch agent branches: %w", err)
		}
		return mirror, nil
	}

	if err := os.MkdirAll(filepath.Dir(mirror), 0o750); err != nil {
		return "", fmt.Errorf("gitworkspace: create mirror dir: %w", err)
	}
	// "--" terminates options so a hostile clone URL beginning with "-" is
	// treated as a positional argument, never a flag.
	if _, err := m.git.run(ctx, "", "clone", "--mirror", "--", repo.CloneURL, mirror); err != nil {
		// Drop a half-written cache so the next attempt reclones cleanly.
		_ = os.RemoveAll(filepath.Dir(mirror))
		return "", fmt.Errorf("gitworkspace: clone mirror: %w", err)
	}
	m.logger.LogAttrs(ctx, slog.LevelInfo, "repository mirror ready",
		slog.String("event", "git_mirror_ready"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("repository_id", repo.ID),
	)
	return mirror, nil
}

// lockFor returns the mutex guarding one repository's mirror cache.
func (m *Manager) lockFor(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	return l
}

// validComponent reports whether s is safe as a single path component.
func validComponent(s string) bool {
	return s != "." && s != ".." &&
		!strings.ContainsAny(s, `/\`) && safeComponent.MatchString(s)
}

// validSHA reports whether s is a full lowercase-hex commit SHA.
func validSHA(s string) bool { return shaRe.MatchString(s) }
