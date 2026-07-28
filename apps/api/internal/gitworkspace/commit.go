package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// Commit identity for Agent-Trail-authored commits. A noreply address keeps the
// bot out of contributor graphs and never exposes a real mailbox.
const (
	commitAuthorName  = "Agent Trail"
	commitAuthorEmail = "agent-trail[bot]@users.noreply.github.com"
)

// ErrNothingToCommit reports that the worktree has no staged or unstaged
// changes, so there is nothing to record (the no-change task outcome).
var ErrNothingToCommit = errors.New("gitworkspace: nothing to commit")

// CommitParams describes a commit to record in a workspace. The trailer fields
// are provenance identifiers only - never a prompt or a secret
// (docs/architecture/git-workspaces.md).
type CommitParams struct {
	Message     string
	TaskID      string
	Provider    string
	Model       string
	RequestedBy string
}

// Stats summarizes the change a worktree introduces over its base commit.
type Stats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// Commit stages every change in the worktree and records one commit carrying
// the Agent-Trail provenance trailers, under a fixed bot identity set per
// invocation with -c (never mutating repository or global config). It returns
// the final commit SHA, or ErrNothingToCommit when the worktree is clean.
func (m *Manager) Commit(ctx context.Context, w Workspace, p CommitParams) (string, error) {
	if strings.TrimSpace(p.Message) == "" {
		return "", errors.New("gitworkspace: commit message is empty")
	}
	trailers, err := buildTrailers(p)
	if err != nil {
		return "", err
	}

	if _, err := m.git.run(ctx, w.Path, "add", "-A"); err != nil {
		return "", fmt.Errorf("gitworkspace: stage changes: %w", err)
	}
	status, err := m.git.run(ctx, w.Path, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("gitworkspace: read status: %w", err)
	}
	if status == "" {
		return "", ErrNothingToCommit
	}

	// Two -m values join with a blank line, so the trailer block sits alone at
	// the end of the message where git recognizes it as trailers.
	if _, err := m.git.run(ctx, w.Path,
		"-c", "user.name="+commitAuthorName,
		"-c", "user.email="+commitAuthorEmail,
		"commit", "-m", p.Message, "-m", trailers,
	); err != nil {
		return "", fmt.Errorf("gitworkspace: commit: %w", err)
	}
	sha, err := m.git.run(ctx, w.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitworkspace: read commit sha: %w", err)
	}

	m.logger.LogAttrs(ctx, slog.LevelInfo, "commit recorded",
		slog.String("event", "workspace_commit_recorded"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("task_attempt_id", w.AttemptID),
		slog.String("final_commit_sha", sha),
	)
	return sha, nil
}

// Head returns the worktree's current HEAD commit SHA. Publishing uses it to
// tell a recovered already-committed worktree (HEAD moved past BaseSHA) from
// a true no-change outcome (HEAD still at BaseSHA, tree clean).
func (m *Manager) Head(ctx context.Context, w Workspace) (string, error) {
	sha, err := m.git.run(ctx, w.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitworkspace: read head: %w", err)
	}
	return sha, nil
}

// DiffStats compares the worktree's HEAD to its base commit and returns the
// change counts. A binary file counts toward FilesChanged with zero line
// deltas, matching git's numstat output.
func (m *Manager) DiffStats(ctx context.Context, w Workspace) (Stats, error) {
	out, err := m.git.run(ctx, w.Path, "diff", "--numstat", w.BaseSHA, "HEAD")
	if err != nil {
		return Stats{}, fmt.Errorf("gitworkspace: diff stats: %w", err)
	}
	return parseNumstat(out)
}

// buildTrailers renders the non-empty provenance fields as a trailer block. A
// newline in any value is rejected so a value cannot forge extra trailers or
// message lines.
func buildTrailers(p CommitParams) (string, error) {
	fields := []struct{ key, val string }{
		{"Agent-Trail-Task-ID", p.TaskID},
		{"Agent-Trail-Agent-Provider", p.Provider},
		{"Agent-Trail-Agent-Model", p.Model},
		{"Agent-Trail-Requested-By", p.RequestedBy},
	}
	var b strings.Builder
	for _, f := range fields {
		if f.val == "" {
			continue
		}
		if strings.ContainsAny(f.val, "\r\n") {
			return "", fmt.Errorf("gitworkspace: trailer %s must not contain a newline", f.key)
		}
		fmt.Fprintf(&b, "%s: %s\n", f.key, f.val)
	}
	if b.Len() == 0 {
		return "", errors.New("gitworkspace: at least one commit trailer is required")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseNumstat sums git diff --numstat output into Stats.
func parseNumstat(out string) (Stats, error) {
	var s Stats
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			return Stats{}, fmt.Errorf("gitworkspace: malformed numstat line %q", line)
		}
		s.FilesChanged++
		if fields[0] == "-" && fields[1] == "-" {
			continue // binary file: no line counts
		}
		add, err := strconv.Atoi(fields[0])
		if err != nil {
			return Stats{}, fmt.Errorf("gitworkspace: numstat insertions %q: %w", fields[0], err)
		}
		del, err := strconv.Atoi(fields[1])
		if err != nil {
			return Stats{}, fmt.Errorf("gitworkspace: numstat deletions %q: %w", fields[1], err)
		}
		s.Insertions += add
		s.Deletions += del
	}
	return s, nil
}
