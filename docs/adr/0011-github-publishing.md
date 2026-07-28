# ADR-0011: GitHub publishing idempotency and the no-change outcome

- Status: accepted
- Date: 2026-07-28

## Context

Milestone 7 turns a validated attempt into GitHub surface: a commit on an
`agent-trail/` branch, one draft pull request carrying the evidence
report, one check run, and an issue comment, ending in `awaiting_review`.
The runner delivers at-least-once - an owner can die between any two
steps and a successor replays the stage - so every external effect needs
an idempotency anchor, and the workflow spec
(docs/architecture/publishing.md) names four keys: attempt id, branch
name, PR head branch, check external id. Separately, a session that
changes nothing must not open an empty PR, but the task state machine has
no no-change state and adding one would touch the schema, the phase
derivation, and every consumer, for an outcome that should be rare.

## Decision

Publishing runs in `internal/runner` inside the agent stages, while the
worktree exists, and realizes the keys as follows: the branch name is a
pure function of the task (issue number, title slug, task-id prefix) and
is recorded on the task first-writer-wins, so all owners converge on one
branch; the PR is looked up by head branch before creation and updated
when found; the check run carries the attempt id as `external_id` and is
searched for on the head commit before creation; `final_commit_sha` and
`pull_request_number` are first-write-wins columns on the attempt. A
recovered owner reattaches the surviving worktree, or publishes from the
already-pushed branch, or - when neither exists - fails the task as
`workspace_lost`. Commit idempotency distinguishes a clean tree at the
base commit (true no-change) from a clean tree whose HEAD moved (the
predecessor's commit).

The no-change outcome maps to the existing FAILED state with failure
code `no_change`, the explanation preserved in the failure message, a
`publishing.no_change` timeline event, a neutral check on the base
commit, and an explaining issue comment. `awaiting_review` leaves the
claimable set: it is the resting state where a human reviews the draft
PR.

Two supporting rules land in gitworkspace: mirror refreshes reset the
stored remote URL (installation tokens expire hourly) and exclude
`refs/heads/agent-trail/*` from refetch, because those branches originate
locally and git refuses to fetch into a branch checked out by a live
worktree.

## Alternatives

- A dedicated `no_change` status: honest but a schema and consumer change
  across the stack for a rare outcome; the spec explicitly allows the
  failed mapping, and the code and explanation keep it distinguishable.
- Completing no-change tasks as successful: reads as delivered work with
  nothing delivered; rejected as a silent lie.
- A database ledger of published side effects instead of GitHub-side
  lookups: duplicates state GitHub already holds and can itself diverge;
  GitHub is the source of truth for what exists on GitHub.

## Consequences

- Retries and recovered owners converge on one branch, one PR, one check;
  the issue comment alone is at-least-once and may rarely duplicate.
- Published tasks rest in `awaiting_review`; the review flow (approve,
  revise) is the next milestone's edge out of that state.
- The queued check from task creation stays on the base branch head; the
  publish-time check is a separate run on the pushed commit. Reconciling
  the two into one run is deferred until the check id is persisted.

## Security implications

- The installation token is embedded in the mirror's remote URL on disk
  for the lifetime of the cache entry; it is short-lived (one hour),
  never logged, redacted from errors, and refreshed on every mirror use.
  A host-level reader of `WORKSPACE_ROOT` can read the current token -
  the same trust boundary that already holds the worktrees themselves.
- The push policy (agent-trail/ namespace, origin only, no force) is
  unchanged and enforced in code before git runs.
- PR bodies, check output, and comments are built only from measured
  evidence fields; prompts and clone URLs never enter them.

## Revisit conditions

- The revision workflow lands (`/agent-trail revise`): the resting
  `awaiting_review` state gains outgoing edges and the branch-reuse rules
  here must extend to follow-up attempts.
- Runners move out of process or share hosts: the on-disk token exposure
  and the process-local mirror lock need the cross-process story ADR-0007
  deferred.
- No-change becomes common enough that a first-class status pays for its
  schema and consumer changes.
