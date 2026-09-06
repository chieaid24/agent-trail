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
	"sync/atomic"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

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

// owns lease for the attempt; releases only after provider shutdown
type Executor struct {
	Tasks       *task.Store
	Store       *Store
	Validations *validation.Store
	Evidence    *evidence.Store
	Adapter     agent.Adapter
	Logger      *slog.Logger
	// all three set -> git worktrees + github publishing; any nil -> fake temp-dir flow
	Workspaces          *gitworkspace.Manager
	GitHub              PublishGitHub
	Repos               RepositoryResolver
	Conflicts           *conflict.Detector
	Metrics             *Metrics
	LeaseDuration       time.Duration
	DefaultRuntime      time.Duration
	SessionStopTimeout  time.Duration
	extendLeaseHook     func(context.Context, string, string, time.Duration) error
	fenceLeaseHook      func(context.Context, string, string, time.Duration) error
	leaseLostHook       func()
	runtimeDeadlineHook func(context.Context, *Claim) (time.Duration, time.Time, error)
}

type attemptLeaseState struct {
	lost     atomic.Bool
	runnerID string
}

type attemptLeaseStateKey struct{}

func leaseOwnershipLost(ctx context.Context) bool {
	state, _ := ctx.Value(attemptLeaseStateKey{}).(*attemptLeaseState)
	return state != nil && state.lost.Load()
}

var ErrAttemptFailed = errors.New("attempt failed")

var ErrSessionStopFailed = errors.New("session stop failed")

const (
	fallbackRuntime          = 45 * time.Minute
	cancellationPollInterval = 100 * time.Millisecond
	sessionCancelTimeout     = 5 * time.Second
)

// shutdown/lease loss leaves attempt for recovery; attempt deadline settles timed_out
func (e *Executor) Execute(ctx context.Context, runnerID string, c *Claim) error {
	log := e.Logger.With(
		slog.String("task_id", c.TaskID),
		slog.String("task_attempt_id", c.AttemptID),
		slog.String("runner_id", runnerID),
	)

	remaining := time.Until(c.LeaseExpiresAt)
	if remaining <= 0 {
		return ErrLeaseLost
	}
	fenceTimeout := remaining / 2
	if fenceTimeout > 5*time.Second {
		fenceTimeout = 5 * time.Second
	}
	fenceCtx, fenceCancel := context.WithTimeout(ctx, fenceTimeout)
	err := e.Store.ExtendLease(fenceCtx, c.AttemptID, runnerID, e.LeaseDuration)
	fenceCancel()
	if err != nil {
		return err
	}
	startupTimeout := e.LeaseDuration / 3
	if startupTimeout <= 0 || startupTimeout > 5*time.Second {
		startupTimeout = 5 * time.Second
	}
	startupCtx, startupCancel := context.WithTimeout(ctx, startupTimeout)
	runtime, deadline, err := e.runtimeDeadline(startupCtx, c)
	startupCancel()
	if err != nil {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		return errors.Join(err, e.Store.ReleaseLease(releaseCtx, c.AttemptID, runnerID))
	}
	deadlineCtx, cancelDeadline := context.WithDeadline(ctx, deadline)
	defer cancelDeadline()
	cancelCtx, cancel := context.WithCancelCause(deadlineCtx)
	defer cancel(context.Canceled)
	leaseState := &attemptLeaseState{runnerID: runnerID}
	execCtx := context.WithValue(cancelCtx, attemptLeaseStateKey{}, leaseState)
	leaseCtx, stopLease := context.WithCancel(context.WithoutCancel(ctx))
	defer stopLease()
	cancellationDone := make(chan struct{})
	go func() {
		defer close(cancellationDone)
		e.watchTaskCancellation(execCtx, func() { cancel(context.Canceled) }, log, c.TaskID)
	}()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(e.LeaseDuration / 3)
		defer ticker.Stop()
		retryDelay := e.LeaseDuration / 9
		if retryDelay <= 0 || retryDelay > 100*time.Millisecond {
			retryDelay = 100 * time.Millisecond
		}
		extensionTimeout := e.LeaseDuration / 3
		if extensionTimeout <= 0 || extensionTimeout > 5*time.Second {
			extensionTimeout = 5 * time.Second
		}
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
			}
			for {
				extendCtx, cancelExtend := context.WithTimeout(leaseCtx, extensionTimeout)
				err := e.extendLease(extendCtx, c.AttemptID, runnerID)
				cancelExtend()
				if err == nil {
					break
				}
				if errors.Is(err, context.Canceled) {
					return
				}
				if errors.Is(err, ErrLeaseLost) {
					e.markLeaseLost(leaseState)
					log.LogAttrs(leaseCtx, slog.LevelWarn, "lease lost; stopping",
						slog.String("event", "runner_lease_lost"),
						slog.String("error", err.Error()),
					)
					cancel(ErrLeaseLost)
					return
				}
				log.LogAttrs(leaseCtx, slog.LevelWarn, "lease extension failed; retrying",
					slog.String("event", "runner_lease_extension_failed"),
					slog.String("error", err.Error()),
				)
				cancel(err)
				retry := time.NewTimer(retryDelay)
				select {
				case <-leaseCtx.Done():
					retry.Stop()
					return
				case <-retry.C:
				}
			}
		}
	}()
	spanCtx, span := startSpan(execCtx, "runner.attempt", c)
	err = e.drive(spanCtx, log, c)
	executionCause := context.Cause(execCtx)
	defer func() { endSpan(span, err) }()
	// stop extending before release or last extension races it
	cancel(context.Canceled)
	<-cancellationDone
	stopLease()
	<-heartbeatDone
	if leaseState.lost.Load() {
		err = errors.Join(err, ErrLeaseLost)
	}
	if executionCause != nil && !errors.Is(err, executionCause) {
		err = errors.Join(err, executionCause)
	}
	timedOut := err != nil && !leaseState.lost.Load() &&
		errors.Is(executionCause, context.DeadlineExceeded) &&
		!errors.Is(err, ErrAttemptFailed)
	if timedOut {
		err = errors.Join(e.timeoutTask(execCtx, c, runtime), err)
	}
	err = e.cleanupTerminalRecovery(execCtx, log, c, err)
	if !errors.Is(err, ErrLeaseLost) {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		if relErr := e.Store.ReleaseLease(releaseCtx, c.AttemptID, runnerID); relErr != nil &&
			!errors.Is(relErr, ErrLeaseLost) {
			log.LogAttrs(ctx, slog.LevelError, "lease release failed",
				slog.String("event", "runner_lease_release_failed"),
				slog.String("error", relErr.Error()),
			)
			err = errors.Join(err, fmt.Errorf("release lease: %w", relErr))
		}
	}
	return err
}

