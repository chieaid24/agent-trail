# Pull request revisions browser verification

Issue: chieaid24/agent-trail#61.

The repository page gained a "revision limit" fact showing the per-repository
`max_attempts` setting, and the facts row now spans six columns on wide
screens so the validation path keeps one line. The full Playwright suite
passed 41 tests against an isolated PostgreSQL instance, API, worker, and
dashboard on lane-scoped ports. The read-model spec asserts the new fact and
its default value and captures the repository page at 1280 and 1024 pixels in
the populated, loading, error, and long-content states.

Visual review checked hierarchy, spacing rhythm, alignment across the facts
row, wrapping of long branch names and paths, clipping, and page overflow.
Monospace facts wrap at hyphens before breaking inside a word.

The final implementation also passed the frontend unit tests, the Go suite
with database tests enabled (including the fake pipeline that drives run,
revise, and merge), and `bash scripts/gate.sh` with the lane database
configured. Independent Standards, Spec, and Docs reviews ran before the pull
request opened; their objective findings were applied and the judgement
calls are recorded in the pull request body.

Reproduce browser verification from `apps/web` after running the Docker
preflight. Use a unique project name and localhost ports for each lane:

```bash
bash ~/.agents/skills/ensure-docker/scripts/ensure-docker.sh
E2E_PROJECT=agent-trail-61-e2e \
E2E_POSTGRES_PORT=5561 E2E_API_PORT=8161 \
E2E_WEB_PORT=3161 E2E_FAKE_GITHUB_PORT=7161 \
npx playwright test
```

The harness now waits for the fake GitHub server before the login flow and
releases its daemons and compose project when setup fails.

## Screenshots

| State | Desktop 1280 | Tablet 1024 |
| --- | --- | --- |
| populated | [1280](repository-detail-1280.png) | [1024](repository-detail-1024.png) |
| long-content | [1280](repository-detail-long-1280.png) | [1024](repository-detail-long-1024.png) |

## Dashboard attempt selector

Issue: chieaid24/agent-trail#62.

The task page gained an attempt selector for tasks with two or more attempts.
The timeline, trace, logs, validations, evidence, and files follow the
selected attempt; the latest attempt is selected by default and switching is
a client-side state change (a window marker set before the switch survives
it). The header names who requested the selected attempt and when, its base
and final commits, and the attempt's own instructions; the insights table and
cards highlight the selected attempt. `revision_requested` renders with the
new `info` tone on the task page, the overview, and the repository page.
Completed and cancelled tasks show their status reason beneath the title on
every list and beside the status on the task page.

The full Playwright suite passed 47 tests against an isolated PostgreSQL
instance, API, worker, and dashboard on lane-scoped ports. The revisions spec
pins four multi-attempt states with route mocks (`revision_requested` from a
second revise on a task whose attempt 2 has published, a running revision,
`awaiting_review` after the revision, and `completed` through a merged pull
request), switches to attempt 1 in the review state, mocks the overview and repository read models for the
badge and reasons, and checks the persisted single-attempt task shows no
selector while its attempt-scoped evidence read returns 200 for attempt 1 and
404 for attempt 2. Every task capture asserts no horizontal overflow at 1280,
1024, and 390 pixels, and the revision requested capture probes the header
rhythm: the selector and request line share the title's left edge and the
selector sits 16 pixels below the badge row.

Visual review checked the selector row and request line against the header
rhythm, meta-grid alignment with the added final commit, the highlighted
insights row and card, truncation of long titles on the overview (the reason
sits under the title so the title keeps its width), the stream chip reading
"stream ended" for every mocked state, and the mobile stack.

The final implementation also passed the frontend unit tests, the Go suite
with database tests enabled, and `bash scripts/gate.sh` with the lane database
configured. Independent Standards, Spec, and Docs reviews ran before the pull
request opened; their objective findings were applied and the judgement calls
are recorded in the pull request body.

Reproduce from `apps/web` after the Docker preflight:

```bash
bash ~/.agents/skills/ensure-docker/scripts/ensure-docker.sh
E2E_PROJECT=agent-trail-62-e2e \
E2E_POSTGRES_PORT=5662 E2E_API_PORT=8162 \
E2E_WEB_PORT=3162 E2E_FAKE_GITHUB_PORT=7162 \
npx playwright test
```

### Screenshots

| State | Desktop 1280 | Tablet 1024 | Mobile 390 |
| --- | --- | --- | --- |
| revision requested | [1280](task-revisions-revision-requested-1280.png) | [1024](task-revisions-revision-requested-1024.png) | [390](task-revisions-revision-requested-390.png) |
| running revision | [1280](task-revisions-running-1280.png) | [1024](task-revisions-running-1024.png) | [390](task-revisions-running-390.png) |
| awaiting review, attempt 2 | [1280](task-revisions-awaiting-review-1280.png) | [1024](task-revisions-awaiting-review-1024.png) | [390](task-revisions-awaiting-review-390.png) |
| awaiting review, attempt 1 selected | [1280](task-revisions-awaiting-review-attempt-1-1280.png) | [1024](task-revisions-awaiting-review-attempt-1-1024.png) | [390](task-revisions-awaiting-review-attempt-1-390.png) |
| completed | [1280](task-revisions-completed-1280.png) | [1024](task-revisions-completed-1024.png) | [390](task-revisions-completed-390.png) |

Status surfaces and the single-attempt control:

- [overview-1280](task-revisions-overview-1280.png)
- [repository-1280](task-revisions-repository-1280.png)
- [single-attempt-1280](task-revisions-single-attempt-1280.png)

