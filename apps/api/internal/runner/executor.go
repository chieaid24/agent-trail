package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
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

// Executor drives one claimed attempt through the flow: workspace, agent
// session, trusted validation, evidence, GitHub publishing, review.
// It owns the lease for the duration and releases it on every exit path.
type Executor struct {
	Tasks       *task.Store
	Store       *Store
	Validations *validation.Store
	Evidence    *evidence.Store
	Adapter     agent.Adapter
	Logger      *slog.Logger
	// Workspaces, GitHub, and Repos are the publishing dependencies: all
	// three set enables git worktree workspaces and GitHub publishing for
	// repository-backed tasks (docs/architecture/publishing.md). Any nil
	// keeps the fake local flow: temp-dir workspace, publishing skipped.
	Workspaces *gitworkspace.Manager
	GitHub     PublishGitHub
	Repos      RepositoryResolver
	// Conflicts records active-task overlap; nil disables detection.
	Conflicts *conflict.Detector
	// LeaseDuration is the claim lease; the executor extends it at a third
	// of this interval while it works.
	LeaseDuration time.Duration
}

// ErrAttemptFailed wraps every failTask error: the task reached a terminal
// failed state, so the attempt is settled rather than retryable.
var ErrAttemptFailed = errors.New("attempt failed")

// Execute drives claim c to a terminal task state. A cancelled ctx or a
// lost lease stops work and leaves the attempt for recovery: the task stays
// mid-flight, the lease (if still ours) is released, and a later claim
// resumes from the recorded status.
func (e *Executor) Execute(ctx context.Context, runnerID string, c *Claim) error {
	log := e.Logger.With(
		slog.String("task_id", c.TaskID),
		slog.String("task_attempt_id", c.AttemptID),
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

// drive advances the task from its claimed status to its resting state:
// completed for the fake local flow, awaiting_review for a published task.
// Interrupted attempts re-enter here at a later status and only run the
// missing stages; duplicated agent events across owners are accepted
// (at-least-once).
func (e *Executor) drive(ctx context.Context, log *slog.Logger, c *Claim) error {
	status := c.TaskStatus

	if status == task.StatusQueued {
		next, err := e.transition(ctx, c, task.StatusProvisioning, "runner", "")
		if err != nil {
			return err
		}
		status = next
	}

	t, err := e.Tasks.Get(ctx, c.TaskID)
	if err != nil {
		return err
	}
	pub, err := e.publishTarget(ctx, c, t)
	if err != nil {
		return err
	}

	// The agent must run unless a previous owner already got the task past
	// executing. Trusted validation, evidence, and (for repository-backed
	// tasks) publishing run inside the agent stages, while the workspace
	// still exists.
	if status == task.StatusProvisioning || status == task.StatusPlanning ||
		status == task.StatusExecuting {
		next, err := e.runAgentStages(ctx, log, c, t, pub, status)
		if err != nil {
			return err
		}
		status = next
	}

	if status == task.StatusValidating {
		// Recovery only: the previous owner died mid-validating. A temp-dir
		// workspace died with it, so the remaining checks cannot run.
		// Record the infrastructure failure honestly - never a pass - and
		// build evidence from whatever it managed to store.
		if err := e.append(ctx, c, "validation.completed", "runner", map[string]any{
			"status": string(validation.StatusError),
			"reason": workspaceLostNote,
		}); err != nil {
			return err
		}
		if err := e.generateEvidence(ctx, c, workspaceLostNote); err != nil {
			return err
		}
		next, err := e.transition(ctx, c, task.StatusPublishing, "runner", "")
		if err != nil {
			return err
		}
		status = next
	}

	if status == task.StatusPublishing {
		if pub == nil {
			if err := e.append(ctx, c, "publishing.skipped", "runner", map[string]any{
				"reason": "task has no repository or publishing is not configured",
			}); err != nil {
				return err
			}
			next, err := e.transition(ctx, c, task.StatusAwaitingReview, "runner", "")
			if err != nil {
				return err
			}
			status = next
		} else {
			// Recovery only: the previous owner died mid-publishing. Its
			// worktree survives on the same host; otherwise the pushed
			// branch (if any) carries the work.
			next, err := e.publishRecovered(ctx, c, t, pub)
			if err != nil {
				return err
			}
			status = next
		}
	}

	if status == task.StatusAwaitingReview && pub == nil {
		// Nothing was published, so there is nothing for a human to review:
		// the fake flow closes its own loop. Published tasks stay in
		// awaiting_review for the human on the draft PR.
		if _, err := e.transition(ctx, c, task.StatusCompleted, "system",
			"fake attempt auto-completed; nothing was published to review"); err != nil {
			return err
		}
	}

	log.LogAttrs(ctx, slog.LevelInfo, "attempt finished",
		slog.String("event", "runner_attempt_finished"),
	)
	return nil
}

// runAgentStages provisions a workspace, runs the agent session, streams
// its events into the timeline, then runs trusted validation, evidence,
// and (for publishable tasks) publishing while the workspace still exists.
// It returns the status the task rests at: publishing for the fake local
// flow, awaiting_review after a successful publish.
func (e *Executor) runAgentStages(ctx context.Context, log *slog.Logger, c *Claim, t task.Task, pub *publishTarget, status task.Status) (st task.Status, retErr error) {
	if err := e.append(ctx, c, "workspace.provisioning", "runner", nil); err != nil {
		return "", err
	}
	var ws gitworkspace.Workspace
	var workspace string
	if pub != nil {
		created, err := e.provisionWorkspace(ctx, c, t, pub)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", e.failTask(ctx, c, "workspace_failed", err.Error())
		}
		ws = created
		workspace = ws.Path
	} else {
		dir, err := os.MkdirTemp("", "agent-trail-attempt-")
		if err != nil {
			return "", e.failTask(ctx, c, "workspace_failed", err.Error())
		}
		workspace = dir
	}
	defer func() {
		cleanupCtx := context.WithoutCancel(ctx)
		if pub != nil {
			// A settled attempt (published, or terminally failed) releases
			// its worktree; a transient error keeps it so the recovering
			// owner can reattach (Lookup) instead of losing the work.
			if retErr != nil && !errors.Is(retErr, ErrAttemptFailed) {
				return
			}
			if err := e.Workspaces.Remove(cleanupCtx, ws); err != nil {
				log.LogAttrs(ctx, slog.LevelWarn, "workspace cleanup failed",
					slog.String("event", "runner_workspace_cleanup_failed"),
					slog.String("error", err.Error()),
				)
				return
			}
		} else if err := os.RemoveAll(workspace); err != nil {
			log.LogAttrs(ctx, slog.LevelWarn, "workspace cleanup failed",
				slog.String("event", "runner_workspace_cleanup_failed"),
				slog.String("error", err.Error()),
			)
			return
		}
		// Best effort; a failed append must not fail a finished attempt.
		_ = e.Tasks.AppendAttemptEvent(cleanupCtx, c.AttemptID,
			"cleanup.completed", "runner", map[string]any{"workspace": "removed"})
	}()
	if err := e.append(ctx, c, "workspace.ready", "runner", nil); err != nil {
		return "", err
	}

	if status == task.StatusProvisioning {
		if _, err := e.transition(ctx, c, task.StatusPlanning, "runner", ""); err != nil {
			return "", err
		}
		status = task.StatusPlanning
	}

	session, err := e.Adapter.Start(ctx, agent.Request{
		WorkspaceDir: workspace,
		Instructions: c.Instructions,
	})
	if err != nil {
		return "", e.failTask(ctx, c, "agent_start_failed", err.Error())
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
			return "", abort(err)
		}
		// The plan marks the end of planning.
		if ev.Type == agent.EventPlan && status == task.StatusPlanning {
			next, err := e.transition(ctx, c, task.StatusExecuting, "runner", "")
			if err != nil {
				return "", abort(err)
			}
			status = next
		}
	}

	result, err := session.Wait(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown or lease loss: leave the task mid-flight for recovery.
			return "", ctx.Err()
		}
		return "", e.failTask(ctx, c, "agent_failed", err.Error())
	}
	log.LogAttrs(ctx, slog.LevelInfo, "agent session completed",
		slog.String("event", "runner_agent_completed"),
		slog.String("summary", result.Summary),
		slog.Int("files_changed", len(result.FilesChanged)),
	)

	// A provider may finish without ever emitting a plan (the plan event is
	// what advances planning -> executing). Don't strand the task in planning:
	// session end is the end of execution whether or not a plan was captured.
	if status == task.StatusPlanning {
		next, err := e.transition(ctx, c, task.StatusExecuting, "runner", "")
		if err != nil {
			return "", err
		}
		status = next
	}
	if status == task.StatusExecuting {
		if _, err := e.transition(ctx, c, task.StatusValidating, "runner", ""); err != nil {
			return "", err
		}
	}

	// The workspace dies with this function (deferred cleanup above), so
	// everything that needs it on disk - validation, evidence, commit, and
	// push - happens before returning.
	note, err := e.runTrustedValidation(ctx, log, c, workspace)
	if err != nil {
		return "", err
	}
	if err := e.generateEvidence(ctx, c, note); err != nil {
		return "", err
	}
	next, err := e.transition(ctx, c, task.StatusPublishing, "runner", "")
	if err != nil {
		return "", err
	}
	if pub == nil {
		return next, nil
	}
	return e.publishFromWorkspace(ctx, c, t, pub, ws, result.Summary)
}

