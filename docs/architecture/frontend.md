# Frontend

Implemented: the dashboard overview, task detail, repository detail, and
runner detail screens against the API surface in docs/architecture/api.md,
plus login and installations. The overview includes runner health and
recently synced repositories. The pull-request link, permissions, and
denied actions render once their data is served; denied actions also need
a policy event surface the runner does not emit yet.

### Screens

#### Login and installation

Implemented. `/login` is the signed-out screen: one sentence, one
Sign-in-with-GitHub action, and the callback's error codes rendered as
plain messages. Any 401 from the API sends the browser there. The
`/installations` screen lists each connected GitHub account with its
synced repositories and per-row enable/disable, links the GitHub App
installation page for adding accounts, and shows a one-action empty state
when nothing is connected. The shell shows the session user with sign
out; without OAuth configured (localhost development) it degrades to the
plain footer.

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
- Conflict warnings (overlapping active tasks, conflict-detection.md)
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

Conflict warnings have loading, empty, unavailable with Retry, populated, and
long-content states in a fixed-height region. Active tasks poll warnings every
10 seconds because a sibling publish does not enter the viewed task's event
stream. Task lifecycle events reload both task and warning data, so a task that
turns terminal clears the active-only warning surface.

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
