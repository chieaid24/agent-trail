# Conflict Detection

Deterministic warnings when two active tasks of one repository are editing
overlapping code. Warnings only: nothing is blocked, the human reviewing
the draft PRs decides (ADR-0012).

### Phase 1: file overlap (implemented)

Detection runs in the worker at publish time, after an attempt's diff is
committed and pushed: the just-published `base..final` range is compared
against the latest published range of every other non-terminal task of the
repository (`internal/conflict`). All comparisons are read-only git
plumbing against the repository's bare mirror cache (`internal/gitworkspace`):

- `git diff --name-only` - changed-path sets; a non-empty intersection is
  `file_overlap`.
- `git diff --unified=0` - base-side hunk ranges; edits to a shared file
  within 3 lines of each other are `adjacent_lines`.
- `git merge-tree --write-tree` - a temporary in-memory merge of the two
  final commits; conflicts are `merge_conflict` with the conflicted paths.
- Both diffs touching SQL files under a `migrations/` directory is
  `migration`, even when the files differ (ordering collides).
- Both diffs touching the same dependency manifest or lockfile
  (`go.mod`, `package-lock.json`, ...) is `dependency`.

Results persist as one row per task pair (`task_conflicts`,
data-model.md), rewritten on every publish of either side and surfaced
three ways: a `conflict.detected` activity event on the publishing task's
timeline, `GET /tasks/{taskId}/conflicts` (api.md), and a warning block on
the dashboard task detail page naming the other task, the detectors that
fired, and the files.

Known limits, accepted for phase 1:

- A sibling whose commits are not in the local mirror (published from
  another host) is skipped and logged, never guessed about. Single-host
  deployments - the MVP shape - always have the objects, because every
  attempt commits through the shared mirror object store.
- Adjacent-line coordinates are base-side; when the two tasks' base
  commits diverge around the compared lines the ranges are approximate.
  The temporary merge catches what the approximation misses.

### Phase 2: structural overlap

Detect:

- Same function
- Same class
- Same dependency file
- Same database table
- Same API schema
- Same Terraform module
- Same configuration keys

Possible tools:

- Tree-sitter
- OpenAPI parser
- SQL migration parser
- HCL parser

### Phase 3: semantic explanation

An LLM may explain deterministic conflicts.

Example:

```text
Potential conflict: high

Task A removes `users.refresh_token`.
Task B adds a reference to `users.refresh_token`.

Evidence:
- Both modify the users table.
- Task B references a column removed by Task A.
```

Do not block the MVP on semantic conflict detection.
