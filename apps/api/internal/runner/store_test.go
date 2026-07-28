package runner

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

func testStores(t *testing.T) (*sql.DB, *Store, *task.Store) {
	t.Helper()
	db := dbtest.Open(t)
	return db, NewStore(db), task.NewStore(db)
}

func mustRegister(t *testing.T, s *Store) Runner {
	t.Helper()
	r, err := s.Register(context.Background(), RegisterParams{
		Type: "process", HostnameOrPod: "test-host",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustCreateTask(t *testing.T, ts *task.Store) task.Task {
	t.Helper()
	tk, err := ts.Create(context.Background(), task.CreateParams{
		Title:        "leasing test task",
		Instructions: "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// expireLease forces an attempt's lease into the past: deterministic lease
// expiry without sleeping through real durations.
func expireLease(t *testing.T, db *sql.DB, attemptID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		UPDATE task_attempts
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, attemptID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegisterDefaultsAndHeartbeat(t *testing.T) {
	_, s, _ := testStores(t)
	r := mustRegister(t, s)

	if r.Status != "online" || r.Capacity != 1 || r.Type != "process" {
		t.Errorf("runner = %+v, want online process capacity 1", r)
	}
	if err := s.Heartbeat(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(context.Background(),
		"00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("heartbeat unknown runner: %v, want ErrRunnerNotFound", err)
	}
}

func TestHeartbeatRevivesLostButNotOffline(t *testing.T) {
	db, s, _ := testStores(t)
	ctx := context.Background()

	lost := mustRegister(t, s)
	if _, err := db.ExecContext(ctx,
		`UPDATE runners SET status = 'lost' WHERE id = $1`, lost.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, lost.ID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM runners WHERE id = $1`, lost.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "online" {
		t.Errorf("lost runner after heartbeat = %s, want online", status)
	}

	off := mustRegister(t, s)
	if err := s.MarkOffline(ctx, off.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, off.ID); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("heartbeat offline runner: %v, want ErrRunnerNotFound", err)
	}
}

func TestClaimLeasesQueuedTaskOnce(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("claim returned nil, want the queued task")
	}
	if c.TaskID != tk.ID || c.TaskStatus != task.StatusQueued ||
		c.AttemptNumber != 1 || c.Instructions != "do the thing" {
		t.Errorf("claim = %+v", c)
	}
	if !c.LeaseExpiresAt.After(time.Now().Add(30 * time.Second)) {
		t.Errorf("lease expiry %v not ~1m out", c.LeaseExpiresAt)
	}

	var runnerID, leaseOwner string
	err = db.QueryRowContext(ctx, `
		SELECT runner_id, lease_owner FROM task_attempts WHERE id = $1`,
		c.AttemptID).Scan(&runnerID, &leaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if runnerID != r.ID || leaseOwner != r.ID {
		t.Errorf("runner_id/lease_owner = %s/%s, want %s", runnerID, leaseOwner, r.ID)
	}

	// The live lease hides the attempt from further claims.
	again, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("second claim = %+v, want nil", again)
	}
}

// TestConcurrentClaimsHaveOneWinner is the "only one runner ever owns an
// attempt" acceptance criterion: many runners race one queued task.
func TestConcurrentClaimsHaveOneWinner(t *testing.T) {
	_, s, ts := testStores(t)
	ctx := context.Background()
	mustCreateTask(t, ts)

	const racers = 8
	runners := make([]Runner, racers)
	for i := range runners {
		runners[i] = mustRegister(t, s)
	}

	var wg sync.WaitGroup
	claims := make([]*Claim, racers)
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claims[i], errs[i] = s.Claim(ctx, runners[i].ID, time.Minute)
		}(i)
	}
	wg.Wait()

	winners := 0
	for i := 0; i < racers; i++ {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if claims[i] != nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
}

// TestExpiredLeaseIsRecoverable is the runner-loss acceptance criterion:
// the lease expires and another runner claims the same attempt.
func TestExpiredLeaseIsRecoverable(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	dead := mustRegister(t, s)
	successor := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	first, err := s.Claim(ctx, dead.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("first claim returned nil")
	}
	// Simulate the dead runner mid-flight: task advanced, then silence.
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning} {
		if _, err := ts.Transition(ctx, tk.ID, task.TransitionParams{
			To: to, Source: "runner",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Live lease: not claimable.
	if c, err := s.Claim(ctx, successor.ID, time.Minute); err != nil || c != nil {
		t.Fatalf("claim against live lease = %+v, %v; want nil, nil", c, err)
	}

	expireLease(t, db, first.AttemptID)
	second, err := s.Claim(ctx, successor.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatal("claim after lease expiry returned nil, want recovery")
	}
	if second.AttemptID != first.AttemptID {
		t.Errorf("recovered attempt %s, want %s", second.AttemptID, first.AttemptID)
	}
	if second.TaskStatus != task.StatusPlanning {
		t.Errorf("recovered status = %s, want planning", second.TaskStatus)
	}

	// The stale owner must not extend the lease it lost.
	if err := s.ExtendLease(ctx, first.AttemptID, dead.ID, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale extend: %v, want ErrLeaseLost", err)
	}
}

func TestExtendAndReleaseLease(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if err := s.ExtendLease(ctx, c.AttemptID, r.ID, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	var expiry time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT lease_expires_at FROM task_attempts WHERE id = $1`,
		c.AttemptID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if !expiry.After(time.Now().Add(90 * time.Second)) {
		t.Errorf("extended expiry %v not ~2m out", expiry)
	}

	// An expired lease cannot be extended (it may belong to someone else).
	expireLease(t, db, c.AttemptID)
	if err := s.ExtendLease(ctx, c.AttemptID, r.ID, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("extend expired: %v, want ErrLeaseLost", err)
	}

	// Release clears both lease fields but keeps runner_id for history.
	if _, err := db.ExecContext(ctx, `
		UPDATE task_attempts
		SET lease_expires_at = now() + interval '1 minute' WHERE id = $1`,
		c.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseLease(ctx, c.AttemptID, r.ID); err != nil {
		t.Fatal(err)
	}
	var owner, expiresAt sql.NullString
	var runnerID sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT lease_owner, lease_expires_at::text, runner_id
		FROM task_attempts WHERE id = $1`, c.AttemptID).
		Scan(&owner, &expiresAt, &runnerID); err != nil {
		t.Fatal(err)
	}
	if owner.Valid || expiresAt.Valid {
		t.Errorf("lease not cleared: owner=%v expires=%v", owner, expiresAt)
	}
	if !runnerID.Valid || runnerID.String != r.ID {
		t.Errorf("runner_id = %v, want %s preserved", runnerID, r.ID)
	}
	if err := s.ReleaseLease(ctx, c.AttemptID, r.ID); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("double release: %v, want ErrLeaseLost", err)
	}
}

func TestMarkLostFlagsOnlyStaleOnlineRunners(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()

	stale := mustRegister(t, s)
	fresh := mustRegister(t, s)
	mustCreateTask(t, ts)
	c, err := s.Claim(ctx, stale.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE runners SET last_heartbeat_at = now() - interval '10 minutes'
		WHERE id = $1`, stale.ID); err != nil {
		t.Fatal(err)
	}

	lost, err := s.MarkLost(ctx, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0].ID != stale.ID {
		t.Fatalf("lost = %+v, want just %s", lost, stale.ID)
	}
	if lost[0].Status != "lost" {
		t.Errorf("lost status = %s", lost[0].Status)
	}

	// Second reap: nothing newly lost (detection happens exactly once).
	again, err := s.MarkLost(ctx, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second reap = %+v, want empty", again)
	}

	var freshStatus string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM runners WHERE id = $1`, fresh.ID).Scan(&freshStatus); err != nil {
		t.Fatal(err)
	}
	if freshStatus != "online" {
		t.Errorf("fresh runner = %s, want online", freshStatus)
	}

	// The lost runner's leased attempts are reportable on their timelines.
	ids, err := s.LeasedAttemptIDs(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != c.AttemptID {
		t.Fatalf("leased attempts = %v, want [%s]", ids, c.AttemptID)
	}
}

func TestRecordAttemptPublishFieldsFirstWriteWins(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	tk := mustCreateTask(t, ts)
	r := mustRegister(t, s)
	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}

	base := "1111111111111111111111111111111111111111"
	final := "2222222222222222222222222222222222222222"
	if err := s.RecordAttemptBase(ctx, c.AttemptID, base); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFinalCommit(ctx, c.AttemptID, final); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPullRequest(ctx, c.AttemptID, 8); err != nil {
		t.Fatal(err)
	}
	// Replays keep the first values.
	if err := s.RecordFinalCommit(ctx, c.AttemptID,
		"3333333333333333333333333333333333333333"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPullRequest(ctx, c.AttemptID, 9); err != nil {
		t.Fatal(err)
	}

	var gotBase, gotFinal string
	var gotPR int64
	err = db.QueryRow(`
		SELECT base_commit_sha, final_commit_sha, pull_request_number
		FROM task_attempts WHERE id = $1`, c.AttemptID).
		Scan(&gotBase, &gotFinal, &gotPR)
	if err != nil {
		t.Fatal(err)
	}
	if gotBase != base || gotFinal != final || gotPR != 8 {
		t.Fatalf("stored = %s, %s, %d", gotBase, gotFinal, gotPR)
	}
	_ = tk
}
