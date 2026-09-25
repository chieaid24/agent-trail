# Agent Trail - Architecture Overview

Agent Trail turns a GitHub issue comment into an evidence-backed draft pull request. A signed webhook creates a durable task, a worker claims it from a PostgreSQL queue, a coding agent runs in an isolated Git worktree, the platform validates the result outside the agent's session, and a draft PR opens with an evidence report and a check run. A human reviews: commenting `/agent-trail revise` on the pull request starts a revision that continues the same branch with the review feedback, merging the pull request completes the task, closing it without merge cancels the task. Terms follow `CONTEXT.md`; the branch model follows ADR 0001.

This file pairs with the two HTML diagrams in `docs/`: `system-diagram.html` (local development topology) and `system-diagram-aws.html` (production topology on AWS). Diagram node names below match the box titles in those files.

## Data flow, end to end

Each step names the diagram node it runs in and the durable record it leaves behind.

1. **Trigger** - a developer with `write` or `admin` on an enabled repository comments `/agent-trail run` on an issue, or `/agent-trail revise` on an Agent Trail pull request once the task awaits review. Node: Developer -> GitHub (App).
2. **Webhook intake** - GitHub delivers `issue_comment` and `pull_request` to `POST /webhooks/github`. The API caps the body at 1 MiB, verifies `X-Hub-Signature-256` (HMAC-SHA256), and inserts the delivery id into `github_webhook_deliveries`; a repeat id returns `202 duplicate`. Node: GitHub -> ALB -> API. Record: `github_webhook_deliveries`.
3. **Task creation** - the in-process processor parses the exact command, rejects bots, redirects `run` on a pull request and `revise` on an issue with a reply, checks repository enablement and commenter permission over the GitHub REST API, then in one transaction inserts `tasks`, `task_attempts` (attempt 1, carrying the requester login and trigger comment id), and a `task.created` event, and transitions the task to `queued`. A unique index on active tasks per issue is the race guard. The API creates a queued trigger check run on the default branch head, records its id on the attempt, and acknowledges with an issue comment. Node: API -> RDS PostgreSQL. Records: `tasks`, `task_attempts`, `activity_events`.
4. **Claim** - the worker polls every 2 s and claims one attempt with `SELECT ... FOR UPDATE OF a SKIP LOCKED`, ordered by priority then age, setting `lease_owner` and a 60 s `lease_expires_at`. In Kubernetes mode the controller lists dispatchable attempts and creates one Job per attempt; Job creation is the scheduling compare-and-swap, and the Job's worker then claims exactly its own row via `TASK_ATTEMPT_ID`. Node: Worker or runner-controller -> Hardened Job pod -> RDS. Record: `task_attempts` lease columns, `runner.*` events.
5. **Provisioning** - the worker mints a GitHub installation token, refreshes the bare mirror at `repos/<repository_id>/repo.git`, and creates a worktree at `workspaces/<task_attempt_id>` on branch `agent-trail/<label>` from the recorded base commit. A revision attempt continues the existing branch: its base is the pull request head recorded at trigger, and the worktree is created only if origin's branch tip still equals that base; a moved tip fails the attempt with `base_mismatch`. Node: Workspace disk (emptyDir in Kubernetes). Record: `task_attempts.base_commit_sha`.
6. **Planning and executing** - the agent adapter (`fake` or `claude-code`) streams normalized events (`agent.started`, `plan.created`, `command.*`, `file.changed`, `agent.cost_update`, ...) into `activity_events`. The runner uses the attempt's own instructions when present (a revision's composed instructions) and the task's otherwise; every attempt is a fresh agent session. The executor extends the lease throughout and stops on `ErrLeaseLost` rather than racing a new owner. Node: Agent adapter -> Anthropic API. Record: `activity_events`.
7. **Validating** - after the agent session ends, the platform runs the checks declared in the repository's `.agent-trail/validation.yaml` as argv arrays with no shell, bounded at 20 checks, 300 s per check by default, 3600 s total. Each check lands in `validation_results` with `trusted_execution = true`. The evidence generator then writes one `evidence_reports` row per attempt. Node: Trusted validation -> Evidence report. Records: `validation_results`, `evidence_reports`.
8. **Publishing** - the worker commits, pushes the branch (fast-forward only; force pushes are refused in code), runs conflict detection against sibling tasks (file overlap, adjacent lines, merge conflict, migration, dependency, and semantic via the Anthropic Messages API), then creates or updates the draft PR whose body is the latest attempt's evidence report followed by the attempts history (base commit, final commit, trusted validation outcome, reported cost per attempt), upserts the check run `Agent Trail Task` keyed by attempt id on the final commit, and completes the attempt's trigger check run with the same conclusion. Attempt 1 comments the PR link on the issue; a revision posts one revision summary on the pull request naming the attempt number, final commit, validation outcome, and the feedback items it was given, never a reply inside a review thread. Evidence, validation results, spans, and check runs stay per attempt. Conflict detection failure never blocks publishing. Node: Hardened Job pod -> NAT Gateway -> GitHub. Records: `task_conflicts`, `task_attempts.pull_request_number`.
9. **Review, revision, and completion** - the task rests in the review phase (`awaiting_review` or `revision_requested`) until a reviewer acts. `/agent-trail revise` on the pull request (write or admin, human author) maps the head branch (`agent-trail/...`, pushed to this repository, never a fork) to the task and, when the task awaits review and its attempt count is below the repository's `max_attempts` setting, gathers every human-written review summary, inline review comment (path, line, diff hunk), and conversation comment posted since the previous publish plus the revise comment body, composes revision instructions from the task title and instructions, a statement that the branch already holds the earlier attempts' work, and the feedback in chronological order, and transitions `awaiting_review -> revision_requested -> queued` in one transaction that supersedes attempt N and inserts attempt N+1 with its own instructions, base commit, requester, trigger comment id, and feedback list. A queued trigger check run on the pull request head and an ack comment follow. Each guard (revise on an issue, run on a pull request, disabled repository, missing permission, running or terminal task, revision limit, non Agent Trail pull request) replies once and creates nothing; bot comments and redelivered commands are ignored. A submitted review never starts a revision by itself. GitHub delivers `pull_request` with action `closed`; the processor transitions the task to `completed` on merge or `cancelled` on close without merge, recording a `system` event that names the pull request. A pull request closed while the task is still running leaves it untouched, and a reopened pull request does not revive a finalized task. Repeated deliveries are no-ops: the delivery ledger drops a replayed id, a finalized task ignores a repeat close, and concurrent copies collapse on the transition idempotency key. Throughout, the dashboard streams `activity_events` over Server-Sent Events (1 s poll, `Last-Event-ID` cursor, 15 s heartbeat) and, after the run, polls `GET /api/v1/tasks/{id}/trace` to build the waterfall from `task_spans`. Node: Reviewer -> GitHub (App) -> API -> RDS. Records: `tasks.status`, `task_attempts`, `activity_events`.

