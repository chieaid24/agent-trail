# Benchmark Results

Measured output of the reproducible suite in `apps/api/internal/bench/`,
run via `scripts/bench.sh` (see `benchmarks.md` for the plan and
`docs/operations/local-development.md` for the runbook). Every number
below names its recorded run; nothing is estimated or extrapolated.

## Recorded run

- Date: 2026-08-19 (UTC), run log `bench-20260819T194406Z.log`
  (artifacts are gitignored; re-run `make bench` to regenerate)
- Commit: the suite at `5ac4f2a` plus the recovery fix `2b1b44c`
- Host: Intel Core Ultra 7 155H (22 hardware threads), 15 GiB RAM,
  Linux 6.6.87 (WSL2)
- Stack: Go 1.26.5, PostgreSQL 17.7 (alpine, Docker 29.3.1), dedicated
  compose project on its own port, `*sql.DB` pool capped at 50
- Adapter: the fake adapter (or the bench script adapter) - these runs
  measure the control plane and runner orchestration, not a model

The Agent hang row was rerun after runtime enforcement changed:

- Date: 2026-08-19 (UTC), run log `bench-20260819T204809Z.log`
- Commit: `79d1ddb`
- Scope: `BENCH_RUN=TestInjectAgentHang`
- Stack and host: same environment as the full recorded run above

Reproduce with:

```bash
make bench                       # full suite
BENCH_RUN=TestScheduler make bench   # one benchmark
```

## Webhook idempotency (10,000 deliveries)

`TestWebhookIdempotency10k`: 10,000 signed deliveries - 5,000 unique
task-creating `issue_comment` events plus one exact duplicate of each,
shuffled - POSTed by 64 concurrent clients to the real
`POST /webhooks/github` handler backed by the real database.

| Measure | Value |
|---|---|
| Send wall clock | 6.462s (1,548 req/s) |
| Ack latency p50 / p95 / p99 / max | 25.5ms / 132.0ms / 211.3ms / 490.6ms |
| Acked `accepted` / `duplicate` | 5,000 / 5,000 (exact) |
| Delivery ledger rows | 5,000 (one per unique id) |
| Tasks created | 5,000 |
| Issues with duplicate tasks | 0 |

The p95 ack latency meets the < 500ms target in
`docs/operations/reliability-targets.md`. Duplicate arbitration is the
`github_delivery_id` unique constraint (`INSERT ... ON CONFLICT DO
NOTHING`), so it holds under concurrency: originals and duplicates were
in flight simultaneously.

## Scheduler (100 tasks, 20 runners)

`TestScheduler100Tasks20Runners`: 100 queued tasks drained by 20
in-process runner hosts, each its own registered runner claiming from the
Postgres queue (`FOR UPDATE SKIP LOCKED` leases). One worker process hosts
one serial runner, so 20 hosts is the honest shape of 20 workers.

| Measure | Value |
|---|---|
| Drain wall clock | 1.979s (50.5 tasks/s) |
| Distinct runners that executed | 20 of 20 |
| Attempts executed more than once | 0 (no double assignment) |
| Queue wait p50 / p95 / max | 1.284s / 1.674s / 1.711s |
| Provisioning p50 / p95 / max | 9.6ms / 24.5ms / 38.0ms |
| Leases still held afterwards | 0 |

Queue wait is dominated by the queue itself (100 tasks across 20 serial
runners: the last tasks wait for four predecessors), not by claim
contention.

## Concurrent agents (20 simultaneous)

`TestConcurrentAgents20`: 20 sessions that block on a shared barrier
releasing only when all 20 are mid-flight - the barrier releasing is the
proof of true 20-way concurrency, not an inference from timestamps.

