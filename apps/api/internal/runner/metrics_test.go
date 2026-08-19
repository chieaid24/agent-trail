package runner

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

func TestMetricsEmitRunnerContract(t *testing.T) {
	reg := observability.NewRegistry()
	m := NewMetrics(reg)
	m.taskStarted()
	m.observeQueueWait(3 * time.Second)
	m.observeTransition(task.StatusAwaitingReview, time.Now().Add(-time.Minute))
	m.observeFailure("agent_failed")
	m.observeLostRunners(2)
	m.observeCheck(validation.Result{DurationMS: 1250, Status: validation.StatusPassed})
	m.observeValidation(2 * time.Second)
	m.observeLogBytes(42)
	m.observeCleanup("removed")

	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"agent_trail_runner_active_tasks 1",
		"agent_trail_task_queue_wait_seconds_count 1",
		"agent_trail_task_duration_seconds_count 1",
		`agent_trail_task_status_total{status="awaiting_review"} 1`,
		`agent_trail_task_failures_total{code="agent_failed"} 1`,
		"agent_trail_runner_heartbeats_missed_total 2",
		"agent_trail_command_duration_seconds_count 1",
		`agent_trail_command_exit_total{status="passed"} 1`,
		"agent_trail_validation_duration_seconds_count 1",
		"agent_trail_log_bytes_total 42",
		`agent_trail_workspace_cleanup_total{outcome="removed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q:\n%s", want, body)
		}
	}

	m.taskFinished()
	rec = httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "agent_trail_runner_active_tasks 0") {
		t.Fatalf("active task gauge did not return to zero:\n%s", rec.Body.String())
	}
}
