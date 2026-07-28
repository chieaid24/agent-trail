# API Design

Base path:

```text
/api/v1
```

### Authentication

```text
GET  /auth/github/start
GET  /auth/github/callback
POST /auth/logout
GET  /me
```

### Organizations and repositories

```text
GET  /organizations
GET  /organizations/{orgId}
GET  /organizations/{orgId}/repositories
POST /repositories/{repoId}/enable
POST /repositories/{repoId}/disable
GET  /repositories/{repoId}/settings
PUT  /repositories/{repoId}/settings
```

### Tasks

Implemented: list, create, get, cancel, events, validations, evidence. The
rest of the surface lands with its milestone (retry with the runner,
commands with the real agent adapter, artifacts with GitHub publishing).

Security limitation: the implemented task endpoints are currently
unauthenticated - anyone who can reach the API can create and cancel tasks.
Acceptable only while the API binds to localhost in development; the
GitHub OAuth session layer above must land before any deployment exposes
this surface.

```text
GET  /tasks
POST /tasks
GET  /tasks/{taskId}
POST /tasks/{taskId}/cancel
POST /tasks/{taskId}/retry
GET  /tasks/{taskId}/events
GET  /tasks/{taskId}/commands
GET  /tasks/{taskId}/validations
GET  /tasks/{taskId}/evidence
GET  /tasks/{taskId}/artifacts
```

Implemented semantics:

- `POST /tasks` takes `title` and `instructions` (required) plus optional
  `priority`, `base_branch`, `max_runtime_seconds`, `max_cost_usd`; returns
  201 with the task, already queued.
- `GET /tasks` filters with `?status=` and bounds with `?limit=` (max 200).
- `GET /tasks/{taskId}/events` returns the timeline ordered by attempt then
  sequence, bounded by `?limit=` (max 1000).
- `POST /tasks/{taskId}/cancel` takes an optional `{"reason": "..."}`;
  cancelling an already-cancelled task is an idempotent 200, other terminal
  states answer 409.
- `GET /tasks/{taskId}/validations` returns every stored validation result
  across the task's attempts, ordered by attempt then execution order, each
  carrying its measured `exit_code`, `status`, and `trusted_execution`.
- `GET /tasks/{taskId}/evidence` returns the latest attempt's evidence
  report (JSON document plus rendered Markdown summary); 404 with
  `no evidence report for task` until one exists.
- Errors are `{"error": "message"}`: 400 malformed input, 404 unknown task,
  409 illegal transition or stale version, 503 when no database is
  configured.

### Streaming

Implemented.

```text
GET /tasks/{taskId}/stream
```

Server-sent events. Each frame's `data` is one activity event, the same JSON
object `GET /tasks/{taskId}/events` returns; the SSE `id` field is the resume
cursor `<attempt_number>:<sequence_number>` (sequence numbers restart per
attempt, so the cursor carries both).

Reconnection: `EventSource` echoes the last cursor as `Last-Event-ID`
automatically and the server replays only events after it. A first connection
that wants to resume (for example after a page reload) may pass
`?last_event_id=<cursor>`; the header wins when both are present. A malformed
cursor is a 400.

The server heartbeats with SSE comment lines while the task runs. Once the
task is terminal and the timeline is fully delivered it emits a `done` event
(`data: {"status": "<final status>"}`) and closes the stream; clients close
on `done` instead of reconnecting.

### Webhook

Implemented (docs/architecture/github-app.md).

```text
POST /webhooks/github
```

Authenticate using GitHub signature validation.

### Metrics

Implemented.

```text
GET /metrics
```

Prometheus text exposition of the counters listed in
docs/operations/observability.md. No authentication; deploy behind the
internal network boundary.

### Runner internal API

```text
POST /internal/runners/register
POST /internal/runners/{runnerId}/heartbeat
POST /internal/tasks/claim
POST /internal/task-attempts/{attemptId}/events
POST /internal/task-attempts/{attemptId}/complete
POST /internal/task-attempts/{attemptId}/fail
POST /internal/artifacts/upload-url
```

Protect internal endpoints using:

- mTLS
- workload identity
- short-lived signed runner tokens

Do not use a permanent shared plaintext token.