Recovery is lease-driven. Expiry, not runner status, makes an attempt claimable again; a heartbeat reaper marks silent runners `lost`; a recovered attempt reattaches a surviving worktree, otherwise republishes from the pushed branch, otherwise fails as `workspace_lost`. A task that ends `failed`, `cancelled`, or `timed_out` while a runner owns it completes its trigger check run with that conclusion; a task cancelled before any runner claims it keeps a queued trigger check run.

## Technologies and where they sit in the diagram

### Entry and identity

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| GitHub App | - | Webhook source (`installation`, `installation_repositories`, `issue_comment`, `pull_request`), REST client for tokens, permissions, comments, PRs, check runs | GitHub (App) |
| GitHub OAuth | - | Dashboard login; session token stored as SHA-256 only | GitHub (App) -> API |
| smee.io | - | Local-only webhook tunnel to `localhost:8080` | smee.io tunnel (local diagram only) |
| Route53 + ACM | Terraform `dns_tls` | Hosted zone, alias record, TLS certificate | Route53 + ACM |
| Application Load Balancer | Terraform `control_plane` | HTTPS :443, TLS 1.3 policy, forwards to container :8080, health check `/healthz` | ALB (public subnets) |

### Control plane

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| Go | 1.26.5 | Language for `api`, `worker`, `migrate`, `seed`, `slice`, `fixture-github` | API, Worker, runner-controller, Job pod |
| `api` binary | - | Webhook receiver, `/api/v1` REST (including `PUT /repositories/{id}/settings` for the per-repository `max_attempts` revision limit, default 5, bounded 1-20), SSE stream, OAuth, `/metrics`, `/healthz`, `/readyz`; hosts the webhook processor goroutine | API control-plane :8080 |
| `worker` binary | - | Process runner (`RUNNER_TYPE=process`) or Kubernetes controller (`RUNNER_TYPE=kubernetes`); same binary runs inside each Job with `TASK_ATTEMPT_ID` | Worker, runner-controller, Hardened Job pod |
| goose | v3.27.3 | Embedded SQL migrations `00001_init` through `00011_pull_request_revisions` | RDS PostgreSQL (schema) |
| ECS Fargate | Terraform `control_plane` | Runs the `api` container, ARM64, deployment circuit breaker with rollback; prod: 2 tasks at 1024 CPU / 2048 MiB | ECS Fargate group |
| Next.js | 16.2.12 | Dashboard: `/`, `/tasks/[id]`, `/repositories/[id]`, `/runners/[id]`, `/installations`, `/login`; proxies `/backend/*` to the API | Dashboard (Next.js) |
| React | 19.2.8 | Dashboard UI runtime | Dashboard (Next.js) |
| TypeScript | 5.9.3 | Dashboard language | Dashboard (Next.js) |
| Tailwind CSS | 4.3.3 | Dashboard styling | Dashboard (Next.js) |

