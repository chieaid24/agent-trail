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
- postgres-exporter (Compose network only)

The apps run natively for fast iteration - `go run` for api and worker,
`next dev` for web. All compose ports bind to localhost and every one is
overridable through `.env` (copy `.env.example`), so parallel checkouts can
coexist: set `COMPOSE_PROJECT_NAME` and the port variables per checkout.

Compose credentials (postgres, minio, grafana) are throwaway dev-only
values; nothing outside `docker-compose.yml` uses them.

The native API and worker export metrics and traces to
`localhost:${OTLP_GRPC_PORT:-4317}`. The collector exposes app metrics inside
Compose, Prometheus scrapes the collector plus the PostgreSQL and MinIO
exporters, and Grafana reads Prometheus. Open Grafana at
`http://localhost:${GRAFANA_PORT:-3300}` with `admin` / `admin`; the seven
dashboards are in the `Agent Trail` folder and the nine rules are under Alerting.
If `OTLP_GRPC_PORT` changes, set `OTEL_EXPORTER_OTLP_ENDPOINT` to the same host
port. Set it to `off` when no collector is available.

## Commands

```bash
make dev               # infra + migrations + api + worker + web
make infra             # compose infrastructure only
make migrate           # apply database migrations (goose)
make seed              # demo tasks and repositories (skips when tasks exist)
make test              # unit tests, both apps
make integration-test  # adds the suites that need a real database
make e2e               # browser suite against its own disposable stack
make bench             # benchmarks + failure injection, disposable database
make demo              # scripted issue-to-PR demo against a simulated GitHub
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
- `AGENT_TIMEOUT_SECONDS` (2700): default attempt runtime when a task omits
  `max_runtime_seconds`; the executor enforces it for every adapter

The worker also reads the agent adapter selection and CLI settings - see
docs/architecture/agent-providers.md and `.env.example` for the list and
defaults - plus `RUNNER_TYPE` (process, docker, or kubernetes) and the one-shot
controls `WORKER_MAX_TASKS` and `WORKER_IDLE_EXIT_SECONDS` that a Kubernetes
Job runner sets so the Job completes and TTL cleanup applies.

The Kubernetes Job template requires an explicit
`OTEL_EXPORTER_OTLP_ENDPOINT`. The local kind verifier sets it to `off`; a live
deployment must provide a reachable collector service address.

## Kubernetes runner verification

`bash scripts/verify-k8s-runner.sh` proves the runner-isolation acceptance
criteria without any cloud resources: it builds the runner and tools images
(deploy/docker/Dockerfile), creates a throwaway kind cluster named per run,
stands up a disposable postgres and the GitHub fixture
(cmd/fixture-github), launches the hardened one-shot runner Job
(deploy/k8s/runner/job.yaml), asserts the pod spec hardening, checks the
task reached awaiting_review with a draft PR through the fixture's /verify
endpoint, and confirms ttlSecondsAfterFinished removed the finished Job.
Everything it creates is namespaced per run; on success it is all deleted,
on failure the log and pod-spec artifacts are kept and their path printed
(`KEEP_CLUSTER=1` also keeps the cluster for debugging). Requires docker,
kind, kubectl, curl, openssl, and python3.

## Dashboard sign-in

Without OAuth credentials the API is open on localhost and the dashboard
shows no user chip. To run with the session layer on, set
`GITHUB_OAUTH_CLIENT_ID` and `GITHUB_OAUTH_CLIENT_SECRET` in `.env` (from
the GitHub App's settings page, docs/architecture/github-app.md) and
register `http://localhost:3000/backend/auth/github/callback` as a
callback URL on the app. Every `/api/v1` route then requires signing in at
`http://localhost:3000/login`. Override `AUTH_PUBLIC_ORIGIN` when the web
port is not 3000; set `AUTH_COOKIE_SECURE=true` only behind HTTPS.

## Pre-commit hook

The committed hook in `.githooks/` runs `scripts/gate.sh` - the exact CI
gate - before every commit. Activate it once per clone:

```bash
make hooks             # git config core.hooksPath .githooks
```

## Browser e2e suite

`make e2e` runs the Playwright suite in `apps/web/e2e/`. Its global setup
boots a stack of its own - a dedicated postgres (compose project
`agent-trail-e2e`), migrations, seed data, a fake GitHub (OAuth and user
endpoints), and freshly built api and worker binaries running the fake
adapter - so it never touches the `make dev` infrastructure, and tears
everything down afterwards. The api runs with the session layer on: global
setup signs in through the fake GitHub once and every spec reuses that
session, so the suite exercises the dashboard as a signed-in user against
genuinely executed tasks, including an api restart under an open SSE
stream and the full sign-in round-trip.

Parallel checkouts override the namespace and ports:

```bash
E2E_PROJECT=agent-trail-e2e-lane E2E_POSTGRES_PORT=5468 \
E2E_API_PORT=8108 E2E_WEB_PORT=3068 E2E_FAKE_GITHUB_PORT=7068 make e2e
```

Audit screenshots land in `apps/web/e2e/screenshots/`. Curated evidence for
each dashboard change lives under `docs/screenshots/`.

## Benchmarks

`make bench` (or `scripts/bench.sh`) boots a dedicated postgres (compose
project `agent-trail-bench`, port 5493 by default), migrates it, and runs
the gated benchmark and failure-injection suite in
`apps/api/internal/bench/`. The tests skip without `AGENT_TRAIL_BENCH=1`,
so plain `go test ./...` and the CI gate never run them. The full-disk
injection needs passwordless sudo for a tmpfs mount and skips without it.
Parallel lanes override the namespace and port:

```bash
BENCH_PROJECT=agent-trail-bench-lane BENCH_POSTGRES_PORT=5494 make bench
```

Run output lands in `apps/api/internal/bench/.artifacts/`; the committed
methodology and measured numbers are `docs/testing/benchmark-results.md`.

## Demo repository

Still to build (milestone 5+): a small demo repository containing a backend
service, tests, a known issue, a validation file, agent instructions, and a
deterministic sample task.
