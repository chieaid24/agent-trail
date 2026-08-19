# API Design

Base path:

```text
/api/v1
```

### Authentication

Implemented (docs/adr/0013-dashboard-sessions.md). These routes sit outside
the `/api/v1` prefix, like `/healthz` and `/webhooks/github`.

```text
GET  /auth/github/start
GET  /auth/github/callback
POST /auth/logout
GET  /me
```

Session handling:

- Sign-in is the GitHub App's OAuth user authorization. `start` binds a
  random state to the browser in a short-lived HttpOnly cookie and
  redirects to GitHub; `callback` verifies the state, exchanges the code,
  upserts the user, resyncs organization memberships from
  `GET /user/installations`, and redirects to the dashboard. Callback
  failures redirect to `/login?error=<code>` with the detail only in the
  server logs.
- A session is a database row: 256-bit random token (only its SHA-256
  stored), 30-day fixed expiry, no sliding refresh. The browser carries it
  in the HttpOnly, SameSite=Lax `agent_trail_session` cookie, set Secure
  under `AUTH_COOKIE_SECURE=true`. Cookies work because the browser
  reaches the API through the dashboard's same-origin `/backend` proxy -
  which is also what lets the SSE stream authenticate, since EventSource
  cannot set headers.
- `POST /auth/logout` deletes the session row and clears the cookie (204,
  idempotent). `GET /me` returns the session user and, when
  `GITHUB_APP_SLUG` is set, the app installation URL; 401 without a live
  session.
- Enforcement is config-gated: with `GITHUB_OAUTH_CLIENT_ID` and
  `GITHUB_OAUTH_CLIENT_SECRET` set (and a database configured - sessions
  are rows, and without `DATABASE_URL` the whole `/api/v1` surface already
  answers 503), every `/api/v1` route (tasks, dashboard reads, enablement
  writes, the SSE stream) answers 401 without a live session. Without them the auth routes answer 503 and the API
  retains its localhost-development openness. `/healthz`, `/readyz`,
  `/metrics`, and `/webhooks/github` never require a session.

### Organizations and repositories

Implemented: organization and repository reads, and the enablement writes.
The settings write lands later.

```text
GET  /organizations
GET  /organizations/{orgId}
GET  /organizations/{orgId}/repositories
GET  /repositories
GET  /repositories/{repoId}
POST /repositories/{repoId}/enable
POST /repositories/{repoId}/disable
GET  /repositories/{repoId}/settings
PUT  /repositories/{repoId}/settings
```

Read semantics:

- `GET /organizations` returns each GitHub account with total and enabled
  repository counts.
- `GET /organizations/{orgId}/repositories` returns that account's synced
  repositories. `GET /repositories` returns the same read model across all
  accounts, newest sync first. Both accept `?limit=` (max 200).
- `GET /repositories/{repoId}` returns repository settings, task metrics,
  up to 10 active tasks, and the 10 most recently updated tasks.
- Repository settings expose `default_policy` and `validation_file`.
  Missing overrides resolve to `platform default` and
  `.agent-trail/validation.yaml`.
- `POST /repositories/{repoId}/enable` and `/disable` flip `is_enabled`
  and return the updated repository read model. With the session layer on,
  the caller must belong to the repository's organization (403 otherwise),
  and the change is audit-logged with the acting user. `PUT
  /repositories/{repoId}/settings` remains unimplemented.

### Runners

Implemented: runner reads.

```text
GET /runners
GET /runners/{runnerId}
```

`GET /runners` returns status, capacity, active slot count, heartbeat, labels,
and optional CPU, memory, and disk percentages. The detail endpoint also
returns current tasks and the 10 most recent failed or timed-out tasks.
Resource percentages remain `null` until a runner reports `cpu_percent`,
`memory_percent`, and `disk_percent` labels.

### Tasks

Implemented: list, create, get, cancel, events, validations, evidence,
conflicts. The rest of the surface lands with its milestone (retry with the
runner, commands with the real agent adapter, artifacts with GitHub
publishing).

Security limitation: without OAuth credentials configured the task
endpoints are unauthenticated - anyone who can reach the API can create
and cancel tasks. Acceptable only while the API binds to localhost in
development. Deployments must set `GITHUB_OAUTH_CLIENT_ID` and
`GITHUB_OAUTH_CLIENT_SECRET`, which puts every task endpoint behind the
session layer above. Authorization is membership-gated on enablement
writes only; task routes require any signed-in user, not yet a role
(docs/adr/0013-dashboard-sessions.md).

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
GET  /tasks/{taskId}/conflicts
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
- `GET /tasks/{taskId}/conflicts` returns the task's stored overlap
  warnings as `{"conflicts":[...]}` against other still-active tasks of its
  repository (conflict-detection.md). Each warning carries its id, detection
  and update timestamps, the other task's id and title, the detector `kinds`
  that fired, and the implicated `files`. Rows are ordered by initial
  `detected_at` descending, then id. An empty list is the normal no-conflict
  state; an unknown task returns 404.
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

Prometheus text exposition of the metrics registered by the API process. Worker
histograms and gauges reach Prometheus through OTLP instead; see
docs/operations/observability.md. No authentication; deploy behind the internal
network boundary.

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
