# ADR-0012: Publish-time conflict detection in the worker

- Status: accepted
- Date: 2026-07-31

## Context

Milestone 11 needs deterministic overlap warnings between active tasks of
one repository (conflict-detection.md phase 1). Detection needs git - the
diffs live only in the repository mirrors, never in the database - and the
mirror cache belongs to the worker process: gitworkspace documents that its
fetch lock is process-local, so a second process sharing the on-disk cache
would race it. The dashboard, meanwhile, polls the API for warnings and
must stay cheap to read.

## Decision

The worker detects conflicts at publish time, right after an attempt's
diff is pushed, and persists the result as one normalized row per task
pair; the API and dashboard only read stored rows, filtered to pairs whose
tasks are both still active.

## Alternatives

- Compute on demand in the API. Rejected: the API process has no workspace
  root, and giving it one either shares the mirror cache across processes
  (unsafe today) or duplicates it; every dashboard poll would also pay for
  git work that only changes when something publishes.
- A dedicated table row per detector kind. Rejected: the pair is the unit
  the UI shows and the publish rewrites; kinds fit naturally as a JSON
  array on the pair row.
- Recompute when a task turns terminal to clean rows up. Rejected: a
  read-time phase filter gives the same visible behavior with no cleanup
  job and keeps the row as history until either side republishes.

## Consequences

- Warnings are exactly as fresh as the last publish: a pair updates when
  either side publishes, never between publishes. That matches what the
  warning means - published diffs overlap - and costs one detection pass
  per publish instead of one per read.
- Both tasks' dashboards read the same row, so the warning is symmetric by
  construction.
- Detection failure is observability failure, not publish failure: it logs
  and the publish proceeds.
- A sibling published from another host is skipped until the mirror holds
  its commits (single-host MVP always does); multi-host detection needs a
  mirror fetch strategy first.

## Security implications

Detection is read-only against local mirrors using recorded commit SHAs;
it mints no credentials and never touches the network. Stored rows carry
only task ids, detector kinds, and file paths - no diff content leaves git.

## Revisit conditions

- Runners spread across hosts (detection starts skipping real siblings).
- Phase 2 structural detectors need more than changed paths and hunks.
- The publish path's latency budget stops affording the extra git calls.
