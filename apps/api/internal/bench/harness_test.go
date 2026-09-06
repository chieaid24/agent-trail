package bench

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// uncapped pool would exhaust max_connections under a 10k-delivery run
const benchPoolSize = 50

func guard(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENT_TRAIL_BENCH") == "" {
		t.Skip("benchmark: set AGENT_TRAIL_BENCH=1 (run via scripts/bench.sh)")
	}
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	guard(t)
	db := dbtest.Open(t)
	db.SetMaxOpenConns(benchPoolSize)
	return db
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fleet struct {
	cancel context.CancelFunc
	done   chan error
	n      int
}

func startFleet(db *sql.DB, s *runner.Store, ts *task.Store, n int, adapter agent.Adapter, lease time.Duration, name string) *fleet {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fleet{cancel: cancel, done: make(chan error, n), n: n}
	for i := 0; i < n; i++ {
		host := &runner.Host{
			Store: s,
			Executor: &runner.Executor{
				Tasks:         ts,
				Store:         s,
				Validations:   validation.NewStore(db),
				Evidence:      evidence.NewStore(db),
				Adapter:       adapter,
				Logger:        discardLogger(),
				LeaseDuration: lease,
			},
			Logger:        discardLogger(),
			RunnerType:    "process",
			HostnameOrPod: fmt.Sprintf("%s-%02d", name, i),
			Lease:         lease,
			Heartbeat:     5 * time.Second,
			LostAfter:     10 * time.Minute,
			Poll:          25 * time.Millisecond,
		}
		go func() { f.done <- host.Run(ctx) }()
	}
	return f
}

func (f *fleet) stop(t *testing.T) {
	t.Helper()
	f.cancel()
	deadline := time.After(30 * time.Second)
	for i := 0; i < f.n; i++ {
		select {
		case err := <-f.done:
			if err != nil {
				t.Errorf("host.Run = %v", err)
			}
		case <-deadline:
			t.Fatal("fleet did not stop within 30s")
		}
	}
}

func queryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).
		Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func waitInt(t *testing.T, db *sql.DB, query string, want int, timeout time.Duration, what string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := queryInt(t, db, query, args...)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: got %d, want %d after %s", what, got, want, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func createTasks(t *testing.T, ts *task.Store, n int, gen func(i int) task.CreateParams) []task.Task {
	t.Helper()
	tasks := make([]task.Task, 0, n)
	for i := 0; i < n; i++ {
		tk, err := ts.Create(context.Background(), gen(i))
		if err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		tasks = append(tasks, tk)
	}
	return tasks
}

// second agent.started on one attempt means two runners executed it
func assertNoDoubleAssignment(t *testing.T, db *sql.DB) {
	t.Helper()
	doubled := queryInt(t, db, `
		SELECT count(*) FROM (
			SELECT task_attempt_id FROM activity_events
			WHERE event_type = 'agent.started'
			GROUP BY task_attempt_id
			HAVING count(*) > 1) d`)
	if doubled != 0 {
		t.Errorf("attempts with more than one agent.started = %d, want 0", doubled)
	}
}

func assertLeasesReleased(t *testing.T, db *sql.DB) {
	t.Helper()
	held := queryInt(t, db,
		`SELECT count(*) FROM task_attempts WHERE lease_owner IS NOT NULL`)
	if held != 0 {
		t.Errorf("attempts still holding a lease = %d, want 0", held)
	}
}

type stageTimings struct {
	queueWait    []time.Duration
	provisioning []time.Duration
}

func collectStageTimings(t *testing.T, db *sql.DB) stageTimings {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT
			min(e."timestamp") FILTER (WHERE e.event_type = 'task.queued'),
			min(e."timestamp") FILTER (WHERE e.event_type = 'workspace.provisioning'),
			min(e."timestamp") FILTER (WHERE e.event_type = 'workspace.ready')
		FROM tasks t
		JOIN task_attempts a ON a.task_id = t.id
		JOIN activity_events e ON e.task_attempt_id = a.id
		GROUP BY t.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out stageTimings
	for rows.Next() {
		var queued, provisioning, ready sql.NullTime
		if err := rows.Scan(&queued, &provisioning, &ready); err != nil {
			t.Fatal(err)
		}
		if queued.Valid && provisioning.Valid {
			out.queueWait = append(out.queueWait,
				provisioning.Time.Sub(queued.Time))
		}
		if provisioning.Valid && ready.Valid {
			out.provisioning = append(out.provisioning,
				ready.Time.Sub(provisioning.Time))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(p/100*float64(len(sorted))+0.5) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func reportDurations(t *testing.T, name string, ds []time.Duration) {
	t.Helper()
	if len(ds) == 0 {
		t.Logf("bench %s: no samples", name)
		return
	}
	t.Logf("bench %s: n=%d p50=%s p95=%s p99=%s max=%s",
		name, len(ds),
		percentile(ds, 50).Round(time.Microsecond),
		percentile(ds, 95).Round(time.Microsecond),
		percentile(ds, 99).Round(time.Microsecond),
		percentile(ds, 100).Round(time.Microsecond))
}
