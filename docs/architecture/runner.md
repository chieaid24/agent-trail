# Runner Design

## Runner Responsibilities

The runner must:

1. Claim a task attempt.
2. Create an isolated workspace.
3. Request short-lived GitHub credentials.
4. Fetch the repository.
5. Verify the base commit.
6. Create a branch and worktree.
7. Prepare task context.
8. Launch the agent.
9. Capture normalized events.
10. Enforce runtime limits.
11. Detect cancellation.
12. Run trusted validation.
13. Compute diff statistics.
14. Commit changes.
15. Push the branch.
16. Upload logs and artifacts.
17. Report completion.
18. Destroy credentials.
19. Clean up the workspace.

The git mechanics behind steps 2, 4, 5, 6, 13, 14, 15, and 19 (mirror cache,
worktree, base-SHA verification, diff statistics, commit trailers, push guard,
cleanup) live in `apps/api/internal/gitworkspace`; see
[git-workspaces.md](git-workspaces.md) and [ADR-0007](../adr/0007-git-mirror-cache-and-worktrees.md).


## Runner Task Contract

Example:

```json
{
  "task_attempt_id": "uuid",
  "repository": {
    "clone_url": "https://github.com/example/repo.git",
    "base_branch": "main",
    "base_commit_sha": "abc123"
  },
  "working_branch": "agent-trail/issue-42-token-rotation",
  "instructions": "Add refresh-token rotation...",
  "agent": {
    "provider": "claude-code",
    "model": "configured-default"
  },
  "limits": {
    "timeout_seconds": 2700,
    "cpu": "2",
    "memory_mb": 4096,
    "disk_mb": 10240
  },
  "policy": {
    "version": 1,
    "network_profile": "package-registries"
  },
  "validation": [
    {
      "name": "unit-tests",
      "command": ["npm", "test"],
      "timeout_seconds": 600
    }
  ]
}
```

Do not place long-lived credentials in this payload.


## Task Leasing

Required guarantees:

- At-least-once task delivery is acceptable.
- Only one runner may own an attempt at a time.
- Every ownership claim must have an expiration.
- Runner heartbeats extend the lease.
- A lost runner must not immediately cause duplicate execution.
- Publishing must be idempotent.

The implemented claim query (`internal/runner/store.go`) selects the active
attempt of a claimable task, skipping live leases:

```sql
SELECT a.id, a.attempt_number, t.id, t.status, t.title, t.instructions
FROM task_attempts a
JOIN tasks t ON t.id = a.task_id
WHERE a.status = 'active'
  AND (a.lease_expires_at IS NULL OR a.lease_expires_at < now())
  AND t.status IN ('queued', 'provisioning', 'planning', 'executing',
                   'validating', 'publishing', 'awaiting_review')
ORDER BY t.priority DESC, t.created_at
FOR UPDATE OF a SKIP LOCKED
LIMIT 1;
```

The lease is written in the same transaction. A queued task is a fresh
claim; any later status is recovery of an attempt whose owner lost its
lease, and the new owner resumes from the recorded status. Delivery is
at-least-once, so a resumed attempt may repeat agent events on the
timeline; only-one-owner-at-a-time is the invariant the lease enforces.

Lease fields on `task_attempts`:

```text
lease_owner       -- runner holding the lease (NULL when unleased)
lease_expires_at  -- lease deadline; expiry makes the attempt claimable
heartbeat_at      -- last lease extension, for diagnostics
```

`runner_id` records which runner ran the attempt and survives release.

## Milestone 3 status

The runner currently lives inside `cmd/worker` as a `process` runner: it
registers itself, heartbeats, reaps lost runners (`status = 'lost'` after
`RUNNER_LOST_AFTER_SECONDS` without a heartbeat, plus a `runner.lost`
timeline event on every attempt the loss strands), and drives claimed
attempts through the fake agent adapter. Runner statuses are `online`,
`lost`, and `offline` (deliberate shutdown; a heartbeat revives `lost` but
never `offline`).

Because the runner and control plane share one process and one database,
claiming goes straight through PostgreSQL (ADR-0003); the internal runner
HTTP API in docs/architecture/api.md lands when runners move out of
process (runner isolation milestone). Trusted validation and publishing
are recorded as skipped on the timeline until milestones 6 and 7, and the
fake flow auto-completes from awaiting_review - there is nothing published
to review yet, so the human review gate becomes real with publishing.