The dashboard is not in Terraform. The production diagram draws it dashed as a second Fargate service behind ALB path routing.

### Data

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| PostgreSQL | 17.7 (compose, CI) / RDS engine 16.8 default | Single datastore and work queue: `tasks`, `task_attempts`, `activity_events`, `runners`, `validation_results`, `evidence_reports`, `task_conflicts`, `task_spans`, `organizations`, `github_installations`, `repositories`, `github_webhook_deliveries`, `users`, `sessions`, `memberships` | RDS PostgreSQL |
| Amazon RDS | Terraform `database` | Managed PostgreSQL, private subnet, encrypted gp3, Performance Insights; prod multi-AZ `db.r6g.large`, 14-day backups, deletion protection | RDS PostgreSQL |
| Local disk | - | `WORKSPACE_ROOT` (default `/var/lib/agent-trail`, `/workspace` in a Job): bare mirror per repository plus one worktree per attempt | Workspace disk / emptyDir 10Gi |
| Amazon SQS | Terraform `queue` | `task-dispatch` queue with dead-letter redrive. Declared in both envs and created on apply; no code path reads or writes it, queueing runs in PostgreSQL | SQS task-dispatch + DLQ (dashed) |
| Amazon S3 | Terraform `object_storage` | Versioned, encrypted, TLS-only artifacts bucket. Declared in both envs and created on apply; no code path writes it, `report_object_key` columns exist on validation and evidence tables | S3 artifacts bucket (dashed) |
| Secrets Manager | Terraform `secrets` | `github-app-private-key`, `github-webhook-secret`, `agent-provider-api-key`, `dashboard-auth-secret`; values set out of band | Secrets Manager |

There is no Redis, no message broker, and no object storage in application code. Queue semantics come from `FOR UPDATE SKIP LOCKED`, lease columns, and partial unique indexes (`tasks_one_active_per_issue_idx`, `task_attempts_one_active_idx`).