func (e *Executor) extendLease(ctx context.Context, attemptID, runnerID string) error {
	if e.extendLeaseHook != nil {
		return e.extendLeaseHook(ctx, attemptID, runnerID, e.LeaseDuration)
	}
	return e.Store.ExtendLease(ctx, attemptID, runnerID, e.LeaseDuration)
}

func (e *Executor) markLeaseLost(state *attemptLeaseState) {
	if state.lost.CompareAndSwap(false, true) && e.leaseLostHook != nil {
		e.leaseLostHook()
	}
}

func (e *Executor) leaseOperationTimeout() time.Duration {
	timeout := e.LeaseDuration / 3
	if timeout <= 0 || timeout > sessionCancelTimeout {
		return sessionCancelTimeout
	}
	return timeout
}

func (e *Executor) fenceLeaseOwnership(ctx context.Context, c *Claim) error {
	state, _ := ctx.Value(attemptLeaseStateKey{}).(*attemptLeaseState)
	if state == nil {
		return nil
	}
	if state.lost.Load() {
		return ErrLeaseLost
	}
	fenceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.leaseOperationTimeout())
	defer cancel()
	retryDelay := e.LeaseDuration / 9
	if retryDelay <= 0 || retryDelay > 100*time.Millisecond {
		retryDelay = 100 * time.Millisecond
	}
	for {
		err := e.fenceLease(fenceCtx, c.AttemptID, state.runnerID)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrLeaseLost) {
			e.markLeaseLost(state)
			return err
		}
		retry := time.NewTimer(retryDelay)
		select {
		case <-fenceCtx.Done():
			retry.Stop()
			return errors.Join(err, fenceCtx.Err())
		case <-retry.C:
		}
	}
}

