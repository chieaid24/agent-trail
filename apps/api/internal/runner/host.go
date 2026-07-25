package runner

import (
	"context"
	"log/slog"
	"time"
)

// Host is one runner process: it registers itself, heartbeats, reaps lost
// runners, and claims and executes attempts until its context ends.
type Host struct {
	Store    *Store
	Executor *Executor
	Logger   *slog.Logger

	// RunnerType and HostnameOrPod identify this runner in the registry.
	RunnerType    string
	HostnameOrPod string

	Lease     time.Duration // claim lease duration
	Heartbeat time.Duration // registry heartbeat and reap cadence
	LostAfter time.Duration // heartbeat staleness that marks a runner lost
	Poll      time.Duration // idle claim-poll interval
}

// Run registers the runner and works the queue until ctx ends, then marks
// the runner offline. An attempt in flight at shutdown is left mid-status
// with its lease released, ready for recovery by another runner.
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

	beatsDone := make(chan struct{})
	go func() {
		defer close(beatsDone)
		h.beatAndReap(ctx, log, self.ID)
	}()

	for ctx.Err() == nil {
		claim, err := h.Store.Claim(ctx, self.ID, h.Lease)
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
			sleep(ctx, h.Poll)
			continue
		}
		log.LogAttrs(ctx, slog.LevelInfo, "attempt claimed",
			slog.String("event", "runner_attempt_claimed"),
			slog.String("task_id", claim.TaskID),
			slog.String("attempt_id", claim.AttemptID),
			slog.String("task_status", string(claim.TaskStatus)),
		)
		if err := h.Executor.Execute(ctx, self.ID, claim); err != nil {
			// The failure is already recorded on the task or the attempt is
			// recoverable; either way this runner moves on.
			log.LogAttrs(ctx, slog.LevelWarn, "attempt did not complete",
				slog.String("event", "runner_attempt_incomplete"),
				slog.String("task_id", claim.TaskID),
				slog.String("attempt_id", claim.AttemptID),
				slog.String("error", err.Error()),
			)
		}
	}
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

// beatAndReap heartbeats the registry and reaps lost runners until ctx ends.
// Any live runner may reap: MarkLost is atomic, so a loss is detected once.
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

// reportLoss writes the loss onto the timeline of every attempt the lost
// runner still leases. The lease itself is untouched: expiry, not runner
// status, makes the attempt claimable again.
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
				slog.String("attempt_id", attemptID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// sleep waits d or until ctx ends, whichever is first.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
