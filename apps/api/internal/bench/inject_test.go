package bench

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/githubfixture"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// Failure-injection matrix (docs/testing/testing-strategy.md "Failure
// injection", issue #13): runner kill, network interruption, database
// restart, S3 timeout, GitHub rate limit, agent hang, full disk. Each test
// injects one fault and asserts the recorded behaviour; the measured
// outcomes live in docs/testing/benchmark-results.md.

// TestInjectRunnerKill kills a runner the hardest way observable from the
// database: the runner claims an attempt and then never heartbeats again -
// the state a SIGKILL leaves behind. The lease must expire on the server
// clock and a second runner must recover the task to completion.
func TestInjectRunnerKill(t *testing.T) {
	db := openDB(t)
	t.Setenv("TMPDIR", t.TempDir())
	s := runner.NewStore(db)
	ts := task.NewStore(db)
	ctx := context.Background()

	tk, err := ts.Create(ctx, task.CreateParams{
		Title:        "bench runner-kill task",
		Instructions: "bench runner kill",
	})
	if err != nil {
		t.Fatal(err)
	}

	victim, err := s.Register(ctx, runner.RegisterParams{
		Type: "process", HostnameOrPod: "bench-kill-victim",
	})
	if err != nil {
		t.Fatal(err)
	}
	const lease = 2 * time.Second
	claim, err := s.Claim(ctx, victim.ID, lease)
	if err != nil || claim == nil {
		t.Fatalf("victim claim = %+v, %v", claim, err)
	}
	killedAt := time.Now()
	// No heartbeat, no executor, no release: the victim is dead.

	f := startFleet(db, s, ts, 1, agent.NewFake(), time.Minute, "bench-kill-rescue")
	waitInt(t, db, `SELECT count(*) FROM tasks WHERE status = 'completed'`,
		1, time.Minute, "recovered task")
	recovery := time.Since(killedAt)
	f.stop(t)

	if recovery < lease {
		t.Errorf("task recovered after %s, before the %s lease expired: "+
			"two runners could have owned the attempt at once", recovery, lease)
	}
	var finalRunner string
	if err := db.QueryRowContext(ctx,
		`SELECT runner_id FROM task_attempts WHERE id = $1`,
		claim.AttemptID).Scan(&finalRunner); err != nil {
		t.Fatal(err)
	}
	if finalRunner == victim.ID {
		t.Errorf("attempt still recorded on the dead runner %s", victim.ID)
	}
	assertLeasesReleased(t, db)
	if got, err := ts.Get(ctx, tk.ID); err != nil || got.Status != task.StatusCompleted {
		t.Errorf("task = %v, %v; want completed", got.Status, err)
	}

	t.Logf("bench inject runner-kill: lease=%s recovery=%s", lease,
		recovery.Round(time.Millisecond))
}

