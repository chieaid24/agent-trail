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
