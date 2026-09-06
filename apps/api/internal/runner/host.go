package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type Host struct {
	Store    *Store
	Executor *Executor
	Logger   *slog.Logger
	Metrics  *Metrics

	RunnerType    string
	HostnameOrPod string
	AttemptID     string

	Lease     time.Duration
	Heartbeat time.Duration
	LostAfter time.Duration
	Poll      time.Duration

	// zero unbounded; capped host exits so a k8s job completes and ttl reclaims it
	MaxTasks int
	// zero never idles out; keeps a one-shot job from hanging on an empty queue
	IdleExit time.Duration
}

// in-flight attempt at shutdown is left mid-status with lease released, ready for recovery
func (h *Host) Run(ctx context.Context) error {
	self, err := h.Store.Register(ctx, RegisterParams{
		Type:          h.RunnerType,
		HostnameOrPod: h.HostnameOrPod,
	})
	if err != nil {
		return err
	}
	log := h.Logger.With(slog.String("runner_id", self.ID))
	log.LogAttrs(ctx, slog.LevelInfo, "runner registered",
		slog.String("event", "runner_registered"),
		slog.String("hostname", h.HostnameOrPod),
	)

	// cap/idle exit end the loop without cancelling ctx; heartbeats need their own stop
	beatCtx, stopBeats := context.WithCancel(ctx)
	defer stopBeats()
	beatsDone := make(chan struct{})
	go func() {
		defer close(beatsDone)
		h.beatAndReap(beatCtx, log, self.ID)
	}()

	executed := 0
	idleSince := time.Now()
	for ctx.Err() == nil {
		var claim *Claim
		var err error
		if h.AttemptID == "" {
			claim, err = h.Store.Claim(ctx, self.ID, h.Lease)
		} else {
			claim, err = h.Store.ClaimAttempt(ctx, self.ID, h.AttemptID, h.Lease)
		}
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.LogAttrs(ctx, slog.LevelError, "claim failed",
				slog.String("event", "runner_claim_failed"),
				slog.String("error", err.Error()),
			)
			sleep(ctx, h.Poll)
			continue
		}
		if claim == nil {
			if h.AttemptID != "" {
				break
			}
			if h.IdleExit > 0 && time.Since(idleSince) >= h.IdleExit {
				log.LogAttrs(ctx, slog.LevelInfo, "idle exit",
					slog.String("event", "runner_idle_exit"),
					slog.Duration("idle_exit", h.IdleExit),
				)
				break
			}
			sleep(ctx, h.Poll)
			continue
		}
		idleSince = time.Now()
		log.LogAttrs(ctx, slog.LevelInfo, "attempt claimed",
			slog.String("event", "runner_attempt_claimed"),
			slog.String("task_id", claim.TaskID),
			slog.String("task_attempt_id", claim.AttemptID),
			slog.String("task_status", string(claim.TaskStatus)),
		)
		// queue wait = creation to first claim; recovered claims not re-counted
		if claim.TaskStatus == task.StatusQueued {
			h.Metrics.observeQueueWait(time.Since(claim.TaskCreatedAt))
			recordQueueWaitSpan(ctx, claim)
		}
		h.Metrics.taskStarted()
		if err := h.Executor.Execute(ctx, self.ID, claim); err != nil {
			log.LogAttrs(ctx, slog.LevelWarn, "attempt did not complete",
				slog.String("event", "runner_attempt_incomplete"),
				slog.String("task_id", claim.TaskID),
				slog.String("task_attempt_id", claim.AttemptID),
				slog.String("error", err.Error()),
			)
		}
		h.Metrics.taskFinished()
		executed++
		if h.MaxTasks > 0 && executed >= h.MaxTasks {
			log.LogAttrs(ctx, slog.LevelInfo, "task cap reached",
				slog.String("event", "runner_task_cap_reached"),
				slog.Int("executed", executed),
			)
			break
		}
	}
	stopBeats()
	<-beatsDone

	offCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Store.MarkOffline(offCtx, self.ID); err != nil {
		return err
	}
	log.LogAttrs(offCtx, slog.LevelInfo, "runner offline",
		slog.String("event", "runner_offline"),
	)
	return nil
}

// any live runner may reap: MarkLost is atomic, a loss is detected once
func (h *Host) beatAndReap(ctx context.Context, log *slog.Logger, runnerID string) {
	ticker := time.NewTicker(h.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := h.Store.Heartbeat(ctx, runnerID); err != nil && ctx.Err() == nil {
			log.LogAttrs(ctx, slog.LevelError, "heartbeat failed",
				slog.String("event", "runner_heartbeat_failed"),
				slog.String("error", err.Error()),
			)
		}
		lost, err := h.Store.MarkLost(ctx, h.LostAfter)
		if err != nil {
			if ctx.Err() == nil {
				log.LogAttrs(ctx, slog.LevelError, "reap failed",
					slog.String("event", "runner_reap_failed"),
					slog.String("error", err.Error()),
				)
			}
			continue
		}
		h.Metrics.observeLostRunners(len(lost))
		for _, r := range lost {
			log.LogAttrs(ctx, slog.LevelWarn, "runner lost",
				slog.String("event", "runner_lost"),
				slog.String("lost_runner_id", r.ID),
				slog.String("lost_hostname", r.HostnameOrPod),
				slog.Time("last_heartbeat_at", r.LastHeartbeatAt),
			)
			h.reportLoss(ctx, log, r)
		}
	}
}

func (h *Host) reportLoss(ctx context.Context, log *slog.Logger, lost Runner) {
	attempts, err := h.Store.LeasedAttemptIDs(ctx, lost.ID)
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "listing lost runner attempts failed",
			slog.String("event", "runner_lost_attempts_failed"),
			slog.String("lost_runner_id", lost.ID),
			slog.String("error", err.Error()),
		)
		return
	}
	for _, attemptID := range attempts {
		err := h.Executor.Tasks.AppendAttemptEvent(ctx, attemptID,
			"runner.lost", "system", map[string]any{
				"runner_id":         lost.ID,
				"hostname_or_pod":   lost.HostnameOrPod,
				"last_heartbeat_at": lost.LastHeartbeatAt.UTC().Format(time.RFC3339),
			})
		if err != nil {
			log.LogAttrs(ctx, slog.LevelError, "runner.lost event failed",
				slog.String("event", "runner_lost_event_failed"),
				slog.String("task_attempt_id", attemptID),
				slog.String("error", err.Error()),
			)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
