# Frontend

Implemented: the dashboard overview and the task detail screen, against the
task API surface that exists (docs/architecture/api.md). Login and
installation, the repository page, and the runner page wait on their
backend endpoints (auth, organizations/repositories, runners) and are
tracked as queue issues. Runner health, recent repositories, and the
pull-request link render once their data is served.

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