// workspaceLostNote explains an unrunnable recovery-path validation.
const workspaceLostNote = "the workspace was lost before trusted validation completed"

// evidenceEventLimit bounds the event-stream read behind one report.
const evidenceEventLimit = 1000

// runTrustedValidation loads the repository validation file and executes
// its checks in the workspace, storing each result before announcing it.
// The returned note is non-empty when the checks could not run (no file,
// invalid file) and says why; a failing check is a result, never an error.
func (e *Executor) runTrustedValidation(ctx context.Context, log *slog.Logger, c *Claim, workspace string) (string, error) {
	if err := e.append(ctx, c, "validation.started", "runner", map[string]any{
		"trusted_execution": true,
	}); err != nil {
		return "", err
	}

	file, found, err := validation.Load(workspace)
	if !found && err == nil {
		note := "no validation file at " + validation.FileName
		if err := e.append(ctx, c, "validation.completed", "runner", map[string]any{
			"status": "skipped", "reason": note,
		}); err != nil {
			return "", err
		}
		return note, nil
	}
	if err != nil {
		// An unreadable or invalid file is an infrastructure-class
		// outcome: the checks never ran, which is not a check failure
		// and must never read as a pass.
		note := "invalid validation file: " + err.Error()
		if appendErr := e.append(ctx, c, "validation.completed", "runner", map[string]any{
			"status": string(validation.StatusError), "reason": note,
		}); appendErr != nil {
			return "", appendErr
		}
		return note, nil
	}

	runner := &validation.Runner{Logger: log}
	var insertErr, eventErr error
	results := runner.Run(ctx, workspace, file, func(r validation.Result) {
		if insertErr != nil || eventErr != nil {
			return
		}
		// Persist before announcing: the stored exit code is the record
		// of what happened, independent of anything the agent claimed.
		if insertErr = e.Validations.Insert(ctx, c.AttemptID, r); insertErr != nil {
			return
		}
		payload := map[string]any{
			"name":              r.Name,
			"category":          r.Category,
			"status":            string(r.Status),
			"duration_ms":       r.DurationMS,
			"trusted_execution": true,
			"summary":           r.Summary,
		}
		if r.ExitCode != nil {
			payload["exit_code"] = *r.ExitCode
		}
		eventErr = e.append(ctx, c, "validation.check.completed", "runner", payload)
	})
	if insertErr != nil {
		return "", insertErr
	}
	if eventErr != nil {
		return "", eventErr
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	counts := map[validation.Status]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	// The aggregate keeps the check/infrastructure distinction: failed
	// means a check measurably failed; error means checks could not all
	// run (timeout or infrastructure) while none failed.
	overall := string(validation.StatusPassed)
	switch {
	case counts[validation.StatusFailed] > 0:
		overall = string(validation.StatusFailed)
	case len(results) != counts[validation.StatusPassed]:
		overall = string(validation.StatusError)
	}
	if err := e.append(ctx, c, "validation.completed", "runner", map[string]any{
		"status":            overall,
		"trusted_execution": true,
		"checks":            len(results),
		"passed":            counts[validation.StatusPassed],
		"failed":            counts[validation.StatusFailed],
		"timed_out":         counts[validation.StatusTimedOut],
		"errors":            counts[validation.StatusError],
	}); err != nil {
		return "", err
	}
	return "", nil
}

// generateEvidence assembles and stores the attempt's evidence report from
// what was actually recorded: stored trusted results, the agent's event
// stream (plan, file changes, claimed commands), and the task row.
func (e *Executor) generateEvidence(ctx context.Context, c *Claim, validationNote string) error {
	t, err := e.Tasks.Get(ctx, c.TaskID)
	if err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	trusted, err := e.Validations.ListForAttempt(ctx, c.AttemptID)
	if err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	events, err := e.Tasks.Events(ctx, c.TaskID, evidenceEventLimit)
	if err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	var unverified []string
	if len(events) == evidenceEventLimit {
		unverified = append(unverified, fmt.Sprintf(
			"event stream truncated at %d events; plan, file, and command claims may be incomplete",
			evidenceEventLimit))
	}

	var plan, files []string
	seenFiles := map[string]bool{}
	var claimed []evidence.CheckResult
	for _, ev := range events {
		if ev.TaskAttemptID != c.AttemptID || ev.Source != "agent" {
			continue
		}
		switch ev.EventType {
		case "plan.created":
			var p struct {
				Plan string `json:"plan"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Plan != "" {
				plan = planSteps(p.Plan)
			}
		case "file.changed":
			var p struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Path != "" && !seenFiles[p.Path] {
				seenFiles[p.Path] = true
				files = append(files, p.Path)
			}
		case "command.completed":
			// Commands the agent says it ran are recorded as claims: they
			// never gain trusted_execution and never alter a stored result.
			var p struct {
				Command  string `json:"command"`
				ExitCode *int   `json:"exit_code"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Command != "" {
				// A claim without an exit code stays unknown: inventing
				// a pass or a failure would both be unmeasured.
				status := "unknown"
				if p.ExitCode != nil {
					status = string(validation.StatusFailed)
					if *p.ExitCode == 0 {
						status = string(validation.StatusPassed)
					}
				}
				claimed = append(claimed, evidence.CheckResult{
					Name:             p.Command,
					Category:         "custom",
					Status:           status,
					TrustedExecution: false,
					ExitCode:         p.ExitCode,
				})
			}
		}
	}

	// Duration is the attempt's wall clock from its stored started_at;
	// the task's own start would fold earlier attempts into this one.
	var duration *int64
	if startedAt, err := e.Store.AttemptStartedAt(ctx, c.AttemptID); err != nil {
		return fmt.Errorf("evidence: %w", err)
	} else if startedAt != nil {
		d := int64(time.Since(*startedAt).Seconds())
		duration = &d
	}
	// Prefer the provider recorded on the task row; the live adapter name
	// is the fallback so the report never guesses.
	provider := e.Adapter.Name()
	if t.AgentProvider != nil {
		provider = *t.AgentProvider
	}
	report := evidence.Generate(evidence.Params{
		Task:            t,
		AgentProvider:   provider,
		DurationSeconds: duration,
		Plan:            plan,
		FilesChanged:    files,
		Trusted:         trusted,
		AgentReported:   claimed,
		ValidationNote:  validationNote,
		Unverified:      unverified,
	})
	if err := e.Evidence.Insert(ctx, c.AttemptID, report, evidence.Markdown(report)); err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	return e.append(ctx, c, "evidence.generated", "runner", map[string]any{
		"schema_version": report.SchemaVersion,
		"trusted_checks": len(trusted),
	})
}

var planStepRe = regexp.MustCompile(`^\s*\d+[.)]\s*`)

// planSteps splits plan text into steps, dropping list numbering.
func planSteps(text string) []string {
	var steps []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(planStepRe.ReplaceAllString(line, ""))
		if line != "" {
			steps = append(steps, line)
		}
	}
	return steps
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
	return fmt.Errorf("%w: %s: %s", ErrAttemptFailed, code, message)
}
