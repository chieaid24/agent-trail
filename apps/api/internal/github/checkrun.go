package github

import (
	"context"
	"log/slog"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type TriggerCheckRunTasks interface {
	Attempts(ctx context.Context, taskID string) ([]task.Attempt, error)
	MarkTriggerCheckRunCompleted(ctx context.Context, attemptID string) error
	AppendAttemptEvent(ctx context.Context, attemptID, eventType, source string, payload any) error
}

type CheckRunAPI interface {
	UpdateCheckRun(ctx context.Context, installationID int64, owner, repo string, checkRunID int64, p CheckRunParams) error
}

// completes the queued trigger check run of a task cancelled before any runner claimed it;
// a claimed task's runner settles its own, and the completion marker keeps the two from clashing
type TriggerCheckRuns struct {
	store  *Store
	tasks  TriggerCheckRunTasks
	api    CheckRunAPI
	logger *slog.Logger
}

func NewTriggerCheckRuns(store *Store, tasks TriggerCheckRunTasks, api CheckRunAPI, logger *slog.Logger) *TriggerCheckRuns {
	return &TriggerCheckRuns{store: store, tasks: tasks, api: api, logger: logger}
}

// best effort: the cancellation is already durable, so every failure here only logs
func (t *TriggerCheckRuns) TaskCancelled(ctx context.Context, cancelled task.Task) {
	if cancelled.RepositoryID == nil || cancelled.Status != task.StatusCancelled {
		return
	}
	warn := func(msg string, err error) {
		t.logger.LogAttrs(ctx, slog.LevelWarn, msg,
			slog.String("event", "github_trigger_check_run_failed"),
			slog.String("trace_id", observability.TraceIDFrom(ctx)),
			slog.String("task_id", cancelled.ID),
			slog.String("error", err.Error()),
		)
	}
	attempts, err := t.tasks.Attempts(ctx, cancelled.ID)
	if err != nil {
		warn("trigger check run attempt lookup failed", err)
		return
	}
	if len(attempts) == 0 {
		return
	}
	last := attempts[len(attempts)-1]
	if last.TriggerCheckRunID == nil || last.TriggerCheckRunCompletedAt != nil {
		return
	}
	rc, err := t.store.RepositoryContextByID(ctx, *cancelled.RepositoryID)
	if err != nil {
		warn("trigger check run repository lookup failed", err)
		return
	}
	id := *last.TriggerCheckRunID
	err = t.api.UpdateCheckRun(ctx, rc.InstallationID, rc.Owner, rc.Name, id, CheckRunParams{
		Name:       CheckRunName,
		Status:     "completed",
		Conclusion: "cancelled",
		Title:      "Agent Trail task cancelled",
		Summary:    "The task was cancelled before it published.",
	})
	if err != nil {
		warn("trigger check run completion failed", err)
		return
	}
	if err := t.tasks.MarkTriggerCheckRunCompleted(ctx, last.ID); err != nil {
		warn("trigger check run completion not recorded", err)
		return
	}
	if err := t.tasks.AppendAttemptEvent(ctx, last.ID, "github.check_run.updated", "system",
		map[string]any{"check_run_id": id, "conclusion": "cancelled", "kind": "trigger"}); err != nil {
		warn("trigger check run event not appended", err)
	}
}
