// Package runner emits the metrics.
package runner

import (
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// Metrics holds the runner and task instruments the worker emits.
type Metrics struct {
	activeTasks        *observability.UpDownCounter
	queueWait          *observability.Histogram
	taskDuration       *observability.Histogram
	taskStatus         *observability.Counter
	taskFailures       *observability.Counter
	heartbeatsMissed   *observability.Counter
	commandDuration    *observability.Histogram
	commandExit        *observability.Counter
	validationDuration *observability.Histogram
	logBytes           *observability.Counter
	workspaceCleanup   *observability.Counter
}

// NewMetrics registers the runner instruments on reg.
func NewMetrics(reg *observability.Registry) *Metrics {
	return &Metrics{
		activeTasks: reg.UpDown("agent_trail_runner_active_tasks",
			"Task attempts this runner is executing right now."),
		queueWait: reg.Histogram("agent_trail_task_queue_wait_seconds",
			"Seconds from task creation to its first runner claim.",
			1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600),
		taskDuration: reg.Histogram("agent_trail_task_duration_seconds",
			"Seconds from task creation to its resting state.",
			10, 30, 60, 120, 300, 600, 1200, 1800, 3600, 7200),
		taskStatus: reg.Counter("agent_trail_task_status_total",
			"Runner-driven task status transitions, labelled by target status."),
		taskFailures: reg.Counter("agent_trail_task_failures_total",
			"Tasks failed by the runner, labelled by failure code."),
		heartbeatsMissed: reg.Counter("agent_trail_runner_heartbeats_missed_total",
			"Runners marked lost after missing their heartbeat window."),
		commandDuration: reg.Histogram("agent_trail_command_duration_seconds",
			"Trusted validation command durations.",
			0.1, 0.5, 1, 5, 15, 30, 60, 120, 300, 600),
		commandExit: reg.Counter("agent_trail_command_exit_total",
			"Trusted validation command outcomes, labelled by status."),
		validationDuration: reg.Histogram("agent_trail_validation_duration_seconds",
			"End-to-end trusted validation durations.",
			1, 5, 15, 30, 60, 120, 300, 600, 1200),
		logBytes: reg.Counter("agent_trail_log_bytes_total",
			"Agent event payload bytes streamed into the timeline."),
		workspaceCleanup: reg.Counter("agent_trail_workspace_cleanup_total",
			"Workspace cleanup attempts, labelled by outcome."),
	}
}

func (m *Metrics) taskStarted() {
	if m == nil {
		return
	}
	m.activeTasks.Add(1)
}

func (m *Metrics) taskFinished() {
	if m == nil {
		return
	}
	m.activeTasks.Add(-1)
}

func (m *Metrics) observeQueueWait(d time.Duration) {
	if m == nil {
		return
	}
	m.queueWait.Observe(d.Seconds())
}

// observeTransition records duration once at awaiting_review or failed.
func (m *Metrics) observeTransition(to task.Status, createdAt time.Time) {
	if m == nil {
		return
	}
	m.taskStatus.Inc(observability.Label{Key: "status", Value: string(to)})
	if to != task.StatusAwaitingReview && to != task.StatusFailed {
		return
	}
	if createdAt.IsZero() {
		return
	}
	m.taskDuration.Observe(time.Since(createdAt).Seconds())
}

func (m *Metrics) observeFailure(code string) {
	if m == nil {
		return
	}
	m.taskFailures.Inc(observability.Label{Key: "code", Value: code})
}

func (m *Metrics) observeLostRunners(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.heartbeatsMissed.Add(int64(n))
}

func (m *Metrics) observeCheck(r validation.Result) {
	if m == nil {
		return
	}
	m.commandDuration.Observe(float64(r.DurationMS) / 1000)
	m.commandExit.Inc(observability.Label{Key: "status", Value: string(r.Status)})
}

func (m *Metrics) observeValidation(d time.Duration) {
	if m == nil {
		return
	}
	m.validationDuration.Observe(d.Seconds())
}

func (m *Metrics) observeLogBytes(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.logBytes.Add(int64(n))
}

func (m *Metrics) observeCleanup(outcome string) {
	if m == nil {
		return
	}
	m.workspaceCleanup.Inc(observability.Label{Key: "outcome", Value: outcome})
}
