// argv only, never a shell; push policy enforced in code; fetch lock is process-local, so sharing a mirror cache across processes is unsafe
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

var ErrBaseSHANotFound = errors.New("gitworkspace: base commit not found")

// same shape the task_attempts.base_commit_sha check enforces
var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// single path component only: no traversal, no shell-special chars
var safeComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type RepoRef struct {
	ID string
	// may embed a credential: never logged, redacted from errors
	CloneURL string
}

type Workspace struct {
	AttemptID string
	Repo      RepoRef
	Path      string
	Branch    string
	BaseSHA   string
}

type Manager struct {
	root     string
	reposDir string
	workDir  string
	git      runner
	logger   *slog.Logger
	cleanups *observability.Counter
	denials  *observability.Counter

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// root must be absolute so worktree paths can be checked against it
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
			"Workspace cleanups, labelled by outcome (removed or failed)."),
		denials: metrics.Counter("agent_trail_policy_denials_total",
			"Push policy violations refused in code, labelled by policy."),
		locks: map[string]*sync.Mutex{},
	}
	for _, d := range []string{m.reposDir, m.workDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("gitworkspace: create %s: %w", d, err)
		}
	}
	return m, nil
}

// per-repo lock: two attempts never clone/fetch the same cache at once
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
		// stored remote url may embed an expired short-lived credential from an earlier clone
		if _, err := m.git.run(ctx, mirror, "remote", "set-url", "origin", "--", repo.CloneURL); err != nil {
			return "", fmt.Errorf("gitworkspace: refresh remote url: %w", err)
		}
		// agent-trail/* refs are born locally and are source of truth; fetching them back is refused while checked out
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
	// "--" so a hostile clone url starting with "-" is never a flag
	if _, err := m.git.run(ctx, "", "clone", "--mirror", "--", repo.CloneURL, mirror); err != nil {
		// drop half-written cache so next attempt reclones cleanly
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

func validComponent(s string) bool {
	return s != "." && s != ".." &&
		!strings.ContainsAny(s, `/\`) && safeComponent.MatchString(s)
}

func validSHA(s string) bool { return shaRe.MatchString(s) }
