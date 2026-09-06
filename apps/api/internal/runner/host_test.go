package runner

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func TestHostWorksQueueUntilStopped(t *testing.T) {
	db, s, ts := testStores(t)
	tk := mustCreateTask(t, ts)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	host := &Host{
		Store:         s,
		Executor:      testExecutor(db, s, ts),
		Logger:        logger,
		RunnerType:    "process",
		HostnameOrPod: "host-test",
		Lease:         time.Minute,
		Heartbeat:     25 * time.Millisecond,
		LostAfter:     10 * time.Minute,
		Poll:          10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx) }()

	deadline := time.After(10 * time.Second)
	for {
		got, err := ts.Get(context.Background(), tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == task.StatusCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("task never completed; status = %s", got.Status)
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("host.Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop on cancel")
	}

	var status string
	if err := db.QueryRowContext(context.Background(), `
		SELECT status FROM runners WHERE hostname_or_pod = 'host-test'`).
		Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "offline" {
		t.Errorf("runner status after shutdown = %s, want offline", status)
	}
}

func TestLostRunnerReportedOnTimeline(t *testing.T) {
	db, s, ts := testStores(t)
	mustCreateTask(t, ts)
	r := mustRegister(t, s)
	ctx := context.Background()

	claim, err := s.Claim(ctx, r.ID, time.Hour)
	if err != nil || claim == nil {
		t.Fatalf("claim = %v, %v", claim, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE runners SET last_heartbeat_at = now() - interval '10 minutes'
		WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}

	lost, err := s.MarkLost(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0].ID != r.ID {
		t.Fatalf("lost = %+v, want runner %s", lost, r.ID)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Host{Store: s, Executor: &Executor{Tasks: ts}, Logger: logger}
	h.reportLoss(ctx, logger, lost[0])

	var n int
	err = db.QueryRowContext(ctx, `
		SELECT count(*) FROM activity_events
		WHERE task_attempt_id = $1 AND event_type = 'runner.lost'`,
		claim.AttemptID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runner.lost events = %d, want 1", n)
	}
}

func TestHostExitsAfterMaxTasks(t *testing.T) {
	db, s, ts := testStores(t)
	tk := mustCreateTask(t, ts)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	host := &Host{
		Store:         s,
		Executor:      testExecutor(db, s, ts),
		Logger:        logger,
		RunnerType:    "kubernetes",
		HostnameOrPod: "one-shot-test",
		Lease:         time.Minute,
		Heartbeat:     25 * time.Millisecond,
		LostAfter:     10 * time.Minute,
		Poll:          10 * time.Millisecond,
		MaxTasks:      1,
	}

	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("host.Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("capped host did not exit after its task")
	}

	got, err := ts.Get(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Errorf("task status = %s, want completed", got.Status)
	}

	var status, runnerType string
	if err := db.QueryRowContext(context.Background(), `
		SELECT status, runner_type FROM runners
		WHERE hostname_or_pod = 'one-shot-test'`).
		Scan(&status, &runnerType); err != nil {
		t.Fatal(err)
	}
	if status != "offline" {
		t.Errorf("runner status after exit = %s, want offline", status)
	}
	if runnerType != "kubernetes" {
		t.Errorf("runner_type = %s, want kubernetes", runnerType)
	}
}

func TestHostIdleExit(t *testing.T) {
	db, s, ts := testStores(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	host := &Host{
		Store:         s,
		Executor:      testExecutor(db, s, ts),
		Logger:        logger,
		RunnerType:    "kubernetes",
		HostnameOrPod: "idle-exit-test",
		Lease:         time.Minute,
		Heartbeat:     25 * time.Millisecond,
		LostAfter:     10 * time.Minute,
		Poll:          10 * time.Millisecond,
		IdleExit:      100 * time.Millisecond,
	}

	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("host.Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("idle host did not exit")
	}
}