func (e *Executor) fenceLease(ctx context.Context, attemptID, runnerID string) error {
	if e.fenceLeaseHook != nil {
		return e.fenceLeaseHook(ctx, attemptID, runnerID, e.LeaseDuration)
	}
	return e.Store.ExtendLease(ctx, attemptID, runnerID, e.LeaseDuration)
}

func (e *Executor) cleanupTerminalRecovery(ctx context.Context, log *slog.Logger, c *Claim, retErr error) error {
	if e.Workspaces == nil || errors.Is(retErr, ErrLeaseLost) || leaseOwnershipLost(ctx) {
		return retErr
	}
	if err := e.fenceLeaseOwnership(ctx, c); err != nil {
		return errors.Join(retErr, fmt.Errorf("fence recovery cleanup: %w", err))
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.leaseOperationTimeout())
	defer cancel()
	t, err := e.Tasks.Get(cleanupCtx, c.TaskID)
	if err != nil {
		return errors.Join(retErr, fmt.Errorf("read task for recovery cleanup: %w", err))
	}
	if !t.Status.Terminal() || t.RepositoryID == nil || t.WorkingBranch == nil ||
		!e.Workspaces.WorkspaceExists(c.AttemptID) {
		return retErr
	}
	repo := gitworkspace.RepoRef{ID: *t.RepositoryID}
	if err := e.Workspaces.CleanupStale(cleanupCtx, repo, c.AttemptID, *t.WorkingBranch); err != nil {
		log.LogAttrs(ctx, slog.LevelWarn, "workspace cleanup failed",
			slog.String("event", "runner_workspace_cleanup_failed"),
			slog.String("error", err.Error()),
		)
		return errors.Join(retErr, fmt.Errorf("workspace cleanup: %w", err))
	}
	if err := e.Tasks.AppendAttemptEvent(cleanupCtx, c.AttemptID,
		"cleanup.completed", "runner", map[string]any{"workspace": "removed"}); err != nil {
		return errors.Join(retErr, fmt.Errorf("record workspace cleanup: %w", err))
	}
	return retErr
}

func (e *Executor) runtimeDeadline(ctx context.Context, c *Claim) (time.Duration, time.Time, error) {
	if e.runtimeDeadlineHook != nil {
		return e.runtimeDeadlineHook(ctx, c)
	}
	t, err := e.Tasks.Get(ctx, c.TaskID)
	if err != nil {
		return 0, time.Time{}, err
	}
	runtime := e.taskRuntime(t)
	startedAt, err := e.Store.AttemptStartedAt(ctx, c.AttemptID)
	if err != nil {
		return 0, time.Time{}, err
	}
	start := time.Now()
	if startedAt != nil {
		start = *startedAt
	}
	return runtime, start.Add(runtime), nil
}

func (e *Executor) taskRuntime(t task.Task) time.Duration {
	runtime := e.DefaultRuntime
	if runtime <= 0 {
		runtime = fallbackRuntime
	}
	if t.MaxRuntimeSeconds != nil {
		runtime = time.Duration(*t.MaxRuntimeSeconds) * time.Second
	}
	return runtime
}

func (e *Executor) sessionStopTimeout() time.Duration {
	if e.SessionStopTimeout > 0 {
		return e.SessionStopTimeout
	}
	return sessionCancelTimeout
}

func (e *Executor) watchTaskCancellation(ctx context.Context, cancel context.CancelFunc, log *slog.Logger, taskID string) {
	ticker := time.NewTicker(cancellationPollInterval)
	defer ticker.Stop()
	loggedFailure := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		t, err := e.Tasks.Get(ctx, taskID)
		if err != nil {
			if ctx.Err() == nil && !loggedFailure {
				log.LogAttrs(ctx, slog.LevelWarn, "task cancellation check failed",
					slog.String("event", "runner_cancellation_check_failed"),
					slog.String("error", err.Error()),
				)
				loggedFailure = true
			}
			continue
		}
		loggedFailure = false
		if t.Status.Terminal() {
			cancel()
			return
		}
	}
}

