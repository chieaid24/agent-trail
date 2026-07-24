# Task State Machine

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

Requirements:

- Every transition must be validated.
- Every transition must emit an activity event.
- Duplicate transition messages must be idempotent.
- The runner must not directly assign arbitrary task states.
- Cancellation must be accepted from any non-terminal state.
- A task version must increment on mutation.
