# Task execution insights

`GET /api/v1/tasks/{taskId}/insights` returns `task_id`, `task_status`,
`overall`, and `attempts`, ordered by `attempt_number`. A revision started by
`/agent-trail revise` supersedes the previous attempt and adds the next
`attempt_number` with its own timings, events, validation, and cost; the
pull request body's attempts history derives a trusted-only validation outcome
(passed, failed, error) and the same reported cost from the same sources. An
unknown task
returns 404; an invalid UUID returns 400. The endpoint uses the existing
session protection of the task API. Like the existing task read endpoints,
it does not enforce repository membership; this read model adds no new
authorization boundary.

All sources are read in one read-only, repeatable-read database snapshot.
No metric-point table or exported metric labels are added. The trace
waterfall remains the detailed view of persisted execution evidence.

| Field | Canonical source |
| --- | --- |
| `queue_wait_ms` | `task_attempts.started_at - created_at`; start is the first provisioning transition |
| `provisioning_ms` | Sum of persisted `runner.provisioning` span durations |
| `agent_session_ms` | Sum of persisted `agent.session` span durations |
| `validation_ms` | Sum of persisted `validation.run` span durations |
| `publishing_ms` | First `task.awaiting_review` event timestamp minus first `task.publishing` event timestamp in the same attempt |
| `total_runtime_ms` | Sum of persisted `runner.attempt` span durations after the attempt ends or the task enters review |
| `event_count` | Number of activity events associated with the attempt |
| `validation` | Outcome counts and trusted-execution count from `validation_results` for the attempt |
| `cost` | Ordered `agent.cost_update` payloads for the attempt |

Span sums include each completed recovery claim associated with the attempt.
Active attempts outside `awaiting_review` and `revision_requested` have a null
total runtime because earlier claim spans cannot establish that the current
execution has ended. Review states follow runner execution even though the
attempt remains active until review resolves. Phase timings include only
recorded spans. Missing spans or timestamp boundaries produce null durations;
negative timestamp differences are not reported. Durations are milliseconds,
with span sums rounded to the nearest millisecond. No duration uses the current
wall clock. Terminal attempts without a persisted runtime span remain null.

Absent validation results produce `validation: null`. Present results contain
`total`, `passed`, `failed`, `timed_out`, `error`, and `trusted` counts. Counts
include both trusted and untrusted outcomes; `trusted` records provenance and
does not turn an agent-reported result into trusted validation.

Cost payloads with a nonnegative numeric `total_cost_usd` replace the preceding
attempt total. Otherwise, a nonnegative numeric `cost_usd` increments it.
Malformed payloads and absent cost fields are ignored. A valid reported zero
is distinct from absent reporting, which returns `cost: null`. `update_count`
counts valid reporting events. These values are provider-reported cost, not
billing verification.

The overall summary counts attempts and events, sums validation outcomes and
reported attempt costs, and returns total runtime only when every attempt has
a recorded runtime. Overall cost covers reporting attempts; providers without
cost reporting do not imply zero cost. Tasks without attempts return an empty
`attempts` array, zero attempt/event counts, and null runtime, validation, and
cost.

Persisted spans are eventually consistent: the worker batches completed spans
into PostgreSQL. A response can contain validation or event evidence before
its matching span arrives, and null timings can become available on a later
read. A missing span is never evidence that the phase took zero time.

Runtime totals describe the completed claim spans already persisted. Export
batching can briefly leave a partial sum after a transition to review or a
terminal state; later reads incorporate the remaining recorded evidence.
