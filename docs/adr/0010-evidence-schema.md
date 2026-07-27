# ADR-0010: Evidence schema

- Status: accepted
- Date: 2026-07-27

## Context

The product's claim is an evidence-backed draft PR: a reviewer should see
what was requested, what changed, and what was verified without replaying
the run. That evidence must separate two kinds of statement that look alike
in prose - facts the platform measured, and things the agent said - and it
must survive schema evolution, because reports are written once and read for
the life of the task.

## Decision

Evidence is a versioned JSON document (`schema_version: 1`) generated per
attempt from stored data: the task row, the attempt's persisted trusted
validation results, its stored `started_at` (duration is measured against
the generation-time clock), and the agent's event stream (plan, file
changes, claimed commands). Claims are carried as validation entries with
`trusted_execution = false`; fields nothing measured are omitted, never
invented, and anything unverified is said outright in an `unverified` list.
The document and a rendered Markdown summary are stored together in
`evidence_reports`, one row per attempt (`task_attempt_id` unique, first
write wins), and served by `GET /tasks/{taskId}/evidence`. The Markdown
renderer keeps trusted results and agent claims in visibly separate
sections; only platform-executed checks appear under "Verified by Agent
Trail".

## Alternatives

- Compose the PR body ad hoc at publish time with no stored document:
  rejected; the report is the audit record, and the PR body is just one
  rendering of it.
- Store only the Markdown: rejected; the dashboard and any later tooling
  need the structured form, and re-parsing prose is how agent text would
  leak back into "facts".
- Store only the JSON and render on read: rejected; the summary shown to a
  human should be the summary that was generated at the time, not subject
  to renderer drift.
- Full spec schema from day one (permissions, policy version, runner image):
  deferred; those fields describe subsystems that have not landed, and
  inventing values would violate the no-invented-metrics rule. The schema
  version exists precisely so they can arrive as measured facts.

## Consequences

- Reports draw on the database alone (plus the clock behind
  `duration_seconds`); losing the workspace loses no evidence.
- A replayed generation (an owner recovering past its lease) is a no-op;
  the first report stands.
- The spec's remaining fields (permissions, human interventions, runner
  image, policy version) require a schema bump when their subsystems land.
- Consumers must treat unknown future fields as additive and key behavior
  off `schema_version`.

## Security implications

- The report never contains prompts, raw agent transcripts, or secrets;
  inputs are the task row, stored results, and typed event payload fields.
- Agent-influenced text (plan steps, file paths, claimed commands) is
  carried as data and labeled as unverified claim, so a reader cannot
  mistake it for platform measurement.
- The `unverified` list is generated, not agent-supplied: the platform
  itself states what it could not verify.

## Revisit conditions

- Publishing lands and the PR body needs links, artifact references, or
  fields v1 lacks (schema v2).
- Reports outgrow row storage and move behind `report_object_key`.
- A consumer needs machine-readable diff/area classification beyond the
  file list.
