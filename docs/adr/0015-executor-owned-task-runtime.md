# ADR-0015: Executor-owned task runtime

- Status: accepted
- Date: 2026-08-19

## Context

Tasks can set `max_runtime_seconds`, but the runner previously left that value
unused. The Claude Code adapter had its own fixed timeout, while the fake
adapter and future providers had none. Provider-local limits could stop one
process, but they could not apply one task contract consistently across
adapters or preserve the original budget after lease recovery.

API cancellation also changed the stored task state without interrupting a
session that stopped emitting events. The executor needed one lifecycle policy
for deadline, cancellation, lease release, and workspace cleanup.

## Decision

The executor owns the attempt deadline. It uses the task's
`max_runtime_seconds` when present and the configured
`AGENT_TIMEOUT_SECONDS` default otherwise. Recovery derives the same deadline
from `task_attempts.started_at`, so a new owner does not receive a fresh
budget.

Adapters receive that deadline through the caller context and implement
`Session.Cancel` for explicit interruption. They do not define independent
runtime limits. The executor polls the stored task state every 100ms so API
cancellation interrupts a live session, drains its terminal event stream, and
then settles resource cleanup.

The executor keeps definitive lease loss separate from the first execution
cancellation cause. It also extends ownership under a bounded context directly
before runner-driven task transitions and repository cleanup. If that final
fence fails, the stale owner performs neither operation.

## Alternatives

- Keep a timeout in every adapter. Rejected because behavior and failure codes
  would vary by provider, and task-specific limits could conflict with adapter
  defaults.
- Reset the deadline when an owner recovers a lease. Rejected because repeated
  recovery could make a bounded task unbounded.
- Use PostgreSQL notifications for cancellation. Deferred until cancellation
  volume justifies another long-lived database channel; bounded polling keeps
  the current queue architecture simple.

## Consequences

- Runtime expiry ends the task and attempt as `timed_out` with failure code
  `task_runtime_exceeded`.
- Cancellation is detected on the next successful task-state poll, scheduled
  every 100ms, then waits for adapter shutdown.
- Terminal timeout and cancellation remove workspaces and release leases;
  cleanup failures remain observable executor errors.
- Until provider termination is proven, the executor keeps extending the lease
  and preserves the workspace so another owner cannot overlap the session.
- Terminal writes and repository cleanup require a final lease fence whose
  database work is bounded to less than the renewed lease.
- Every executing attempt adds one small task-state query per polling interval.

## Security implications

The executor requests provider shutdown and proves termination before it
releases the workspace and lease. The Claude Code adapter kills the full
process group so child tool processes do not survive the session. If shutdown
exceeds five seconds, the executor keeps the lease alive and waits; after the
provider eventually stops, it cleans up and returns `ErrSessionStopFailed` to
record that the shutdown contract was breached.

## Revisit conditions

Revisit database polling when runner concurrency makes its query load material,
or when the runner API replaces direct database access. Revisit the five-second
shutdown wait when isolated runner teardown provides a stronger external kill
boundary.
