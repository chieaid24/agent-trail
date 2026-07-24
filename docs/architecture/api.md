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

Implemented: list, create, get, cancel, events. The rest of the surface
lands with its milestone (retry with the runner, commands/validations/
evidence/artifacts with validation and evidence).

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
- Errors are `{"error": "message"}`: 400 malformed input, 404 unknown task,
  409 illegal transition or stale version, 503 when no database is
  configured.

### Streaming

```text
GET /tasks/{taskId}/stream
```

Use server-sent events.

Event envelope:

```json
{
  "id": "event-uuid",
  "sequence": 143,
  "type": "command.output",
  "timestamp": "2026-07-24T18:00:00Z",
  "data": {}
}
```

Support reconnection using `Last-Event-ID`.

### Webhook

```text
POST /webhooks/github
```

Authenticate using GitHub signature validation.

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