func (e *Executor) timeoutTask(ctx context.Context, c *Claim, runtime time.Duration) error {
	if err := e.fenceLeaseOwnership(ctx, c); err != nil {
		return fmt.Errorf("fence timeout settlement: %w", err)
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.leaseOperationTimeout())
	defer cancel()
	current, err := e.Tasks.Get(settleCtx, c.TaskID)
	if err != nil {
		return fmt.Errorf("read task after timeout: %w", err)
	}
	if current.Status == task.StatusCancelled {
		return context.Canceled
	}
	if current.Status.Terminal() {
		return fmt.Errorf("%w: task already %s", ErrAttemptFailed, current.Status)
	}
	message := fmt.Sprintf("task exceeded runtime limit of %s", runtime)
	_, err = e.Tasks.Transition(settleCtx, c.TaskID, task.TransitionParams{
		To:             task.StatusTimedOut,
		Source:         "runner",
		FailureCode:    "task_runtime_exceeded",
		FailureMessage: message,
		IdempotencyKey: fmt.Sprintf("attempt:%s:timeout", c.AttemptID),
	})
	if err != nil {
		return fmt.Errorf("time out task: %w", err)
	}
	return fmt.Errorf("%w: task_runtime_exceeded: %s", ErrAttemptFailed, message)
}

// interrupted attempts re-enter at a later status and only run missing stages; agent events are at-least-once
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

	// validation, evidence, publishing run inside agent stages while workspace exists
	if status == task.StatusProvisioning || status == task.StatusPlanning ||
		status == task.StatusExecuting {
		next, err := e.runAgentStages(ctx, log, c, t, pub, status)
		if err != nil {
			return err
		}
		status = next
	}

	if status == task.StatusValidating {
		// recovery: prior owner died mid-validating, workspace gone; record error, never a pass
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
			// recovery: prior owner died mid-publishing; surviving worktree or pushed branch carries the work
			next, err := e.publishRecovered(ctx, log, c, t, pub)
			if err != nil {
				return err
			}
			status = next
		}
	}

	if status == task.StatusAwaitingReview && pub == nil {
		// nothing published -> fake flow self-completes; published tasks wait for human review
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

func (e *Executor) runAgentStages(ctx context.Context, log *slog.Logger, c *Claim, t task.Task, pub *publishTarget, status task.Status) (st task.Status, retErr error) {
	if err := e.append(ctx, c, "workspace.provisioning", "runner", nil); err != nil {
		return "", err
	}
	var ws gitworkspace.Workspace
	var workspace string
	workspaceCleaned := false
	provisionCtx, provisionSpan := startSpan(ctx, "runner.provisioning", c)
	if pub != nil {
		created, err := e.provisionWorkspace(provisionCtx, c, t, pub)
		endSpan(provisionSpan, err)
		if err != nil {
			if ctx.Err() != nil {
				return "", context.Cause(ctx)
			}
			return "", e.failTask(ctx, c, "workspace_failed", err.Error())
		}
		ws = created
		workspace = ws.Path
	} else {
		dir, err := os.MkdirTemp("", "agent-trail-attempt-")
		endSpan(provisionSpan, err)
		if err != nil {
			return "", e.failTask(ctx, c, "workspace_failed", err.Error())
		}
		workspace = dir
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), sessionCancelTimeout)
		defer cleanupCancel()
		if pub != nil {
			if !workspaceCleaned {
				retErr = e.cleanupGitWorkspace(ctx, log, c, ws, retErr)
			}
			return
		} else if err := os.RemoveAll(workspace); err != nil {
			e.Metrics.observeCleanup("failed")
			log.LogAttrs(ctx, slog.LevelWarn, "workspace cleanup failed",
				slog.String("event", "runner_workspace_cleanup_failed"),
				slog.String("error", err.Error()),
			)
			retErr = errors.Join(retErr, fmt.Errorf("workspace cleanup: %w", err))
			return
		}
		e.Metrics.observeCleanup("removed")
		if err := e.Tasks.AppendAttemptEvent(cleanupCtx, c.AttemptID,
			"cleanup.completed", "runner", map[string]any{"workspace": "removed"}); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("record workspace cleanup: %w", err))
		}
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

	sessionCtx, sessionSpan := startSpan(ctx, "agent.session", c)
	session, err := e.Adapter.Start(sessionCtx, agent.Request{
		WorkspaceDir: workspace,
		Instructions: c.Instructions,
	})
	if err != nil {
		endSpan(sessionSpan, err)
		return "", e.failTask(ctx, c, "agent_start_failed", err.Error())
	}

	// must still drain on error: channel unbuffered, abandoned producer blocks forever
	abort := func(err error) error {
		endSpan(sessionSpan, err)
		return errors.Join(err, stopSession(session, session.Events(), e.sessionStopTimeout()))
	}
	events := session.Events()
	for {
		var ev agent.Event
		var ok bool
		select {
		case ev, ok = <-events:
			if !ok {
				goto sessionEnded
			}
		case <-ctx.Done():
			cause := context.Cause(ctx)
			endSpan(sessionSpan, cause)
			return "", errors.Join(cause, stopSession(session, events, e.sessionStopTimeout()))
		}
		e.Metrics.observeLogBytes(len(ev.Payload))
		eventType, ok := agentEventTypes[ev.Type]
		if !ok {
			eventType = "agent." + string(ev.Type)
		}
		if err := e.append(ctx, c, eventType, "agent", ev.Payload); err != nil {
			return "", abort(err)
		}
		if ev.Type == agent.EventPlan && status == task.StatusPlanning {
			next, err := e.transition(ctx, c, task.StatusExecuting, "runner", "")
			if err != nil {
				return "", abort(err)
			}
			status = next
		}
	}

