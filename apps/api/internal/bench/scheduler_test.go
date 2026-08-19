package bench

import (
	"fmt"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const (
	schedulerTasks   = 100
	schedulerRunners = 20
)

// TestScheduler100Tasks20Runners drains 100 queued tasks with 20 concurrent
// runners claiming from the Postgres queue (FOR UPDATE SKIP LOCKED leases)
// and verifies no attempt was ever executed twice
// (docs/testing/testing-strategy.md "Load tests > Scheduler").
func TestScheduler100Tasks20Runners(t *testing.T) {
	db := openDB(t)
	t.Setenv("TMPDIR", t.TempDir())
	s := runner.NewStore(db)
	ts := task.NewStore(db)

	createTasks(t, ts, schedulerTasks, func(i int) task.CreateParams {
		return task.CreateParams{
			Title:        fmt.Sprintf("bench scheduler task %03d", i),
			Instructions: "bench scheduler load",
		}
	})

	start := time.Now()
	f := startFleet(db, s, ts, schedulerRunners, agent.NewFake(),
		time.Minute, "bench-sched")
	waitInt(t, db, `SELECT count(*) FROM tasks WHERE status = 'completed'`,
		schedulerTasks, 5*time.Minute, "completed tasks")
	drain := time.Since(start)
	f.stop(t)

	assertNoDoubleAssignment(t, db)
	assertLeasesReleased(t, db)
	if started := queryInt(t, db, `
		SELECT count(DISTINCT task_attempt_id) FROM activity_events
		WHERE event_type = 'agent.started'`); started != schedulerTasks {
		t.Errorf("attempts that ran an agent = %d, want %d", started, schedulerTasks)
	}
	runnersUsed := queryInt(t, db, `
		SELECT count(DISTINCT runner_id) FROM task_attempts
		WHERE runner_id IS NOT NULL`)

	timings := collectStageTimings(t, db)
	t.Logf("bench scheduler: tasks=%d runners=%d drain=%s (%.1f tasks/s) distinct_executing_runners=%d",
		schedulerTasks, schedulerRunners, drain.Round(time.Millisecond),
		float64(schedulerTasks)/drain.Seconds(), runnersUsed)
	reportDurations(t, "scheduler queue wait", timings.queueWait)
	reportDurations(t, "scheduler provisioning", timings.provisioning)
}
