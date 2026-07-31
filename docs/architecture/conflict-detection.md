# Conflict Detection

Deterministic warnings when two active tasks of one repository are editing
overlapping code. Warnings only: nothing is blocked, the human reviewing
the draft PRs decides (ADR-0012).

### Phase 1: file overlap (implemented)

Detection runs in the worker at publish time, after an attempt's diff is
committed and pushed: the just-published `base..final` range is compared
against the latest published range of every other non-terminal task of the
repository (`internal/conflict`). The worker refreshes the bare mirror from
origin first, including remote-tracking refs for agent branches published by
other hosts. Comparisons never change local branches or worktrees
(`internal/gitworkspace`):

- `git diff --name-only` - changed-path sets; a non-empty intersection is
  `file_overlap`.
- `git diff --unified=0` - base-side hunk ranges; edits to a shared file
  within 3 lines of each other are `adjacent_lines`.
- `git merge-tree --write-tree` - a temporary merge of the two final commits;
  conflicts are `merge_conflict` with the conflicted paths. Git may write
  unreachable objects, including result trees and auto-merged blobs, but
  changes no ref.
- Both diffs touching SQL files under a `migrations/` directory is
  `migration`, even when the files differ (ordering collides).
- Both diffs touching the same dependency manifest or lockfile
  (`go.mod`, `package-lock.json`, ...) is `dependency`.

After every comparison succeeds, the worker atomically reconciles the
publishing task's stored rows (`task_conflicts`, data-model.md). A missing,
not-yet-pushed sibling commit is skipped and any stale row for that pair is
removed. Detection failures leave the previous stored set unchanged and do
not stop publishing. Stored rows feed `GET /tasks/{taskId}/conflicts`
(api.md) and the dashboard warning block. The worker appends a
`conflict.detected` activity event after the row transaction; the timeline is
at-least-once and may briefly lag the stored warning while a publish retries.

Known limits, accepted for phase 1:

- A sibling commit recorded before its branch push completes cannot be fetched.
  That pair is skipped and logged; the other task's later publish performs the
  symmetric comparison.
- Adjacent-line coordinates are base-side; when the two tasks' base
  commits diverge around the compared lines the ranges are approximate.
  The temporary merge catches what the approximation misses.

### Phase 2: structural overlap

Detect:

- Same function
- Same class
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
