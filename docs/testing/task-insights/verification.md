# Task insights browser verification

Issue: chieaid24/agent-trail#51.

The full Playwright suite passed 41 tests against an isolated PostgreSQL
instance, API, worker, and dashboard. Insight states were captured at 1280,
1024, and 390 pixels. The persisted-data flow compared the API's attempt
count, runtime, event count, and reported cost with the rendered summary and
attempt rows. The existing trace tab remains available.

Visual review checked hierarchy, spacing, alignment, wrapping, clipping,
page overflow, status colors, and long content. Desktop comparison columns
remain reachable through the table's own horizontal scroll region; right-edge
captures record those values. Mobile attempts use cards, and task/repository
names retain meaningful width on the populated overview. Long validation
fractions stay on one line within their tile, and failure details wrap inside
a bounded column. Browser assertions verify these constraints.

The final implementation also passed 90 frontend unit tests and the full
Go suite with database tests enabled. `bash scripts/gate.sh` passed with the
isolated test database configured. Independent Standards, Spec, and Docs
reviews reported no remaining findings.

Reproduce browser verification from `apps/web` after running the Docker
preflight. Use a unique project name and localhost ports for each lane:

```bash
bash ~/.agents/skills/ensure-docker/scripts/ensure-docker.sh
E2E_PROJECT=agent-trail-51-insights-e2e \
E2E_POSTGRES_PORT=15552 E2E_API_PORT=18551 \
E2E_WEB_PORT=13551 E2E_FAKE_GITHUB_PORT=17551 \
npx playwright test
```

The harness disables external OTLP export while retaining persisted task
spans. It removes its processes, containers, and volume on teardown. The API
restart probe intentionally makes requests fail while the process is stopped;
the stream and task reads recover after restart.

## Screenshots

| State | Desktop 1280 | Tablet 1024 | Mobile 390 |
| --- | --- | --- | --- |
| loading | [1280](task-insights-loading-1280.png) | [1024](task-insights-loading-1024.png) | [390](task-insights-loading-390.png) |
| error | [1280](task-insights-error-1280.png) | [1024](task-insights-error-1024.png) | [390](task-insights-error-390.png) |
| empty | [1280](task-insights-empty-1280.png) | [1024](task-insights-empty-1024.png) | [390](task-insights-empty-390.png) |
| partial | [1280](task-insights-partial-1280.png) | [1024](task-insights-partial-1024.png) | [390](task-insights-partial-390.png) |
| populated | [1280](task-insights-populated-1280.png) | [1024](task-insights-populated-1024.png) | [390](task-insights-populated-390.png) |
| failed | [1280](task-insights-failed-1280.png) | [1024](task-insights-failed-1024.png) | [390](task-insights-failed-390.png) |
| multi-attempt | [1280](task-insights-multi-attempt-1280.png) | [1024](task-insights-multi-attempt-1024.png) | [390](task-insights-multi-attempt-390.png) |
| long-content | [1280](task-insights-long-content-1280.png) | [1024](task-insights-long-content-1024.png) | [390](task-insights-long-content-390.png) |
| persisted | [1280](task-insights-persisted-1280.png) | [1024](task-insights-persisted-1024.png) | [390](task-insights-persisted-390.png) |

Desktop table right edges:

- [long-content-1024-right](task-insights-long-content-1024-right.png)
- [long-content-1280-right](task-insights-long-content-1280-right.png)
- [multi-attempt-1024-right](task-insights-multi-attempt-1024-right.png)
- [multi-attempt-1280-right](task-insights-multi-attempt-1280-right.png)
- [populated-1024-right](task-insights-populated-1024-right.png)

Mobile navigation and overview states:

- [empty-390](task-insights-mobile-navigation-empty-390.png)
- [error-390](task-insights-mobile-navigation-error-390.png)
- [installations-390](task-insights-mobile-navigation-installations-390.png)
- [loading-390](task-insights-mobile-navigation-loading-390.png)
- [populated-390](task-insights-mobile-navigation-populated-390.png)
