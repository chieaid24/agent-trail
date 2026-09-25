# ADR 0001: A revision continues the task's branch

Status: accepted, 2026-09-24

## Context

A task publishes one working branch and one draft pull request. Reviewers leave
feedback on that pull request and expect the agent to act on it. Two models were
considered for the follow-up attempt: continue the existing branch from the pull
request head, or redo the task from the original base with the feedback added
to the instructions. The platform already refuses force pushes and pins every
push to the `agent-trail/` branch namespace.

## Decision

A revision is attempt N+1 of the same task. It starts from the pull request head
at trigger time, human commits included, commits on top, and pushes normally to
the same branch and pull request. The platform never rebases the branch onto the
default branch and never force-pushes. Each attempt records its own base and
final commit, and evidence, validation results, and check runs stay per attempt.

## Consequences

- Review threads, discussion, and the pull request number survive across
  revisions, so a reviewer sees one continuous history.
- The no-force-push guarantee holds, so a runner can never rewrite history a
  human has already reviewed.
- Merge conflicts with the default branch remain a human decision; conflict
  detection reports them but a revision does not resolve them.
- A task that went badly wrong cannot be restarted from scratch under the same
  task; the human closes the pull request (which cancels a task awaiting review)
  and runs the issue again.
