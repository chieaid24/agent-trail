# Local Development

One command starts everything:

```bash
make dev
```

It brings up the infrastructure in Docker Compose, applies migrations, then
runs the api, worker, and web dev servers natively in the foreground. Ctrl-C
stops the apps; `make clean` stops the infrastructure and drops its volumes.

## Layout

Docker Compose provides the infrastructure only:

- postgres (5432)
- redis (6379)
- minio (9000, console 9001)
- otel-collector (4317 gRPC, 4318 HTTP)
- prometheus (9090)
- grafana (3300)

The apps run natively for fast iteration - `go run` for api and worker,
`next dev` for web. All compose ports bind to localhost and every one is
overridable through `.env` (copy `.env.example`), so parallel checkouts can
coexist: set `COMPOSE_PROJECT_NAME` and the port variables per checkout.

Compose credentials (postgres, minio, grafana) are throwaway dev-only
values; nothing outside `docker-compose.yml` uses them.

## Commands

```bash
make dev               # infra + migrations + api + worker + web
make infra             # compose infrastructure only
make migrate           # apply database migrations (goose)
make seed              # demo tasks (skips when tasks already exist)
make test              # unit tests, both apps
make integration-test  # adds the suites that need a real database
make e2e               # browser suite against its own disposable stack
make demo              # scripted issue-to-PR demo - lands with milestone 7
make clean             # stop infra, drop volumes, remove build artifacts
make hooks             # activate the pre-commit hook (once per clone)
bash scripts/gate.sh   # the exact CI gate, locally
```

## Worker configuration

The worker is the runner host (docs/architecture/runner.md). Beyond
`DATABASE_URL` (required) it reads, all in whole seconds:

- `RUNNER_LEASE_SECONDS` (60): how long a claimed attempt stays owned
  without a lease extension
- `RUNNER_HEARTBEAT_SECONDS` (10): runner registry heartbeat and reap cadence
- `RUNNER_LOST_AFTER_SECONDS` (30): heartbeat staleness that marks a runner
  lost; must exceed the heartbeat interval
- `WORKER_POLL_SECONDS` (2): idle claim-poll interval

The worker also reads the agent adapter selection (`AGENT_PROVIDER` and the
other `AGENT_*` variables) - see docs/architecture/agent-providers.md and
`.env.example` for the list and defaults.

## Pre-commit hook

The committed hook in `.githooks/` runs `scripts/gate.sh` - the exact CI
gate - before every commit. Activate it once per clone:

```bash
make hooks             # git config core.hooksPath .githooks
```

## Browser e2e suite

`make e2e` runs the Playwright suite in `apps/web/e2e/`. Its global setup
boots a stack of its own - a dedicated postgres (compose project
`agent-trail-e2e`), migrations, seed data, and freshly built api and worker
binaries running the fake adapter - so it never touches the `make dev`
infrastructure, and tears everything down afterwards. The suite exercises
the dashboard against genuinely executed tasks, including an api restart
under an open SSE stream.

Parallel checkouts override the namespace and ports:

```bash
E2E_PROJECT=agent-trail-e2e-lane E2E_POSTGRES_PORT=5468 \
E2E_API_PORT=8108 E2E_WEB_PORT=3068 make e2e
```

Audit screenshots land in `apps/web/e2e/screenshots/`; the curated set for
the dashboard milestone is committed under `docs/screenshots/m8-dashboard/`.

## Demo repository

Still to build (milestone 5+): a small demo repository containing a backend
service, tests, a known issue, a validation file, agent instructions, and a
deterministic sample task.
