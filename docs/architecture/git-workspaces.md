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
