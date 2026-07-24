# Milestones

### Milestone 0: Foundation

Deliver:

- Monorepo
- API skeleton
- Web skeleton
- PostgreSQL
- CI
- Docker Compose
- ADR template
- Threat-model draft

Acceptance:

- One command starts development.
- CI runs format, lint, test, and build.

### Milestone 1: GitHub integration

Deliver:

- GitHub App
- Installation flow
- Repository sync
- Signature validation
- Delivery deduplication
- `/agent-trail run`

Acceptance:

- One real issue comment creates one task.
- Replayed delivery creates no duplicate.

### Milestone 2: Task domain

Deliver:

- Task tables
- Task attempts
- State machine
- Activity events
- API
- Cancellation

Acceptance:

- Invalid transitions fail.
- Every transition creates an event.

### Milestone 3: Fake runner

Deliver:

- Runner registration
- Task claiming
- Leases
- Heartbeats
- Fake agent adapter
- Timeline events

Acceptance:

- A fake task completes end to end.
- Runner loss is detected.

### Milestone 4: Git workspaces

Deliver:

- Repository cache
- Worktree creation
- Branch creation
- Diff capture
- Cleanup

Acceptance:

- Two tasks have isolated writable workspaces.
- Cleanup removes completed worktrees.

### Milestone 5: Real agent adapter

Deliver:

- Provider interface
- First provider
- Plan capture
- Event normalization
- Timeout
- Cancellation

Acceptance:

- Agent completes a fixture issue.
- Provider details stay outside core domain logic.

### Milestone 6: Validation and evidence

Deliver:

- Validation file
- Trusted checks
- Result storage
- Evidence JSON
- Evidence Markdown

Acceptance:

- Failed tests remain failed.
- Trusted results are visibly distinct.

### Milestone 7: GitHub publishing

Deliver:

- Commit
- Push
- Draft PR
- Check run
- Issue comment
- Idempotent retries

Acceptance:

- One task creates at most one PR.
- Empty changes create no PR.

### Milestone 8: Dashboard

Deliver:

- Task list
- Task detail
- SSE timeline
- Log viewer
- Validation view
- Evidence view
- Cancellation

Acceptance:

- User can understand task state without backend logs.
- SSE reconnects correctly.

### Milestone 9: Cloud deployment

Deliver:

- Terraform
- Kubernetes Job runner
- S3
- RDS
- Queue
- Secrets Manager
- OpenTelemetry
- Grafana

Acceptance:

- Task runs inside a restricted Job.
- Control plane and runner use separate IAM roles.
- Cleanup works.

### Milestone 10: Benchmarks

Deliver:

- Load tests
- Failure injection
- Benchmark report
- Security limitations
- Demo video
- Resume metrics

Acceptance:

- Main results are reproducible.
- No fake metrics appear.

### Milestone 11: Conflict detection

Post-MVP.

Deliver:

- Active task overlap
- File conflicts
- Migration conflicts
- Dependency conflicts

Acceptance:

- Deterministic overlaps are detected against a labelled fixture set.