sessionEnded:

	result, err := session.Wait(ctx)
	endSpan(sessionSpan, err)
	if err != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		return "", e.failTask(ctx, c, "agent_failed", err.Error())
	}
	log.LogAttrs(ctx, slog.LevelInfo, "agent session completed",
		slog.String("event", "runner_agent_completed"),
		slog.String("summary", result.Summary),
		slog.Int("files_changed", len(result.FilesChanged)),
	)

	// provider may never emit a plan; session end still ends planning
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

	// workspace dies with this fn: validation, evidence, commit, push must run before return
	valCtx, valSpan := startSpan(ctx, "validation.run", c)
	valStart := time.Now()
	note, err := e.runTrustedValidation(valCtx, log, c, workspace)
	e.Metrics.observeValidation(time.Since(valStart))
	endSpan(valSpan, err)
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
	if _, err := e.publishFromWorkspace(ctx, log, c, t, pub, ws, result.Summary); err != nil {
		return "", err
	}
	if err := e.cleanupGitWorkspace(ctx, log, c, ws, nil); err != nil {
		return "", err
	}
	workspaceCleaned = true
	return e.transition(ctx, c, task.StatusAwaitingReview, "runner", "")
}

func (e *Executor) cleanupGitWorkspace(ctx context.Context, log *slog.Logger, c *Claim, ws gitworkspace.Workspace, retErr error) error {
	if errors.Is(retErr, ErrLeaseLost) || leaseOwnershipLost(ctx) {
		return retErr
	}
	if err := e.fenceLeaseOwnership(ctx, c); err != nil {
		return errors.Join(retErr, fmt.Errorf("fence workspace cleanup: %w", err))
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.leaseOperationTimeout())
	defer cancel()
	if retErr != nil && !errors.Is(retErr, ErrAttemptFailed) {
		current, err := e.Tasks.Get(cleanupCtx, c.TaskID)
		if err != nil || !current.Status.Terminal() {
			return retErr
		}
	}
	if err := e.Workspaces.Remove(cleanupCtx, ws); err != nil {
		log.LogAttrs(ctx, slog.LevelWarn, "workspace cleanup failed",
			slog.String("event", "runner_workspace_cleanup_failed"),
			slog.String("error", err.Error()),
		)
		return errors.Join(retErr, fmt.Errorf("workspace cleanup: %w", err))
	}
	if err := e.Tasks.AppendAttemptEvent(cleanupCtx, c.AttemptID,
		"cleanup.completed", "runner", map[string]any{"workspace": "removed"}); err != nil {
		return errors.Join(retErr, fmt.Errorf("record workspace cleanup: %w", err))
	}
	return retErr
}