### Execution and isolation

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| Kubernetes Jobs API | client-go v0.36.4 | Controller creates, gets, lists, watches, deletes `batch/v1` Jobs; nothing else | runner-controller |
| Job template | `deploy/k8s/runner/job.yaml` | Per-attempt pod: nonroot 65532, read-only root, `automountServiceAccountToken: false`, seccomp `RuntimeDefault`, drop all capabilities, 500m/1Gi request, 2 CPU / 4Gi limit, 10Gi workspace emptyDir, `backoffLimit: 0`, `ttlSecondsAfterFinished` 300 | Hardened Job pod |
| RBAC | `deploy/k8s/runner/serviceaccount.yaml` | Role `runner-controller` limited to Job verbs; SA `runner-task` has no permissions | EKS group |
| NetworkPolicy | `deploy/k8s/runner/networkpolicy.yaml` | Default deny ingress and egress, allow DNS, allow controller egress to the Kubernetes API only | EKS group |
| Pod Security Admission | `deploy/k8s/runner/namespace.yaml` | Namespace `agent-trail-runners` enforces `restricted` | EKS group |
| Amazon EKS | Terraform `runner_cluster` | Private-endpoint cluster, Kubernetes 1.33, ARM64 managed node group labelled `agent-trail.dev/role=runner`, IRSA roles for controller and task | EKS runner cluster |
| kind | v0.30.0 | CI verifier: builds runner and tools images, asserts RBAC and pod hardening, runs one fixture task to `awaiting_review`, confirms TTL cleanup | not drawn; exercises the EKS group locally |
| NAT Gateway | Terraform `network` | Private-subnet egress to GitHub and Anthropic | NAT Gateway |
| Amazon ECR | Terraform `container_registry` | `control-plane` and `runner` images, immutable tags, scan on push | ECR |
| Docker | multi-stage `deploy/docker/Dockerfile` | Targets `control-plane` (api), `runner` (worker + job template), `tools` (migrate, seed, fixture-github); base `golang:1.26-alpine3.23` | ECR (image source) |

### Agent and language model

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| Fake adapter | `AGENT_PROVIDER=fake` | No-cost adapter emitting the full event contract; default in tests and the kind verifier | Agent adapter |
| Claude Code CLI | pinned by `AGENT_CLI_VERSION` | `claude --print --output-format stream-json --permission-mode <mode>`; refuses to start on a version mismatch; killed by process group on cancel | Agent adapter |
| Anthropic Messages API | `2023-06-01`, default model `claude-sonnet-4-6` | Semantic conflict detection only, 40 KiB diff cap, 10 s timeout, behind `CONFLICT_LLM_ENABLED` | Anthropic API |

### Validation and evidence

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| `.agent-trail/validation.yaml` | schema in `internal/validation` | Repository-declared checks; loaded through `os.OpenRoot`, max 20 checks, 1 MiB file, 64 KiB output per check | Trusted validation |
| Evidence report | schema version 1 | Task, execution (provider, model, base and final commit, duration), plan, changed files, checks with `TrustedExecution`, risks, unverified claims | Evidence report |
| GitHub check run | name `Agent Trail Task` | One queued trigger run per attempt on the trigger head (default branch for a run, pull request head for a revise), completed at publish, failure, timeout, or cancellation; one run per attempt on the final commit. Conclusion `success` only when every trusted check passed, `failure` on any failed trusted check, otherwise `neutral` | Evidence report -> GitHub |

