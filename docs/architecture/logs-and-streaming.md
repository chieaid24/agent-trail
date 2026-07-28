# Logs and Event Streaming

### Event ordering

Every task attempt receives a monotonic sequence number.

### Current implementation

The dashboard's terminal log view is derived from `command.*` activity
events (`command.started`, `command.output` chunks, `command.completed`),
delivered over the SSE stream (docs/architecture/api.md: Streaming) and
rebuilt client-side into a virtualized transcript. The dedicated log store
below is not built yet; until it exists, command output lives in event
payloads and is bounded by the activity-event path.

### Storage

Do not store unlimited log output in PostgreSQL.

Recommended approach:

- PostgreSQL stores metadata and summaries.
- S3 or MinIO stores chunked logs.
- Redis may store a short live tail.
- The UI requests historical chunks when scrolling.

### Redaction

Redact:

- Authorization headers
- GitHub tokens
- Provider API keys
- Private keys
- Common cloud credential formats
- Explicitly marked secrets

Because redaction is imperfect:

- Minimize secret exposure.
- Use short-lived credentials.
- Restrict log access.
- Avoid public raw logs.
