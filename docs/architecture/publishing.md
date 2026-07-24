# Publishing and Revision Workflow

## Publishing Workflow

1. Confirm the workspace uses the recorded base SHA.
2. Confirm the branch starts with `agent-trail/`.
3. Run trusted validation.
4. Generate evidence.
5. Create a commit if changes exist.
6. Push the branch.
7. Create or update one draft pull request.
8. Create or update one GitHub Check.
9. Post an issue comment.
10. Mark the attempt `AWAITING_REVIEW`.

Idempotency keys:

- Task attempt ID
- Branch name
- Pull-request head branch
- GitHub Check external ID

If there is no diff:

- Do not create an empty pull request.
- Mark the task as no-change or failed.
- Preserve the explanation.


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
