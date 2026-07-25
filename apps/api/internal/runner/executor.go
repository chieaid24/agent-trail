package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// agentEventTypes maps normalized adapter events to activity-event types
// (docs/architecture/data-model.md). The web client consumes these, never
// raw provider formats.
var agentEventTypes = map[agent.EventType]string{
	agent.EventSessionStarted:   "agent.started",
	agent.EventAssistantMessage: "agent.message",
	agent.EventPlan:             "plan.created",
	agent.EventToolRequested:    "command.requested",
	agent.EventToolStarted:      "command.started",
	agent.EventToolOutput:       "command.output",
	agent.EventToolCompleted:    "command.completed",
	agent.EventFileRead:         "file.read",
	agent.EventFileWritten:      "file.changed",
	agent.EventCostUpdate:       "agent.cost_update",
	agent.EventWarning:          "agent.warning",
	agent.EventSessionCompleted: "agent.completed",
	agent.EventSessionFailed:    "agent.failed",
}

// Executor drives one claimed attempt through the fake flow: workspace,
// agent session, stubbed validation and publishing, completion. It owns the
// lease for the duration and releases it on every exit path.
type Executor struct {
	Tasks   *task.Store
	Store   *Store
	Adapter agent.Adapter
	Logger  *slog.Logger
	// LeaseDuration is the claim lease; the executor extends it at a third
	// of this interval while it works.
	LeaseDuration time.Duration
}

// Execute drives claim c to a terminal task state. A cancelled ctx or a
// lost lease stops work and leaves the attempt for recovery: the task stays
// mid-flight, the lease (if still ours) is released, and a later claim
// resumes from the recorded status.
func (e *Executor) Execute(ctx context.Context, runnerID string, c *Claim) error {
	log := e.Logger.With(
		slog.String("task_id", c.TaskID),
		slog.String("attempt_id", c.AttemptID),
		slog.String("runner_id", runnerID),
	)

	// Stop everything the moment the lease cannot be extended: after expiry
	// another runner may own the attempt, and two owners must never run.
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(e.LeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-execCtx.Done():
				return
			case <-ticker.C:
				if err := e.Store.ExtendLease(execCtx, c.AttemptID, runnerID, e.LeaseDuration); err != nil {
					if !errors.Is(err, context.Canceled) {
						log.LogAttrs(execCtx, slog.LevelWarn, "lease extension failed; stopping",
							slog.String("event", "runner_lease_lost"),
							slog.String("error", err.Error()),
						)
					}
					cancel()
					return
				}
			}
		}
	}()
	err := e.drive(execCtx, log, c)
	// Stop extending before releasing, or the last extension races the
	// release and logs a spurious loss.
	cancel()
	<-heartbeatDone
	// Release only our own lease; after ErrLeaseLost there is nothing to
	// release and the attempt may already belong to another runner.
	if !errors.Is(err, ErrLeaseLost) {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		if relErr := e.Store.ReleaseLease(releaseCtx, c.AttemptID, runnerID); relErr != nil &&
			!errors.Is(relErr, ErrLeaseLost) {
			log.LogAttrs(ctx, slog.LevelError, "lease release failed",
				slog.String("event", "runner_lease_release_failed"),
				slog.String("error", relErr.Error()),
			)
		}
	}
	return err
}

// drive advances the task from its claimed status to completed. Interrupted
// attempts re-enter here at a later status and only run the missing stages;
// duplicated agent events across owners are accepted (at-least-once).
func (e *Executor) drive(ctx context.Context, log *slog.Logger, c *Claim) error {
	status := c.TaskStatus

	if status == task.StatusQueued {
		next, err := e.transition(ctx, c, task.StatusProvisioning, "runner", "")
		if err != nil {
			return err
		}
		status = next
	}

	// The agent must run unless a previous owner already got the task past
	// executing (its workspace died with it; later stages don't need one).
	if status == task.StatusProvisioning || status == task.StatusPlanning ||
		status == task.StatusExecuting {
		if err := e.runAgentStages(ctx, log, c, status); err != nil {
			return err
		}
		status = task.StatusValidating
	}

	if status == task.StatusValidating {
		// Trusted validation lands with milestone 6; record the skip
		// honestly instead of inventing a result.
		skip := map[string]any{
			"status": "skipped",
			"reason": "trusted validation lands with milestone 6",
		}
		if err := e.append(ctx, c, "validation.started", "runner", skip); err != nil {
			return err
		}
		if err := e.append(ctx, c, "validation.completed", "runner", skip); err != nil {
			return err
		}
		next, err := e.transition(ctx, c, task.StatusPublishing, "runner", "")
		if err != nil {
			return err
		}
		status = next
	}

	if status == task.StatusPublishing {
		if err := e.append(ctx, c, "publishing.skipped", "runner", map[string]any{
			"reason": "GitHub publishing lands with milestone 7",
		}); err != nil {
			return err
		}
		next, err := e.transition(ctx, c, task.StatusAwaitingReview, "runner", "")
		if err != nil {
			return err
		}
		status = next
	}

	if status == task.StatusAwaitingReview {
		// Nothing was published, so there is nothing for a human to review:
		// the fake flow closes its own loop. The review gate becomes real
		// with milestone 7 publishing.
		if _, err := e.transition(ctx, c, task.StatusCompleted, "system",
			"fake attempt auto-completed; review gates arrive with publishing"); err != nil {
			return err
		}
	}

	log.LogAttrs(ctx, slog.LevelInfo, "attempt finished",
		slog.String("event", "runner_attempt_finished"),
	)
	return nil
}

