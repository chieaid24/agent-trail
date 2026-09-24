package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

const allowedRemote = "origin"

var (
	ErrForbiddenBranch = errors.New("gitworkspace: refusing to push a branch outside agent-trail/")
	ErrForbiddenRemote = errors.New("gitworkspace: refusing to push to a remote other than origin")
	ErrForcePushDenied = errors.New("gitworkspace: force push is not allowed")
)

type PushParams struct {
	Remote string
	Force  bool // always refused
}

// policy in code, not prompt; mirror=false so a refspec push cannot become a mirror push that prunes upstream refs
func (m *Manager) Push(ctx context.Context, w Workspace, p PushParams) error {
	remote := p.Remote
	if remote == "" {
		remote = allowedRemote
	}
	if remote != allowedRemote {
		m.denials.Inc(observability.Label{Key: "policy", Value: "forbidden_remote"})
		return fmt.Errorf("%w: %q", ErrForbiddenRemote, remote)
	}
	if !ValidBranch(w.Branch) {
		m.denials.Inc(observability.Label{Key: "policy", Value: "forbidden_branch"})
		return fmt.Errorf("%w: %q", ErrForbiddenBranch, w.Branch)
	}
	if p.Force {
		m.denials.Inc(observability.Label{Key: "policy", Value: "force_push"})
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
