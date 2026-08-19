package bench

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const (
	cleanupTasks     = 100
	cleanupCancelled = 50
	cleanupRunners   = 20
)

// TestCleanup100ForcedFailures forces 100 attempts off the happy path - 50
// cancelled through the store mid-session (the API cancellation path) and
// 50 failed by the agent session - then verifies every workspace was
// removed and every lease released
// (docs/testing/benchmarks.md "Cleanup").
func TestCleanup100ForcedFailures(t *testing.T) {
	db := openDB(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s := runner.NewStore(db)
	ts := task.NewStore(db)

	// Cancel-mode sessions carry a key resolving to their own task id; the
	// onStarted hook cancels that task while the session is mid-flight and
	// its workspace exists, exactly like a user hitting the cancel endpoint.
	var (
		mu        sync.Mutex
		taskByKey = map[int]string{}
	)
	adapter := &scriptAdapter{
		onStarted: func(instructions string) {
			for _, field := range strings.Fields(instructions) {
				keyStr, ok := strings.CutPrefix(field, "key=")
				if !ok {
					continue
				}
				key, err := strconv.Atoi(keyStr)
				if err != nil {
					return
				}
				mu.Lock()
				id := taskByKey[key]
				mu.Unlock()
				if id == "" {
					return
				}
				_, _ = ts.Cancel(context.Background(), id,
					"benchmark forced cancellation")
				return
			}
		},
	}

	created := createTasks(t, ts, cleanupTasks, func(i int) task.CreateParams {
		instructions := modeFail
		if i < cleanupCancelled {
			instructions = fmt.Sprintf("%s key=%d", modeCancel, i)
		}
		return task.CreateParams{
			Title:        fmt.Sprintf("bench cleanup task %03d", i),
			Instructions: instructions,
		}
	})
	mu.Lock()
	for i := 0; i < cleanupCancelled; i++ {
		taskByKey[i] = created[i].ID
	}
	mu.Unlock()

	start := time.Now()
	f := startFleet(db, s, ts, cleanupRunners, adapter, time.Minute, "bench-clean")
	waitInt(t, db,
		`SELECT count(*) FROM tasks WHERE status IN ('cancelled', 'failed')`,
		cleanupTasks, 5*time.Minute, "terminal tasks")
	drain := time.Since(start)
	f.stop(t)

	cancelled := queryInt(t, db,
		`SELECT count(*) FROM tasks WHERE status = 'cancelled'`)
	failed := queryInt(t, db,
		`SELECT count(*) FROM tasks WHERE status = 'failed'`)
	if cancelled != cleanupCancelled || failed != cleanupTasks-cleanupCancelled {
		t.Errorf("terminal split: cancelled=%d failed=%d, want %d and %d",
			cancelled, failed, cleanupCancelled, cleanupTasks-cleanupCancelled)
	}

	// Every terminal failure must be machine-readable
	// (docs/operations/reliability-targets.md).
	if bare := queryInt(t, db, `
		SELECT count(*) FROM tasks WHERE status = 'failed'
		AND (failure_code IS NULL OR failure_message IS NULL)`); bare != 0 {
		t.Errorf("failed tasks without failure_code/message = %d, want 0", bare)
	}

	// The cleanup contract: no workspace survives, no lease survives, and
	// every attempt that provisioned a workspace recorded its removal.
	leftovers, err := filepath.Glob(filepath.Join(tmp, "agent-trail-attempt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("workspaces left on disk = %d, want 0", len(leftovers))
	}
	assertLeasesReleased(t, db)
	provisioned := queryInt(t, db, `
		SELECT count(DISTINCT task_attempt_id) FROM activity_events
		WHERE event_type = 'workspace.ready'`)
	cleaned := queryInt(t, db, `
		SELECT count(DISTINCT task_attempt_id) FROM activity_events
		WHERE event_type = 'cleanup.completed'`)
	if cleaned != provisioned {
		t.Errorf("cleanup.completed attempts = %d, want %d (one per provisioned workspace)",
			cleaned, provisioned)
	}

	t.Logf("bench cleanup: forced=%d (cancelled=%d failed=%d) runners=%d drain=%s cleaned=%d/%d workspaces_left=%d",
		cleanupTasks, cancelled, failed, cleanupRunners,
		drain.Round(time.Millisecond), cleaned, provisioned, len(leftovers))
}
