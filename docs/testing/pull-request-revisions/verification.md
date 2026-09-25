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
