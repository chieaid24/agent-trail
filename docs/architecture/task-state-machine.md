# Task State Machine

Happy path (implemented in `apps/api/internal/task`):

```text
CREATED
  |
  v
QUEUED
  |
  v
PROVISIONING
  |
  v
PLANNING
  |
  v
EXECUTING
  |
  v
VALIDATING
  |
  +--------------------+
  |                    |
  v                    v
PUBLISHING            FAILED
  |
  v
AWAITING_REVIEW
  |
  +--------------------+
  |                    |
  v                    v
COMPLETED          REVISION_REQUESTED
                       |
                       v
                    QUEUED
```

Terminal states:

- COMPLETED
- FAILED
- CANCELLED
- TIMED_OUT

Beyond the diagram, two rule-based edge families exist (safe failure):

- Any non-terminal state -> CANCELLED.
- Any running state (PROVISIONING, PLANNING, EXECUTING, VALIDATING,
  PUBLISHING) -> FAILED or TIMED_OUT. The diagram draws VALIDATING -> FAILED
  because validation failure is the common case; a crash, timeout, or rate
  limit in any running state must also land in a clear terminal state.

Statuses are stored lowercase (`created`, `queued`, ... `awaiting_review`,
`revision_requested`, `timed_out`). Each status maps to a stored, derived
`phase` for coarse filtering: `pending` (created, queued), `running`
(provisioning through publishing), `review` (awaiting_review,
revision_requested), `terminal`.

Requirements (all enforced by `Store.Transition`, with DB constraints as
backstop):

- Every transition must be validated; illegal edges are rejected without
  side effects.
- Every transition must emit exactly one activity event, type
  `task.<new-status>`, with `from`/`to` in the payload.
- Duplicate transition messages must be idempotent: a transition may carry
  an idempotency key, and a replayed key is a no-op (no event, no version
  bump) - even delivered late, after further transitions.
- The runner must not directly assign arbitrary task states; the store's
  transition API is the only write path.
- Cancellation must be accepted from any non-terminal state; cancelling an
  already-cancelled task is an idempotent no-op.
- A task version must increment on mutation. Callers may pass an expected
  version for optimistic concurrency; a stale version is rejected.

Attempts: a task always has exactly one active attempt (attempt 1 is created
with the task), every activity event belongs to an attempt, and sequence
numbers are monotonic per attempt. A terminal transition closes the active
attempt with the matching status; REVISION_REQUESTED -> QUEUED marks it
`superseded` and opens the next attempt, whose timeline restarts at
sequence 1.