// waitIntTolerant polls like waitInt but tolerates query errors - required
// while the database is being restarted under the test.
func waitIntTolerant(t *testing.T, db *sql.DB, query string, want int, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var n int
		err := db.QueryRowContext(context.Background(), query).Scan(&n)
		if err == nil && n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: got %d (last err %v), want %d after %s",
				what, n, err, want, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestInjectDatabaseRestart restarts the Postgres container while 10
// runners drain 50 tasks. Runners must ride out the outage (claim errors
// retry on the poll interval), interrupted attempts must be recovered
// after lease expiry, and every task must still complete exactly once at
// the task level. Requires BENCH_PG_CONTAINER (set by scripts/bench.sh).
func TestInjectDatabaseRestart(t *testing.T) {
	container := os.Getenv("BENCH_PG_CONTAINER")
	if container == "" {
		guard(t)
		t.Skip("BENCH_PG_CONTAINER not set (run via scripts/bench.sh)")
	}
	db := openDB(t)
	t.Setenv("TMPDIR", t.TempDir())
	s := runner.NewStore(db)
	ts := task.NewStore(db)

	const (
		total   = 50
		runners = 10
		lease   = 10 * time.Second
	)
	createTasks(t, ts, total, func(i int) task.CreateParams {
		return task.CreateParams{
			Title:        fmt.Sprintf("bench db-restart task %02d", i),
			Instructions: "bench database restart",
		}
	})

	start := time.Now()
	f := startFleet(db, s, ts, runners, agent.NewFake(), lease, "bench-dbrestart")

	// Let the drain get going, then pull the database out.
	waitIntTolerant(t, db,
		`SELECT count(*) FROM tasks WHERE status = 'completed'`,
		total/5, time.Minute, "warm-up completions")
	restartAt := time.Now()
	out, err := exec.Command("docker", "restart", "-t", "1", container).CombinedOutput()
	if err != nil {
		t.Fatalf("docker restart %s: %v\n%s", container, err, out)
	}
	// Outage window: from the restart order to the first successful query.
	var outage time.Duration
	for {
		var one int
		if err := db.QueryRowContext(context.Background(),
			`SELECT 1`).Scan(&one); err == nil {
			outage = time.Since(restartAt)
			break
		}
		if time.Since(restartAt) > time.Minute {
			t.Fatal("database did not come back within a minute")
		}
		time.Sleep(50 * time.Millisecond)
	}

	waitIntTolerant(t, db,
		`SELECT count(*) FROM tasks WHERE status = 'completed'`,
		total, 4*time.Minute, "completed tasks after restart")
	drain := time.Since(start)
	f.stop(t)

	assertLeasesReleased(t, db)
	// Attempts interrupted mid-agent-session are re-run after lease expiry
	// (documented at-least-once recovery), so count them rather than
	// forbidding them: task-level exactly-once is the invariant.
	reExecuted := queryInt(t, db, `
		SELECT count(*) FROM (
			SELECT task_attempt_id FROM activity_events
			WHERE event_type = 'agent.started'
			GROUP BY task_attempt_id
			HAVING count(*) > 1) d`)
	if got := queryInt(t, db,
		`SELECT count(*) FROM tasks WHERE status = 'completed'`); got != total {
		t.Errorf("completed tasks = %d, want %d", got, total)
	}

	t.Logf("bench inject db-restart: tasks=%d runners=%d lease=%s outage=%s drain=%s re_executed_attempts=%d",
		total, runners, lease, outage.Round(time.Millisecond),
		drain.Round(time.Millisecond), reExecuted)
}

// stubGitHub is a minimal GitHub API for the real github.Client, with a
// switchable fault mode: "ok" serves the endpoints webhook processing
// needs, "reset" drops every connection mid-request (network
// interruption), "429" answers every request with a rate-limit rejection.
type stubGitHub struct {
	mu   sync.Mutex
	mode string
}

func (g *stubGitHub) setMode(mode string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mode = mode
}

const (
	injectInstallationID = 424242
	injectRepositoryID   = 909
)

func (g *stubGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	mode := g.mode
	g.mu.Unlock()

	switch mode {
	case "reset":
		hj, ok := w.(http.Hijacker)
		if !ok {
			panic("stubGitHub: response writer cannot hijack")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			panic(err)
		}
		_ = conn.Close() // dropped mid-request: the client sees a broken connection
		return
	case "429":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message": "API rate limit exceeded",
		})
		return
	}

	path, method := r.URL.Path, r.Method
	writeOK := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case method == http.MethodPost && strings.HasPrefix(path, "/app/installations/"):
		writeOK(map[string]any{
			"token":      "bench-token",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	case method == http.MethodGet && path == "/installation/repositories":
		writeOK(map[string]any{
			"repositories": []map[string]any{{
				"id":             injectRepositoryID,
				"name":           "bench-repo",
				"full_name":      "acme/bench-repo",
				"default_branch": "main",
				"clone_url":      "https://github.example/acme/bench-repo.git",
				"owner":          map[string]any{"login": "acme"},
			}},
		})
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/bench-repo/collaborators/"):
		writeOK(map[string]any{"permission": "write"})
	case method == http.MethodPost && strings.HasPrefix(path, "/repos/acme/bench-repo/issues/"):
		writeOK(map[string]any{"id": 1})
	case method == http.MethodPost && path == "/repos/acme/bench-repo/check-runs":
		writeOK(map[string]any{"id": 1})
	default:
		http.NotFound(w, r)
	}
}

// injectRig wires the real webhook handler and the real GitHub client to a
// fault-switchable GitHub stub over real HTTP.
type injectRig struct {
	db        *sql.DB
	stub      *stubGitHub
	processor *github.Processor
	webhook   *httptest.Server
	client    *http.Client
	secret    []byte
}

func newInjectRig(t *testing.T) *injectRig {
	t.Helper()
	db := openDB(t)
	stub := &stubGitHub{mode: "ok"}
	stubSrv := httptest.NewServer(stub)
	t.Cleanup(stubSrv.Close)

	key, err := githubfixture.ThrowawayKey()
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewRegistry()
	client, err := github.NewClient("424242", key, stubSrv.URL, metrics)
	if err != nil {
		t.Fatal(err)
	}
	logger := discardLogger()
	store := github.NewStore(db)
	tasks := task.NewStore(db)
	processor := github.NewProcessor(store, tasks, client, logger, metrics)
	secret := []byte("bench-inject-secret")
	webhook := httptest.NewServer(github.NewWebhook(secret, store, processor, logger, metrics))
	t.Cleanup(webhook.Close)

	rig := &injectRig{
		db:        db,
		stub:      stub,
		processor: processor,
		webhook:   webhook,
		client:    &http.Client{Timeout: 30 * time.Second},
		secret:    secret,
	}
	// Seed the installation and wait for the repository sync while the
	// stub is healthy.
	status, ack, _ := postWebhook(t, rig.client, webhook.URL, secret,
		"bench-inject-install", "installation", installationEvent(t, injectInstallationID))
	if status != http.StatusAccepted || ack != "accepted" {
		t.Fatalf("installation seed: status=%d ack=%q", status, ack)
	}
	waitInt(t, db, fmt.Sprintf(
		`SELECT count(*) FROM repositories WHERE github_repository_id = %d AND is_enabled`,
		injectRepositoryID), 1, 10*time.Second, "repository sync")
	return rig
}

