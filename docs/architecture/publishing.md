# Publishing and Revision Workflow

## Publishing Workflow

1. Confirm the workspace uses the recorded base SHA.
2. Confirm the branch starts with `agent-trail/`.
3. Run trusted validation.
4. Generate evidence.
5. Create a commit if changes exist.
6. Push the branch.
7. Detect overlap with published active-task diffs.
8. Create or update one draft pull request.
9. Create or update one GitHub Check.
10. Post an issue comment.
11. Mark the attempt `AWAITING_REVIEW`.

Idempotency keys:

- Task attempt ID
- Branch name
- Pull-request head branch
- GitHub Check external ID

If there is no diff:

- Do not create an empty pull request.
- Mark the task as no-change or failed.
- Preserve the explanation.

## Implementation (ADR-0011)

Publishing lives in `apps/api/internal/runner` (`publish.go`) and runs
inside the agent stages, while the worktree still exists. How each
idempotency key is realized:

- The working branch is derived deterministically from the task
  (`issue-<n>-<title>-<task-id-prefix>`) and recorded on the task row,
  first writer wins, so every retry and recovered owner lands on one
  branch.
- The pull request is found by head branch before one is created; a found
  PR gets its body refreshed instead of a sibling.
- The check run carries the attempt id as `external_id` and is searched
  for on the head commit before one is created.
- `final_commit_sha` and `pull_request_number` on the attempt are
  first-write-wins.

A clean worktree whose HEAD equals the base is the no-change outcome: no
push, no PR, a `neutral` check on the base commit, an explaining issue
comment, and the task fails with code `no_change` carrying the
explanation (the state machine has no dedicated no-change state; the
failed state with a distinct code is the recorded mapping). A clean
worktree whose HEAD moved past the base is a recovered owner's earlier
commit and publishes as-is.

After the branch push, the worker records deterministic conflict warnings
before it creates or refreshes the pull request (ADR-0012). It refreshes the
mirror from origin, then atomically reconciles the publishing task's warnings.
Detection failure is logged and does not fail publishing; the draft pull
request still opens.

The issue comment is at-least-once: an owner that dies between the
comment and the final transition leaves a duplicate comment on retry,
matching the timeline's at-least-once delivery. `AWAITING_REVIEW` is the
resting state - runners do not claim it, and the human review gate on the
draft PR closes the loop.


## Revision Workflow

Post-MVP trigger:

```text
/agent-trail revise
```

Flow:

1. Validate reviewer authorization.
2. Collect unresolved review comments.
3. Create a new task attempt.
4. Recreate or restore the workspace.
5. Provide previous evidence and review comments.
6. Run the agent.
7. Revalidate.
8. Push new commits.
9. Update evidence.

Never overwrite the original attempt history.
