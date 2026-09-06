package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const concurrentAgents = 20

func TestConcurrentAgents20(t *testing.T) {
	db := openDB(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s := runner.NewStore(db)
	ts := task.NewStore(db)

	adapter := &scriptAdapter{barrier: newBarrier(concurrentAgents)}
	createTasks(t, ts, concurrentAgents, func(i int) task.CreateParams {
		return task.CreateParams{
			Title:        fmt.Sprintf("bench concurrent task %02d", i),
			Instructions: modeBarrier,
		}
	})

	start := time.Now()
	f := startFleet(db, s, ts, concurrentAgents, adapter, time.Minute, "bench-conc")

	select {
	case <-adapter.barrier.release:
	case <-time.After(2 * time.Minute):
		t.Fatal("barrier never released: fewer than 20 sessions ran concurrently")
	}
	allStarted := time.Since(start)

	waitInt(t, db, `SELECT count(*) FROM tasks WHERE status = 'completed'`,
		concurrentAgents, 2*time.Minute, "completed tasks")
	drain := time.Since(start)
	f.stop(t)

	assertNoDoubleAssignment(t, db)
	assertLeasesReleased(t, db)

	rows, err := db.QueryContext(context.Background(), `
		SELECT payload_json->>'workspace' FROM activity_events
		WHERE event_type = 'file.changed' AND source = 'agent'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	workspaces := map[string]bool{}
	for rows.Next() {
		var ws string
		if err := rows.Scan(&ws); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(ws, tmp+string(filepath.Separator)) {
			t.Errorf("workspace %q outside benchmark TMPDIR %q", ws, tmp)
		}
		workspaces[ws] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != concurrentAgents {
		t.Errorf("distinct workspaces = %d, want %d", len(workspaces), concurrentAgents)
	}
	leftovers, err := filepath.Glob(filepath.Join(tmp, "agent-trail-attempt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("workspaces left on disk after completion = %d, want 0", len(leftovers))
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("bench TMPDIR vanished: %v", err)
	}

	timings := collectStageTimings(t, db)
	t.Logf("bench concurrent: sessions=%d all_started=%s total=%s distinct_workspaces=%d",
		concurrentAgents, allStarted.Round(time.Millisecond),
		drain.Round(time.Millisecond), len(workspaces))
	reportDurations(t, "concurrent queue wait", timings.queueWait)
	reportDurations(t, "concurrent provisioning", timings.provisioning)
}
