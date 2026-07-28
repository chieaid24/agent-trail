# GitHub App Design

### Minimum permissions

Verify current GitHub requirements before implementation.

Likely permissions:

- Metadata: read
- Contents: read and write
- Issues: read and write
- Pull requests: read and write
- Checks: read and write

Avoid broad administration permissions.

### Webhook events

Start with:

- installation
- installation_repositories
- issue_comment
- issues
- pull_request
- pull_request_review
- ping

### Webhook handling

The webhook endpoint must:

1. Read the raw request body.
2. Validate the HMAC signature.
3. Enforce a body-size limit.
4. Read the GitHub delivery ID.
5. Insert the delivery ID under a unique constraint.
6. Return quickly.
7. Process asynchronously.
8. Ignore duplicate delivery IDs.
9. Avoid logging secrets or full private payloads.

Suggested table:

```sql
CREATE TABLE github_webhook_deliveries (
    id uuid PRIMARY KEY,
    github_delivery_id text UNIQUE NOT NULL,
    event_type text NOT NULL,
    action text,
    installation_id bigint,
    repository_id bigint,
    signature_valid boolean NOT NULL,
    processing_status text NOT NULL,
    received_at timestamptz NOT NULL,
    processed_at timestamptz,
    failure_message text
);
```

Implementation note (migration `00003_github_integration.sql`): only
signature-valid deliveries are recorded, so a forged request can never
occupy a delivery id; invalid signatures are rejected and counted in
metrics instead.

### Task command

MVP:

```text
/agent-trail run
```

Optional future syntax:

```text
/agent-trail run --base main --timeout 45m
```

Authorization rules:

- Commenter must have write access.
- Repository must be enabled.
- Only one active task per issue by default.
- The issue title and body become primary instructions.
- The triggering comment becomes an additional instruction.

### GitHub Checks

Create a check named:

```text
Agent Trail Task
```

Statuses:

- queued
- in_progress
- completed

Conclusions:

- success
- failure
- cancelled
- timed_out
- action_required
- neutral

Task creation puts a `queued` check (external id: task id) on the base
branch head. Publishing resolves a `completed` check on the pushed head
commit, keyed by external id = attempt id so replays update rather than
duplicate; conclusion mirrors trusted validation (failed check ->
`failure`; checks that could not all run, or a no-change outcome ->
`neutral`; otherwise `success`).
