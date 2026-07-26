package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// allowedRemote is the only remote a workspace may push to.
const allowedRemote = "origin"

// Push policy violations, refused in code before git is invoked
// (docs/architecture/git-workspaces.md, docs/architecture/command-execution.md).
var (
	ErrForbiddenBranch = errors.New("gitworkspace: refusing to push a branch outside agent-trail/")
	ErrForbiddenRemote = errors.New("gitworkspace: refusing to push to a remote other than origin")
	ErrForcePushDenied = errors.New("gitworkspace: force push is not allowed")
)

// PushParams describes a push of a workspace's working branch.
type PushParams struct {
	// Remote is the target remote; empty defaults to origin, and only origin
	// is allowed.
	Remote string
	// Force requests a non-fast-forward push. The guard always refuses it.
	Force bool
}

// Push publishes the workspace's working branch to origin. The policy is
// enforced in code, not by prompt: the branch must sit under agent-trail/, the
// remote must be origin, and force is refused - the command built never carries
// a leading "+" refspec or --force. The mirror flag is disabled for this
// invocation so an explicit refspec push cannot be reinterpreted as a
// mirror push that prunes upstream refs.
func (m *Manager) Push(ctx context.Context, w Workspace, p PushParams) error {
	remote := p.Remote
	if remote == "" {
		remote = allowedRemote
	}
	if remote != allowedRemote {
		return fmt.Errorf("%w: %q", ErrForbiddenRemote, remote)
	}
	if !validBranch(w.Branch) {
		return fmt.Errorf("%w: %q", ErrForbiddenBranch, w.Branch)
	}
	if p.Force {
		return ErrForcePushDenied
	}

	refspec := "refs/heads/" + w.Branch + ":refs/heads/" + w.Branch
	if _, err := m.git.run(ctx, w.Path,
		"-c", "remote."+remote+".mirror=false",
		"push", remote, refspec,
	); err != nil {
		return fmt.Errorf("gitworkspace: push: %w", err)
	}

	m.logger.LogAttrs(ctx, slog.LevelInfo, "working branch pushed",
		slog.String("event", "workspace_branch_pushed"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("task_attempt_id", w.AttemptID),
		slog.String("working_branch", w.Branch),
		slog.String("remote", remote),
	)
	return nil
}