// runAgentStages provisions a workspace, runs the agent session, streams
// its events into the timeline, and leaves the task in validating.
func (e *Executor) runAgentStages(ctx context.Context, log *slog.Logger, c *Claim, status task.Status) error {
	if err := e.append(ctx, c, "workspace.provisioning", "runner", nil); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "agent-trail-attempt-")
	if err != nil {
		return e.failTask(ctx, c, "workspace_failed", err.Error())
	}
	defer func() {
		if err := os.RemoveAll(workspace); err == nil {
			// Best effort; a failed append must not fail a finished attempt.
			_ = e.Tasks.AppendAttemptEvent(context.WithoutCancel(ctx), c.AttemptID,
				"cleanup.completed", "runner", map[string]any{"workspace": "removed"})
		}
	}()
	if err := e.append(ctx, c, "workspace.ready", "runner", nil); err != nil {
		return err
	}

	if status == task.StatusProvisioning {
		if _, err := e.transition(ctx, c, task.StatusPlanning, "runner", ""); err != nil {
			return err
		}
		status = task.StatusPlanning
	}

	session, err := e.Adapter.Start(ctx, agent.Request{
		WorkspaceDir: workspace,
		Instructions: c.Instructions,
	})
	if err != nil {
		return e.failTask(ctx, c, "agent_start_failed", err.Error())
	}

	// On an error mid-stream the session must still be drained: the event
	// channel is unbuffered, so an abandoned producer would block forever.
	abort := func(err error) error {
		_ = session.Cancel(ctx)
		go func() {
			for range session.Events() {
			}
		}()
		return err
	}
	for ev := range session.Events() {
		eventType, ok := agentEventTypes[ev.Type]
		if !ok {
			eventType = "agent." + string(ev.Type)
		}
		if err := e.append(ctx, c, eventType, "agent", ev.Payload); err != nil {
			return abort(err)
		}
		// The plan marks the end of planning.
		if ev.Type == agent.EventPlan && status == task.StatusPlanning {
			next, err := e.transition(ctx, c, task.StatusExecuting, "runner", "")
			if err != nil {
				return abort(err)
			}
			status = next
		}
	}

	result, err := session.Wait(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown or lease loss: leave the task mid-flight for recovery.
			return ctx.Err()
		}
		return e.failTask(ctx, c, "agent_failed", err.Error())
	}
	log.LogAttrs(ctx, slog.LevelInfo, "agent session completed",
		slog.String("event", "runner_agent_completed"),
		slog.String("summary", result.Summary),
		slog.Int("files_changed", len(result.FilesChanged)),
	)

	if status == task.StatusExecuting {
		if _, err := e.transition(ctx, c, task.StatusValidating, "runner", ""); err != nil {
			return err
		}
	}
	return nil
}

// transition applies one runner-driven task transition. The idempotency key
// scopes it to the attempt, so an interrupted owner's replay cannot double-
// apply. A task that moved somewhere the edge no longer fits (cancelled
// under us) surfaces as InvalidTransitionError for the caller to stop on.
func (e *Executor) transition(ctx context.Context, c *Claim, to task.Status, source, reason string) (task.Status, error) {
	_, err := e.Tasks.Transition(ctx, c.TaskID, task.TransitionParams{
		To:             to,
		Source:         source,
		Reason:         reason,
		IdempotencyKey: fmt.Sprintf("attempt:%s:to:%s", c.AttemptID, to),
	})
	if err != nil {
		return "", fmt.Errorf("transition to %s: %w", to, err)
	}
	return to, nil
}

// append adds one activity event to the attempt timeline.
func (e *Executor) append(ctx context.Context, c *Claim, eventType, source string, payload any) error {
	if err := e.Tasks.AppendAttemptEvent(ctx, c.AttemptID, eventType, source, payload); err != nil {
		return fmt.Errorf("append %s: %w", eventType, err)
	}
	return nil
}

// failTask records a safe failure: the terminal transition also closes the
// attempt (store semantics) with the failure preserved on both.
func (e *Executor) failTask(ctx context.Context, c *Claim, code, message string) error {
	_, err := e.Tasks.Transition(ctx, c.TaskID, task.TransitionParams{
		To:             task.StatusFailed,
		Source:         "runner",
		FailureCode:    code,
		FailureMessage: message,
		IdempotencyKey: fmt.Sprintf("attempt:%s:fail:%s", c.AttemptID, code),
	})
	if err != nil {
		return fmt.Errorf("fail task (%s): %w", code, err)
	}
	return fmt.Errorf("attempt failed: %s: %s", code, message)
}
