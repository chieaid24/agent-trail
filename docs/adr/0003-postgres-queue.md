# ADR-0003: PostgreSQL task queue for the MVP

- Status: accepted
- Date: 2026-07-24

## Context

Tasks travel from the API (webhook creates a task) to the scheduler and
runners. The queue must be durable, support atomic claiming by competing
workers, and run identically on a laptop and in CI. Candidates: a
PostgreSQL-backed queue, Redis Streams, AWS SQS.

## Decision

The MVP queue is PostgreSQL: workers claim tasks with
`SELECT ... FOR UPDATE SKIP LOCKED` inside a transaction.

## Alternatives

- AWS SQS: managed scaling and dead-letter queues, but it does not run
  locally, and the MVP's development loop is local-first. Planned for the
  cloud milestone.
- Redis Streams: fast, but adds a second datastore to the durability-
  critical path; a claimed-but-unacknowledged task would live in Redis
  while its state lives in PostgreSQL, and reconciling the two is exactly
  the kind of distributed-state bug the MVP should not carry.

## Consequences

- Task state and queue position live in one transactional store: claiming
  a task and marking it running is a single transaction, which removes a
  class of lost-update bugs.
- Queue throughput is bounded by PostgreSQL row locking; fine for the
  MVP's task rates, and the ceiling is measurable before it hurts.
- The cloud milestone introduces SQS behind the same internal interface;
  a follow-up ADR records that switch.

## Security implications

- One datastore to secure and back up; no additional broker credentials
  or IAM surface until the cloud milestone.

## Revisit conditions

- The cloud deployment milestone (SQS is already the plan there).
- Measured queue contention or wait times exceeding the reliability
  targets in docs/operations/reliability-targets.md.