### Observability

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| OpenTelemetry Go SDK | v1.45.0 | Traces and metrics from `agent-trail-api` and `agent-trail-worker`, W3C tracecontext propagation | OpenTelemetry / OTLP |
| OTLP exporter | gRPC, `OTEL_EXPORTER_OTLP_ENDPOINT` | Default `localhost:4317`; `off` disables export; Kubernetes runs set it explicitly | OpenTelemetry / OTLP |
| OpenTelemetry Collector | contrib 0.130.0 (compose) | Local receiver on 4317/4318 with the debug exporter | Observability band (local) |
| Task span store | table `task_spans` | Worker-side second span exporter persisting task-scoped spans for the dashboard waterfall | RDS -> Dashboard |
| Prometheus exposition | `GET /metrics` on the API | About 25 `agent_trail_*` series: task lifecycle, runner resources, command and validation durations, webhook and GitHub API counters, auth and session counters | API control-plane |
| slog | Go stdlib | JSON logs stamped with `trace_id` | all Go nodes |
| CloudWatch | Terraform `observability`, `control_plane`, `network`, `database`, `runner_cluster` | Container Insights, ECS log group, VPC flow logs, RDS log export, EKS control-plane logs, six alarms (`alb-5xx`, `no-healthy-hosts`, `db-cpu`, `db-storage`, `queue-age`, `dead-letter-depth`) | CloudWatch |
| Amazon SNS | Terraform `observability` | Alerts topic with optional email subscription | SNS alerts topic -> Operator email |

### Delivery and tooling

| Technology | Version | Role | Diagram node |
|---|---|---|---|
| Terraform | >= 1.9.0 (CI 1.15.2), AWS provider ~> 6.0, TLS provider ~> 4.0 | Modules `network`, `control_plane`, `database`, `queue`, `object_storage`, `secrets`, `container_registry`, `dns_tls`, `runner_cluster`, `observability`, `github_oidc_ci`; envs `dev` and `prod`; S3 backend | AWS band |
| GitHub Actions | workflow `ci.yml`, job `test` | Postgres 17.7 service, Go from `go.mod`, Node 24, Terraform 1.15.2, `scripts/gate.sh`, then kind verifier; never applies Terraform | GitHub Actions CI |
| GitHub OIDC | Terraform `github_oidc_ci` | Deploy role for ECR push and ECS update, scoped to this repository | GitHub Actions CI -> ECR |
| `scripts/gate.sh` | - | gofmt, go vet, go test, go build; terraform fmt and validate; npm format, lint, test, build; also the pre-commit hook via `make hooks` | GitHub Actions CI |
| Docker Compose | project `agent-trail` | Local `postgres` and `otel-collector`, both bound to 127.0.0.1 | local diagram |
| Vitest | 4.1.10 | Dashboard unit tests | Dashboard |
| Playwright | 1.62.0 | Dashboard end-to-end harness with a fake GitHub server on isolated ports | Dashboard |
| Node.js | 24 | Dashboard runtime and CI | Dashboard |

## Local versus production

| Concern | Local (`make dev`) | Production (Terraform) |
|---|---|---|
| Entry | `:8080` direct, webhooks via smee.io | Route53 -> ALB :443 -> Fargate :8080 |
| Dashboard | Next.js dev server `:3000` | Not provisioned; drawn dashed |
| Database | `postgres:17.7-alpine` on 127.0.0.1:5432 | RDS, private subnets, multi-AZ |
| Runner | `RUNNER_TYPE=process`, worktrees under `/var/lib/agent-trail` | `RUNNER_TYPE=kubernetes` on EKS, one Job per attempt |
| Images | local build | ECR `control-plane` and `runner` |
| Secrets | `.env` | Secrets Manager plus Kubernetes Secrets `runner-database`, `runner-github`, `runner-agent` |
| Telemetry | collector with debug exporter on 4317 | OTLP endpoint set per environment; CloudWatch alarms to SNS |
| Queue | PostgreSQL | PostgreSQL; SQS created on apply, unused by code |
| Artifacts | PostgreSQL rows | PostgreSQL rows; S3 created on apply, unused by code |

## Boundaries worth knowing

- Trusted validation is a separate platform step, not a second sandbox. Its isolation is the runner's own boundary: the Job pod in Kubernetes, the host process otherwise.
- The controller never claims work. A Job may claim only the attempt named in `TASK_ATTEMPT_ID`, so a compromised Job cannot pull arbitrary queue items.
- Span attributes and status messages never carry credentials or source content; they persist with the task audit record.
- The check run conclusion is `neutral` whenever no trusted check ran, so an agent's own claim of passing tests never turns the PR green.
