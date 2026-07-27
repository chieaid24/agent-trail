# ADR-0008: Trusted validation

- Status: accepted
- Date: 2026-07-27

## Context

An agent's own account of its work cannot be trusted: a session that claims
"all tests passed" may have run nothing, run the wrong thing, or misread a
failure. The draft PR that ends a task needs verification the platform
measured itself. At the same time, what to verify is repository-specific -
only the repository knows its format, lint, test, and build commands - so the
check definitions must come from repository content, which the agent being
validated can edit. The design has to draw the trust boundary so that the
repository chooses *what* runs while the platform alone measures *how it
went*.

## Decision

A repository declares its checks in `.agent-trail/validation.yaml` (version 1,
strictly parsed, unknown fields rejected). After the agent session ends and
before the workspace is torn down, the executor runs each check sequentially
in the attempt workspace: argument arrays only, no shell, per-check timeout
(default 300s, cap 1800s) under a 3600s total budget, at most 20 checks.
Each result is persisted to `validation_results` with the measured exit code
and `trusted_execution = true` before it is announced as an activity event,
and a unique `(task_attempt_id, name)` constraint makes the first recording
final. Status classifies the outcome: `passed` and `failed` are measured exit
codes; `timed_out` and `error` (the command never ran) are infrastructure
outcomes and are never conflated with a check failure. Commands the agent
claimed to run enter the evidence report as claims with
`trusted_execution = false`; nothing an agent emits can create, alter, or
upgrade a stored result.

## Alternatives

- Trust the agent's reported command results: rejected outright - the point
  of the milestone is independent verification.
- Reuse the repository's CI (GitHub Actions) as the trusted run: rejected;
  it verifies a pushed commit, not the workspace about to become the PR, it
  adds a round trip through GitHub, and its configuration is broader
  arbitrary code than a bounded check list.
- Shell command strings in the validation file: rejected; every command runs
  on agent-editable input, and argument arrays remove the interpretation
  layer entirely (repo-wide rule since the runner landed).
- Parallel check execution: deferred; sequential runs keep resource use and
  event ordering simple, and the total-timeout budget bounds the phase.

## Consequences

- A repository without a validation file gets no trusted verification; the
  evidence report says so explicitly instead of inventing a result.
- An invalid file is an infrastructure-class outcome (`error`), never a pass
  and never a silent skip.
- Validation runs inside the agent stages, while the workspace still exists;
  a task recovered in `validating` after its owner died has lost the
  workspace, and is recorded as an infrastructure failure rather than reruns
  it cannot perform.
- A failing check does not abort the flow: the failure is recorded and the
  task proceeds to publishing, where the evidence-bearing draft PR is the
  reviewer's decision point.

## Security implications

- Running repository-declared commands is arbitrary code execution by
  design: the agent can edit the validation file, so a check can run
  anything the workspace user can. Today checks run in the same environment
  as the agent session; the containment boundary is the runner-isolation
  milestone, not this feature. Until then trusted validation proves *what
  the platform measured*, not that the command was benign.
- The limits (file size, check count, argument count, timeouts, bounded
  output capture) bound resource use, not maliciousness.
- No shell ever interprets the command; exit codes are recorded as measured;
  the database constraint set (status/category CHECKs, uniqueness) rejects
  out-of-vocabulary writes.

## Revisit conditions

- Runner isolation lands (checks should then run under the same sandbox
  profile as the agent, and this ADR's containment caveat shrinks).
- Repositories need structured reports (JUnit XML, coverage) preserved, not
  just exit codes and a summary line - `report_object_key` is reserved.
- Sequential execution becomes the long pole of task latency.
