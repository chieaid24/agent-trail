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
- Cancellation reaches a running session within the 100ms polling interval,
  plus adapter shutdown time.
- Terminal timeout and cancellation remove workspaces and release leases;
  cleanup failures remain observable executor errors.
- Every executing attempt adds one small task-state query per polling interval.

## Security implications

The executor stops provider processes before it releases their workspace and
lease. The Claude Code adapter kills the full process group so child tool
processes do not survive the session. A provider that ignores both its context
and `Session.Cancel` can exceed the five-second shutdown wait; the executor
reports that failure instead of claiming the provider stopped.

## Revisit conditions

Revisit database polling when runner concurrency makes its query load material,
or when the runner API replaces direct database access. Revisit the five-second
shutdown wait when isolated runner teardown provides a stronger external kill
boundary.
