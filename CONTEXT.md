# Agent Trail glossary

Canonical terms for the product domain. Definitions only; behavior lives in
the code, the ADRs under `docs/adr/`, and the README.

- **Task**: the unit of work created from one `/agent-trail run` comment on a
  GitHub issue. One task owns one working branch and at most one pull request.
- **Attempt**: one execution of a task by a runner. A task has attempt 1 at
  creation; each revision adds attempt N+1 and supersedes attempt N.
- **Attempt base**: the commit an attempt starts from. Attempt 1 starts from
  the default branch head; a revision starts from the pull request head at
  trigger time, human commits included. The working branch is never rebased or
  force-pushed by the platform.
- **Revision**: a follow-up attempt on an existing task, started by the
  `/agent-trail revise` command on the task's pull request. The task moves
  `awaiting_review -> revision_requested -> queued`.
- **Revise command**: `/agent-trail revise`, posted as a comment on an Agent
  Trail pull request by a user with write or admin access. The only trigger for
  a revision; a submitted review by itself never starts one.
- **Run command**: `/agent-trail run`, posted on an issue. Creates a task.
- **Review feedback**: the reviewer text a revision acts on: every human-written
  review summary, inline review comment (with file path, line, and diff hunk),
  and conversation comment posted on the pull request since the previous
  revision was requested (every such comment for the first revision), plus the
  revise command body. Bot comments and other commands are excluded.
- **Publish**: the runner stage that pushes the working branch, creates or
  updates the pull request body with the evidence report and the attempts
  history, records the check run for the attempt's final commit, completes the
  trigger check run, and for a revision posts the revision summary.
- **Revision limit**: the per-repository `max_attempts` setting (default 5)
  in repository settings. A revise command on a task already at the limit is
  refused with a reply comment and no attempt.
- **Revision summary**: the single comment the platform posts on the pull
  request after a revision publishes, listing the attempt number, the final
  commit, the validation outcome, and which review feedback items it was given.
  The platform never replies inside review threads and never resolves them.
- **Attempts history**: the section of the pull request body that lists every
  attempt with its base commit, final commit, validation result, and reported
  cost. The evidence report above it always belongs to the latest attempt.
- **Attempt selector**: the dashboard control on a task page that switches the
  timeline, validations, evidence, and trace between attempts; the latest
  attempt is selected by default.
- **Trigger check run**: the `Agent Trail Task` check run created queued on the
  trigger head (the default branch for a run, the pull request head for a
  revise) when the command is accepted, before the attempt has a final commit.
  It is completed with the attempt's conclusion at publish, no change, failure,
  timeout, or cancellation.