func stopSession(session agent.Session, events <-chan agent.Event, timeout time.Duration) error {
	cancelCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	drained := make(chan error, 1)
	go func() {
		for range events {
		}
		_, err := session.Wait(context.Background())
		drained <- err
	}()
	started := time.Now()
	cancelErr := session.Cancel(cancelCtx)
	remaining := timeout - time.Since(started)
	var stopErr error
	if remaining <= 0 {
		stopErr = fmt.Errorf("%w: session did not stop within %s",
			ErrSessionStopFailed, timeout)
		remaining = time.Nanosecond
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	var waitErr error
	for {
		select {
		case waitErr = <-drained:
			goto stopped
		case <-timer.C:
			stopErr = fmt.Errorf("%w: session did not stop within %s",
				ErrSessionStopFailed, timeout)
		}
	}

stopped:
	if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
		waitErr = nil
	}
	if cancelErr != nil {
		cancelErr = fmt.Errorf("cancel session: %w", cancelErr)
	}
	if waitErr != nil {
		waitErr = fmt.Errorf("wait for stopped session: %w", waitErr)
	}
	return errors.Join(stopErr, cancelErr, waitErr)
}

const workspaceLostNote = "the workspace was lost before trusted validation completed"

const evidenceEventLimit = 1000

// note non-empty when checks could not run; a failing check is a result, not an error
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
		// checks never ran: infra outcome, must never read as a pass
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
		e.Metrics.observeCheck(r)
		if insertErr != nil || eventErr != nil {
			return
		}
		// persist before announcing: stored exit code is the record
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
		return "", context.Cause(ctx)
	}

	counts := map[validation.Status]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	// failed = a check failed; error = checks could not all run with none failed
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
			// agent-claimed commands never gain trusted_execution
			var p struct {
				Command  string `json:"command"`
				ExitCode *int   `json:"exit_code"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Command != "" {
				// no exit code -> unknown; inventing pass/fail would be unmeasured
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

	// attempt started_at, not task start: task start folds earlier attempts in
	var duration *int64
	if startedAt, err := e.Store.AttemptStartedAt(ctx, c.AttemptID); err != nil {
		return fmt.Errorf("evidence: %w", err)
	} else if startedAt != nil {
		d := int64(time.Since(*startedAt).Seconds())
		duration = &d
	}
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

// idempotency key scoped to attempt so an interrupted owner's replay can't double-apply
func (e *Executor) transition(ctx context.Context, c *Claim, to task.Status, source, reason string) (task.Status, error) {
	if err := e.fenceLeaseOwnership(ctx, c); err != nil {
		return "", fmt.Errorf("fence transition to %s: %w", to, err)
	}
	transitionCtx, cancel := context.WithTimeout(ctx, e.leaseOperationTimeout())
	defer cancel()
	_, err := e.Tasks.Transition(transitionCtx, c.TaskID, task.TransitionParams{
		To:             to,
		Source:         source,
		Reason:         reason,
		IdempotencyKey: fmt.Sprintf("attempt:%s:to:%s", c.AttemptID, to),
	})
	if err != nil {
		return "", fmt.Errorf("transition to %s: %w", to, err)
	}
	e.Metrics.observeTransition(to, c.TaskCreatedAt)
	return to, nil
}

func (e *Executor) append(ctx context.Context, c *Claim, eventType, source string, payload any) error {
	if err := e.Tasks.AppendAttemptEvent(ctx, c.AttemptID, eventType, source, payload); err != nil {
		return fmt.Errorf("append %s: %w", eventType, err)
	}
	return nil
}

func (e *Executor) failTask(ctx context.Context, c *Claim, code, message string) error {
	if err := e.fenceLeaseOwnership(ctx, c); err != nil {
		return fmt.Errorf("fence task failure (%s): %w", code, err)
	}
	failCtx, cancel := context.WithTimeout(ctx, e.leaseOperationTimeout())
	defer cancel()
	_, err := e.Tasks.Transition(failCtx, c.TaskID, task.TransitionParams{
		To:             task.StatusFailed,
		Source:         "runner",
		FailureCode:    code,
		FailureMessage: message,
		IdempotencyKey: fmt.Sprintf("attempt:%s:fail:%s", c.AttemptID, code),
	})
	if err != nil {
		return fmt.Errorf("fail task (%s): %w", code, err)
	}
	e.Metrics.observeFailure(code)
	e.Metrics.observeTransition(task.StatusFailed, c.TaskCreatedAt)
	return fmt.Errorf("%w: %s: %s", ErrAttemptFailed, code, message)
}
