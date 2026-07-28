# Testing Strategy

### Unit tests

Test:

- State transitions
- Signature validation
- Task command parsing
- Branch-name sanitization
- Policy decisions
- Redaction
- Evidence generation
- Retry classification
- Validation parsing
- Cost limits

### Integration tests

Use disposable:

- PostgreSQL
- Redis
- MinIO
- Git repositories
- Fake GitHub API
- Docker runner

Test:

- Duplicate webhook delivery
- Concurrent task claims
- Lease expiration
- Cancellation
- Runner crash
- Git fetch failure
- Agent timeout
- Validation failure
- Push retry
- Pull-request idempotency
- Cleanup

### End-to-end tests

Implemented: the browser suite (`make e2e`, `apps/web/e2e/`). Playwright
boots a disposable stack (dedicated postgres, migrations, seed, api and
worker binaries with the fake adapter) and drives the dashboard against
genuinely executed tasks: grouping, live SSE updates, the log transcript,
trusted-vs-claimed validation rendering, evidence, inline cancellation, and
stream reconnection across a real api restart.

The GitHub-side flow below still needs a dedicated test repository and
lands with publishing.

Scenarios:

1. Successful issue-to-PR flow
2. No-change task
3. Agent failure
4. Test failure
5. Cancellation
6. Duplicate `/agent-trail run`
7. Unauthorized user
8. Replayed webhook
9. Base branch moves
10. Revision request

### Security tests

Test:

- Invalid signatures
- Oversized payloads
- Path traversal
- Command injection
- Secret output
- Denied filesystem paths
- Metadata-service access
- Protected branch push
- Force push
- Malicious repository instruction
- Symlink escape

### Load tests

Control plane:

- 10,000 webhook deliveries
- Duplicate events
- API P95 latency
- SSE connections

Scheduler:

- 100 queued tasks
- 20 concurrent runners
- No double assignment

Logs:

- 20 concurrent tasks
- Multiple MB of logs per task
- Browser responsiveness
- Memory usage

### Failure injection

Simulate:

- Runner kill
- Network interruption
- Database restart
- S3 timeout
- GitHub rate limit
- Queue redelivery
- Control-plane restart
- Agent hang
- Full disk
