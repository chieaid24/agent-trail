# Frontend

Implemented: the dashboard overview, task detail, repository detail, and
runner detail screens against the API surface in docs/architecture/api.md.
The overview includes runner health and recently synced repositories.
Login and installation remain tracked separately because the authentication
endpoints do not exist yet. The pull-request link, permissions, and denied
actions render once their data is served; denied actions also need a policy
event surface the runner does not emit yet.

### Screens

#### Login and installation

- Sign in with GitHub
- Install GitHub App
- Choose organization
- Enable repositories

#### Dashboard

Show:

- Queued tasks
- Running tasks
- Awaiting review
- Failed tasks
- Runner health
- Recent repositories
- Completion rate
- Runtime

#### Task detail

Show:

- Status
- Original issue
- Agent plan
- Live timeline
- Terminal-style logs
- Files changed
- Validation results
- Permissions
- Denied actions
- Evidence report
- Pull-request link
- Runtime and cost
- Cancellation button

#### Repository page

Show:

- Enabled state
- Default policy
- Validation config
- Active tasks
- Recent tasks
- Repository metrics

#### Runner page

Show:

- Runner status
- Capacity
- Current task
- Heartbeat
- CPU
- Memory
- Disk
- Recent failures

### UX requirements

- Live updates without refresh
- Searchable logs
- Follow mode
- Virtualized large output
- Visible redaction markers
- Clear distinction between trusted validation and agent claims
- Confirmation before cancellation
- Actionable failure messages
