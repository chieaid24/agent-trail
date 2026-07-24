# Benchmark Plan

Use reproducible benchmarks, not invented user metrics.

### Concurrent agents

Goal:

- Run 20 simultaneous agent tasks.
- Verify separate branches, workspaces, logs, and limits.
- Measure provisioning and queue wait.

Possible resume bullet:

> Orchestrated 20 concurrent coding-agent sessions in isolated Kubernetes Jobs with independent Git worktrees, resource limits, and live execution logs.

### Webhook idempotency

Goal:

- Process 10,000 simulated GitHub deliveries.
- Include duplicate delivery IDs.
- Verify no duplicate tasks.

Possible bullet:

> Designed an idempotent GitHub webhook pipeline that processed 10,000 simulated deliveries without duplicate task creation.

### Cleanup

Goal:

- Force 100 cancellations and runner failures.
- Verify containers, pods, worktrees, and credentials are cleaned.

Possible bullet:

> Achieved 100% workspace cleanup across 100 forced runner-failure and cancellation tests using task leases and TTL-based Kubernetes cleanup.

### Review study

Goal:

- Select 10 to 20 small pull requests.
- Ask developers to review with and without Agent Trail evidence.
- Compare review time and defect detection.

Possible bullet:

> Reduced median review time by X% across Y controlled tasks by attaching verified tests, command history, permission usage, and risk summaries.

Use only if measured.
