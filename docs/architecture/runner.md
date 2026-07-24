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

Example claim query:

```sql
SELECT id
FROM task_attempts
WHERE status = 'QUEUED'
  AND (
    lease_expires_at IS NULL
    OR lease_expires_at < now()
  )
ORDER BY priority DESC, created_at
FOR UPDATE SKIP LOCKED
LIMIT 1;
```

Update the lease in the same transaction.

Suggested fields:

```text
lease_owner
lease_expires_at
heartbeat_at
```
