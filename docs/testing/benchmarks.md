# Benchmark Plan

Use reproducible benchmarks, not invented user metrics.

Implemented: `scripts/bench.sh` (or `make bench`) runs every benchmark and
the failure-injection matrix from `apps/api/internal/bench/` against a
dedicated disposable database. Measured numbers live in
`benchmark-results.md`; nothing below is a result until it appears there.

### Concurrent agents

Goal:

- Run 20 simultaneous agent tasks.
- Verify separate branches, workspaces, logs, and limits.
- Measure provisioning and queue wait.

### Webhook idempotency

Goal:

- Process 10,000 simulated GitHub deliveries.
- Include duplicate delivery IDs.
- Verify no duplicate tasks.

### Cleanup

Goal:

- Force 100 cancellations and runner failures.
- Verify containers, pods, worktrees, and credentials are cleaned.

### Review study

Goal:

- Select 10 to 20 small pull requests.
- Ask developers to review with and without Agent Trail evidence.
- Compare review time and defect detection.
