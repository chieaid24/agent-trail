# Git Workspace Strategy

### Repository cache

```text
/var/lib/agent-trail/repos/<repository-id>/repo.git
```

Use a bare or mirror repository cache.

### Task workspace

```text
/var/lib/agent-trail/workspaces/<task-attempt-id>/
```

Conceptual commands:

```bash
git clone --mirror "$CLONE_URL" "$REPO_CACHE"
git -C "$REPO_CACHE" fetch origin
git -C "$REPO_CACHE" worktree add \
  -b "$WORKING_BRANCH" \
  "$WORKTREE_PATH" \
  "$BASE_COMMIT_SHA"
```

Implementation rules:

- Use argument arrays, not shell interpolation.
- Sanitize branch names.
- Verify the base SHA.
- Prevent path traversal.
- Prevent branch pushes outside `agent-trail/*`.
- Prevent force pushes.
- Record final commit SHA.
- Use Git-aware worktree cleanup.
- Never prune an active worktree.

### Commit trailers

Example:

```text
Agent-Trail-Task-ID: <task-id>
Agent-Trail-Agent-Provider: <provider>
Agent-Trail-Agent-Model: <model>
Agent-Trail-Requested-By: <github-login>
```

Do not include prompts or secret values.

### Implementation

`apps/api/internal/gitworkspace` (see ADR-0007) owns this strategy. The root is
`WORKSPACE_ROOT` (default `/var/lib/agent-trail`); the mirror cache lives at
`<root>/repos/<repository-id>/repo.git` and worktrees at
`<root>/workspaces/<task-attempt-id>`. Every git call uses an argument array
with a hardened environment - no shell, and clone URLs (which may carry an
installation token) are never logged and are redacted from errors. The push
policy is enforced in code before git runs: branch under `agent-trail/`,
`origin` only, no force. Because a `--mirror` clone sets
`remote.origin.mirror=true`, that flag is disabled per push so an explicit
refspec cannot be reinterpreted as a mirror push.

Limitation: the per-repository fetch lock that serializes clone and fetch is
process-local. A single runner process per host is safe; sharing one on-disk
cache across processes on a host would need a cross-process file lock, deferred
until a deployment needs it.