| Measure | Value |
|---|---|
| Time to all 20 sessions running | 133ms |
| Total wall clock | 355ms |
| Distinct workspaces | 20 (all under the run's TMPDIR) |
| Workspaces left on disk afterwards | 0 |
| Queue wait p50 / p95 | 158.9ms / 211.0ms |
| Provisioning p50 / p95 | 8.8ms / 11.9ms |

Isolation measured here is workspace and timeline isolation of the
process runner. Branch-level isolation of the git worktree flow is
covered by `internal/gitworkspace` tests; per-task resource limits and
Kubernetes Job isolation are properties of the Job manifest, verified
separately by `scripts/verify-k8s-runner.sh`, and were not part of this
run.

## Cleanup (100 forced cancellations and failures)

`TestCleanup100ForcedFailures`: 100 tasks forced off the happy path - 50
cancelled through the store mid-session (the API cancellation path), 50
failed by the agent session - across 20 runners.

| Measure | Value |
|---|---|
| Terminal outcomes | 50 cancelled + 50 failed (exact) |
| Workspaces cleaned | 100 of 100 (`cleanup.completed` per attempt) |
| Workspaces left on disk | 0 |
| Failed tasks without machine-readable code+message | 0 |
| Leases still held | 0 |
| Drain wall clock | 801ms |

Scope caveat: this measures the temp-dir workspace of the process runner
flow. Terminal cancellation and timeout also remove repository-backed git
worktrees; shutdown and lease loss retain them for recovery. Credential cleanup
is not exercised: the fake flow holds no credentials, and in the publishing
flow installation tokens live only in process memory, so process exit is their
destruction.

## Failure injection

`inject_test.go`, one test per row of the matrix in
`testing-strategy.md`.

| Injection | Method | Measured outcome |
|---|---|---|
| Runner kill | Claim taken, then silence (the state SIGKILL leaves) | Recovered by a second runner in 2.149s on a 2s lease; never before expiry; attempt reassigned; task completed |
| Database restart | `docker restart -t 1` mid-drain (50 tasks, 10 runners, 10s lease) | Outage 1.807s; runners retried through it; 3 interrupted attempts re-executed after lease expiry; all 50 tasks completed exactly once at task level; drain 10.486s |
| Network interruption | GitHub stub drops every connection mid-request | Webhook still acked in 5.5ms (processing is async); delivery settled `failed` with the error recorded; no task created; next delivery after recovery processed normally |
| GitHub rate limit | Stub answers 429 to every call | Delivery settled `failed` with the 429 recorded; no task; recovered on the next delivery. The client has no retry/backoff by design (ADR-0006), so one 429 fails that delivery's processing |
| S3 timeout | Not applicable | No object-storage code path exists; logs, evidence, and validation results live in Postgres. Skip is recorded in the suite; add the injection when log offload lands |
| Agent hang | Session emits one event, then nothing, until cancelled | A 1s task limit ended `timed_out` with `task_runtime_exceeded`; API cancellation stopped a second hung session and released its lease in 119ms; both workspaces were removed and no lease remained (supplemental run above) |
| Full disk | 1 MiB tmpfs filled to ENOSPC as TMPDIR | Skipped in the recorded run: the mount needs passwordless sudo, which this host does not grant. The harness (`TestInjectFullDisk`) asserts a terminal `failed` task with the ENOSPC recorded when `BENCH_FULL_DISK_DIR` is provided |

## Defect found and fixed by this suite

The first full run stranded 1 task of 50 in the database-restart
injection: a repository-less task whose owner died between the
`awaiting_review` transition and the executor's auto-complete was left in
a status no runner may claim. Fixed in `2b1b44c` - the claim query now
admits `awaiting_review` tasks with no repository - with regression tests
(`TestClaimRecoversStrandedRepositorylessAwaitingReview`,
`TestClaimSkipsPublishedAwaitingReview`). The recorded run passed 50/50
after the fix.

## Known limitations of these numbers

- Single host, WSL2, local Postgres over localhost: no network latency
  between control plane, database, and runners. Cloud numbers will be
  different; nothing here claims them.
- The fake and bench adapters complete in milliseconds, so throughput
  numbers measure orchestration overhead, not agent runtime.
- The webhook processor spawns one goroutine per unique accepted
  delivery (unbounded); at 5,000 unique deliveries the capped pool (50)
  absorbed it. Sustained abuse beyond that is untested.
- The log-volume browser test (20 tasks, multiple MB of logs, browser
  responsiveness), SSE-connection load, and the review study in
  `benchmarks.md` have no harness yet; API latency is measured only for
  the webhook ack path.
- Queue redelivery and control-plane restart from the
  `testing-strategy.md` matrix are not yet injected.