// deliver posts one signed task-creating delivery and waits for its ledger
// row to settle, returning the processing status and failure message.
func (rig *injectRig) deliver(t *testing.T, deliveryID string, issue int) (status, failure string, ackLatency time.Duration) {
	t.Helper()
	httpStatus, ack, latency := postWebhook(t, rig.client, rig.webhook.URL,
		rig.secret, deliveryID, "issue_comment",
		runCommandEvent(t, injectInstallationID, injectRepositoryID, issue))
	if httpStatus != http.StatusAccepted || ack != "accepted" {
		t.Fatalf("delivery %s: status=%d ack=%q", deliveryID, httpStatus, ack)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var failureMessage sql.NullString
		err := rig.db.QueryRowContext(context.Background(), `
			SELECT processing_status, failure_message
			FROM github_webhook_deliveries WHERE github_delivery_id = $1`,
			deliveryID).Scan(&status, &failureMessage)
		if err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			return status, failureMessage.String, latency
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery %s still pending after 30s", deliveryID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (rig *injectRig) tasksForIssue(t *testing.T, issue int) int {
	t.Helper()
	return queryInt(t, rig.db,
		`SELECT count(*) FROM tasks WHERE source_issue_number = $1`, issue)
}

// TestInjectNetworkInterruption drops every GitHub connection mid-request
// while a task-creating delivery is processed. The webhook must still ack
// fast (processing is async), the delivery must settle as failed with the
// error recorded, no task may be created - and the next delivery after the
// network returns must succeed with no restart.
func TestInjectNetworkInterruption(t *testing.T) {
	rig := newInjectRig(t)
	const issue = 31

	rig.stub.setMode("reset")
	status, failure, ackLatency := rig.deliver(t, "bench-net-down", issue)
	if status != "failed" {
		t.Errorf("delivery during interruption = %q, want failed", status)
	}
	if failure == "" {
		t.Error("failed delivery recorded no failure message")
	}
	if got := rig.tasksForIssue(t, issue); got != 0 {
		t.Errorf("tasks created during interruption = %d, want 0", got)
	}

	rig.stub.setMode("ok")
	status, _, _ = rig.deliver(t, "bench-net-recover", issue)
	if status != "processed" {
		t.Errorf("delivery after recovery = %q, want processed", status)
	}
	if got := rig.tasksForIssue(t, issue); got != 1 {
		t.Errorf("tasks after recovery = %d, want 1", got)
	}

	t.Logf("bench inject network-interruption: ack_during_outage=%s failed_recorded=%t recovered=%t",
		ackLatency.Round(time.Microsecond), failure != "", status == "processed")
}

// TestInjectGitHubRateLimit answers every GitHub call with 429. The client
// has no retry or backoff by design (docs/adr/0006), so the delivery fails
// with the 429 recorded and no task; processing recovers on the next
// delivery once the limit lifts.
func TestInjectGitHubRateLimit(t *testing.T) {
	rig := newInjectRig(t)
	const issue = 41

	rig.stub.setMode("429")
	status, failure, _ := rig.deliver(t, "bench-rate-hit", issue)
	if status != "failed" {
		t.Errorf("delivery under rate limit = %q, want failed", status)
	}
	if !strings.Contains(failure, "429") {
		t.Errorf("failure message %q does not record the 429", failure)
	}
	if got := rig.tasksForIssue(t, issue); got != 0 {
		t.Errorf("tasks created under rate limit = %d, want 0", got)
	}

	rig.stub.setMode("ok")
	status, _, _ = rig.deliver(t, "bench-rate-recover", issue)
	if status != "processed" {
		t.Errorf("delivery after limit lifted = %q, want processed", status)
	}
	if got := rig.tasksForIssue(t, issue); got != 1 {
		t.Errorf("tasks after limit lifted = %d, want 1", got)
	}

	t.Logf("bench inject rate-limit: failed_with_429_recorded=%t recovered=%t",
		strings.Contains(failure, "429"), status == "processed")
}

// TestInjectS3Timeout documents the S3 row of the matrix: there is nothing
// to inject into yet.
func TestInjectS3Timeout(t *testing.T) {
	guard(t)
	t.Skip("not applicable: no object-storage code path exists yet - logs, " +
		"evidence, and validation results live in Postgres " +
		"(docs/architecture/logs-and-streaming.md); add this injection when " +
		"log offload to object storage lands")
}

// TestInjectAgentHang proves timeout and API cancellation both stop a session
// that emits one event and then hangs.
func TestInjectAgentHang(t *testing.T) {
	db := openDB(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s := runner.NewStore(db)
	ts := task.NewStore(db)
	ctx := context.Background()
	maxRuntime := 1
	timed, err := ts.Create(ctx, task.CreateParams{
		Title:             "bench timed agent-hang task",
		Instructions:      modeHang,
		MaxRuntimeSeconds: &maxRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}

	const lease = 3 * time.Second
	f := startFleet(db, s, ts, 1, &scriptAdapter{}, lease, "bench-hang")
	defer f.stop(t)
	waitInt(t, db, `
		SELECT count(*) FROM tasks WHERE id = $1 AND status = 'timed_out'`,
		1, 5*time.Second, "runtime timeout", timed.ID)
	timed, err = ts.Get(ctx, timed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if timed.FailureCode == nil || *timed.FailureCode != "task_runtime_exceeded" ||
		timed.FailureMessage == nil {
		t.Fatalf("timed task failure = %v/%v", timed.FailureCode, timed.FailureMessage)
	}
	assertLeasesReleased(t, db)

	cancelled, err := ts.Create(ctx, task.CreateParams{
		Title:        "bench cancelled agent-hang task",
		Instructions: modeHang,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitInt(t, db, `
		SELECT count(*) FROM activity_events e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1 AND e.event_type = 'agent.started'`,
		1, 5*time.Second, "cancelled session start", cancelled.ID)
	cancelledAt := time.Now()
	if _, err := ts.Cancel(ctx, cancelled.ID, "benchmark cancel during hang"); err != nil {
		t.Fatal(err)
	}
	waitInt(t, db, `
		SELECT count(*) FROM task_attempts
		WHERE task_id = $1 AND status = 'cancelled' AND lease_owner IS NULL`,
		1, 2*time.Second, "cancelled session release", cancelled.ID)
	cancelTook := time.Since(cancelledAt)
	assertLeasesReleased(t, db)

	leftovers, err := filepath.Glob(filepath.Join(tmp, "agent-trail-attempt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("workspaces after timeout and cancellation = %d, want 0", len(leftovers))
	}
	t.Logf("bench inject agent-hang: timeout_status=%s failure_code=%s cancel_released_lease_in=%s workspaces_left=%d",
		timed.Status, *timed.FailureCode, cancelTook.Round(time.Millisecond), len(leftovers))
}

// TestInjectFullDisk points the workspace root at a full filesystem (a
// 1 MiB tmpfs filled by scripts/bench.sh). Provisioning must fail the task
// terminally with the ENOSPC recorded, and the lease must be released.
func TestInjectFullDisk(t *testing.T) {
	dir := os.Getenv("BENCH_FULL_DISK_DIR")
	if dir == "" {
		guard(t)
		t.Skip("BENCH_FULL_DISK_DIR not set (scripts/bench.sh mounts and " +
			"fills a 1 MiB tmpfs when passwordless sudo is available)")
	}
	db := openDB(t)
	t.Setenv("TMPDIR", dir)
	s := runner.NewStore(db)
	ts := task.NewStore(db)
	ctx := context.Background()

	tk, err := ts.Create(ctx, task.CreateParams{
		Title:        "bench full-disk task",
		Instructions: "bench full disk",
	})
	if err != nil {
		t.Fatal(err)
	}

	f := startFleet(db, s, ts, 1, agent.NewFake(), time.Minute, "bench-fulldisk")
	waitInt(t, db, `SELECT count(*) FROM tasks WHERE phase = 'terminal'`,
		1, time.Minute, "terminal task")
	f.stop(t)

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusFailed {
		t.Fatalf("task status = %s, want failed", got.Status)
	}
	if got.FailureCode == nil || got.FailureMessage == nil {
		t.Fatal("failed task has no machine-readable failure code/message")
	}
	if !strings.Contains(*got.FailureMessage, "no space left on device") {
		t.Errorf("failure message %q does not record ENOSPC", *got.FailureMessage)
	}
	assertLeasesReleased(t, db)

	t.Logf("bench inject full-disk: failure_code=%s enospc_recorded=%t",
		*got.FailureCode,
		strings.Contains(*got.FailureMessage, "no space left on device"))
}
