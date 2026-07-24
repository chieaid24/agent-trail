# Logs and Event Streaming

### Event ordering

Every task attempt receives a monotonic sequence number.

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
