# Agent Trail

Agent Trail is a control plane for coding agents. Comment `/agent-trail run` on a GitHub issue and it creates a durable task, runs a coding agent in an isolated workspace with scoped credentials, streams every action to a dashboard, independently validates the result, and opens a draft pull request with an evidence report. A human approves the merge.

Status: the issue-to-PR path runs end to end. A signed GitHub webhook creates a durable task; the worker claims it from a PostgreSQL queue, runs an agent (a no-cost fake, or the Claude Code CLI behind `AGENT_PROVIDER`) in an isolated Git worktree, validates the result outside the agent's session, and opens a draft pull request backed by an evidence report. The dashboard streams lifecycle and cost events over Server-Sent Events (SSE), loads completed OpenTelemetry spans as they become available, and renders them as a trace waterfall. Process execution remains the default. With `RUNNER_TYPE=kubernetes`, a controller creates one hardened Kubernetes Job per task attempt and watches it through completion. Terraform defines the AWS network, compute, database, queue, artifact, identity, and monitoring foundations; apply the Kubernetes workloads, policies, and Secrets separately.

## Quickstart

Requires Go 1.26+, Node 24+, and Docker.

```bash
make dev      # compose infra + migrations + api, worker, web
make test     # unit tests for both apps
make hooks    # activate the pre-commit hook (once per clone)
```

`make dev` serves the API on :8080, the dashboard on :3000, and Grafana on
http://127.0.0.1:3300. Grafana grants anonymous Admin access on localhost; the
built-in `admin` / `admin` account also authenticates HTTP API calls. See
[.env.example](.env.example) for port settings and the [Makefile](Makefile) for
available targets.

Run the offline infrastructure checks before opening a pull request:

```bash
bash scripts/gate.sh
bash scripts/verify-production-telemetry.sh
bash scripts/verify-k8s-runner.sh
```

The Kubernetes check renders the production collector endpoint as `off` inside its isolated kind cluster.

## Observability data

The local `grafana/otel-lgtm` Compose service (Loki, Grafana, Tempo, and
Prometheus) receives OpenTelemetry Protocol (OTLP) metrics and traces from the
API and worker at `localhost:4317`. Open Grafana at
http://127.0.0.1:3300, then use the Prometheus data source for
`agent_trail_*` metrics and the Tempo data source for traces. Override the
host ports with `GRAFANA_PORT`, `OTLP_GRPC_PORT`, and `OTLP_HTTP_PORT`; keep
`OTEL_EXPORTER_OTLP_ENDPOINT` synchronized with `OTLP_GRPC_PORT`.

Compose mounts the `otel-lgtm-data` named volume at `/data`, where Grafana,
Prometheus, Tempo, and Loki keep their state, so `docker compose down`
preserves it across container replacement. Run `make clean` to remove both the
local PostgreSQL and LGTM volumes. The applications write structured logs to
stdout and do not export logs through OTLP.

Run `make telemetry-smoke` to start an isolated Compose project on free ports
(exported port variables take precedence), run one task through the fake
provider to `awaiting_review`, query one `agent_trail_*` metric and one worker
trace through Grafana, replace the LGTM container, and query both signals
again. The script writes the exact PromQL and TraceQL queries, service names,
task result, API and worker logs, LGTM logs, and query responses under
`artifacts/`. It appends a unique suffix to `COMPOSE_PROJECT_NAME` and removes
that isolated project's PostgreSQL and LGTM volumes after the persistence
check. It requires Docker, `curl`, and `openssl`.

The worker also batches completed task-scoped spans into PostgreSQL for the
dashboard. `GET /api/v1/tasks/{id}/trace` returns a `spans` array ordered by
start time, trace ID, and span ID. Each span includes its trace and parent
identity, optional attempt ID, name, kind, start and end timestamps,
attributes, and status. The dashboard polls this eventually consistent read
model after a run ends and constructs the waterfall in the browser.

Production sends the same OTLP metrics and traces to CloudWatch without changing application instrumentation. Each Fargate control-plane task runs a pinned ARM64 CloudWatch agent sidecar and exports to `localhost:4317`. The EKS runner controller and its Jobs export to `cloudwatch-agent-collector.amazon-cloudwatch.svc.cluster.local:4317`; the runner NetworkPolicy permits TCP 4317 only to CloudWatch agent pods in the `amazon-cloudwatch` namespace. The kind verifier renders the endpoint as `off`, so isolated verification does not contact AWS.

The Fargate task role grants `cloudwatch:PutMetricData` and `xray:PutTraceSegments`. ECS shares one task role across every container in a task, so the API container can technically use those actions even though the sidecar sends the telemetry. In EKS, only the `amazon-cloudwatch/cloudwatch-agent` ServiceAccount can assume the dedicated collector role. The pinned `amazon-cloudwatch-observability` add-on keeps Enhanced Container Insights, Application Signals, and container logging enabled while adding the OTLP gRPC and HTTP receivers.

Terraform enables account- and Region-wide Transaction Search from the dev root, routes X-Ray segments to CloudWatch Logs, indexes 1 percent of spans, and sets 30-day retention on `aws/spans` and `/aws/application-signals/data`. Dev and prod must use the same AWS account and Region, and dev must be applied first because prod shares that account-level configuration. Metrics retain their `agent_trail_*` names and bounded labels for PromQL in [CloudWatch Query Studio](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-PromQL-QueryStudio.html); traces retain their OpenTelemetry resource and span attributes for [Transaction Search](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/Enable-TransactionSearch.html).

CloudWatch charges OTLP metrics by ingested volume and stores them for up to 15 months; span costs include ingestion, CloudWatch Logs storage, and the configured Transaction Search indexing percentage. Container Insights, Application Signals, and container log ingestion add their own usage. Review the [CloudWatch pricing model](https://aws.amazon.com/cloudwatch/pricing/) before applying either environment. The application does not emit OTLP logs: ECS still sends structured stdout through `awslogs`, while the EKS add-on owns its container log path. The existing ALB, RDS, SQS queue-age, and dead-letter alarms remain because they measure infrastructure signals outside application telemetry.

The task header derives its cost total and per-attempt breakdown from `agent.cost_update` events delivered over SSE. A `total_cost_usd` value replaces the current attempt total, a `cost_usd` value increments it, and the header sums attempt totals. Tasks without a valid cost event show `not reported`.

Span attributes and error descriptions persist as instrumentation records them and remain with the task audit record. Do not attach credentials, source content, or other secrets to span attributes or status messages.

## Kubernetes backend

Set `RUNNER_TYPE=kubernetes` and provide a version-pinned `RUNNER_IMAGE` to run the worker as an in-cluster controller. Apply `deploy/k8s/runner/namespace.yaml` and `serviceaccount.yaml`, render `networkpolicy.yaml` with the Kubernetes API Service ClusterIP as a `/32`, create the Secrets below, and then render `controller.yaml`. The controller ServiceAccount can manage Jobs only. The production controller manifest sets the CloudWatch agent Service endpoint directly, and every controller-created Job inherits it.

- `runner-database`: `url`
- `runner-github`: `webhook-secret`, `app-id`, and `key.pem`
- `runner-agent`: optional for the fake provider; Claude Code requires `anthropic-api-key` or `claude-code-oauth-token`

The repository runner image supports the fake provider used by `scripts/verify-k8s-runner.sh`. For `AGENT_PROVIDER=claude-code`, build the pinned Claude CLI into the runner image and set `AGENT_CLI_VERSION`. The kind verifier creates the controller, checks its RBAC, runs one task to `awaiting_review`, and confirms TTL cleanup.

## Production layout

Terraform (`deploy/terraform/envs/dev` and `envs/prod`) runs the API and the dashboard as two Fargate services in one ECS cluster behind one Application Load Balancer, so the API's `SameSite=Lax` session cookie works without cross-origin handling; the worker runs on the EKS runner cluster from the same root. Listener rules send `/webhooks/*`, `/auth/*`, `/api/*`, `/healthz`, `/readyz`, and `/me` to the `control-plane` service, answer `/metrics` with 404, and send every other path to the `dashboard` service. The dashboard forwards `/backend/*` to `API_PROXY_TARGET`, read per request, which the task definition sets to `http://control-plane:8080`, the API's ECS Service Connect name, so that traffic never leaves the VPC. Each environment root requires `control_plane_image` and `dashboard_image` (immutable tag or digest) from the `control-plane` and `web` ECR repositories; the `runner` repository holds the worker image. `docker build -f deploy/docker/Dockerfile --target web .` builds the dashboard image; `scripts/verify-web-image.sh` builds it and checks that it serves `/healthz`, forwards `/backend/*` to the run-time `API_PROXY_TARGET`, and runs as uid 65532 on a read-only root filesystem.

## Layout

- `apps/api/` - Go control plane: `api` (HTTP), `worker` (process runner or Kubernetes controller), `migrate` (goose)
- `apps/web/` - Next.js dashboard
- `docker-compose.yml` - local PostgreSQL and Grafana LGTM infrastructure
- `deploy/docker/` - one Dockerfile with the `control-plane`, `runner`, `tools`, and `web` targets
- `deploy/k8s/` - `runner/` controller and Job manifests, `local/` kind verification manifests
- `deploy/terraform/` - AWS foundations: `modules/` and one root per environment under `envs/`
- `scripts/` - `gate.sh` (the CI gate), `dev.sh` (app runner), `verify-local-telemetry.sh` (Grafana LGTM smoke), `verify-production-telemetry.sh` (CloudWatch agent config check), `verify-web-image.sh` (dashboard image check), `verify-k8s-runner.sh` (kind verifier)
- `docs/` - benchmark plans and measured results

## Documentation

- [docs/testing/benchmarks.md](docs/testing/benchmarks.md) - benchmark and failure-injection plan
- [docs/testing/benchmark-results.md](docs/testing/benchmark-results.md) - measured results

## Tools Used

<table>
  <tr>
    <td><strong>Backend</strong></td>
    <td><img alt="Go" src="https://img.shields.io/badge/Go-%2300ADD8?style=for-the-badge&logo=go&logoColor=%23FFFFFF"> <img alt="GitHub App" src="https://img.shields.io/badge/GitHub%20Apps-%23181717?style=for-the-badge&logo=github&logoColor=%23FFFFFF"> <img alt="Claude Code" src="https://img.shields.io/badge/Claude%20Code%20CLI-%23D97757?style=for-the-badge&logo=claude&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Frontend</strong></td>
    <td><img alt="TypeScript" src="https://img.shields.io/badge/TypeScript-%233178C6?style=for-the-badge&logo=typescript&logoColor=%23FFFFFF"> <img alt="Next.js" src="https://img.shields.io/badge/Next.js-black?style=for-the-badge&logo=nextdotjs&logoColor=%23FFFFFF"> <img alt="Tailwind CSS" src="https://img.shields.io/badge/Tailwind%20CSS-%2306B6D4?style=for-the-badge&logo=tailwindcss&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Data</strong></td>
    <td><img alt="PostgreSQL (Amazon RDS)" src="https://img.shields.io/badge/PostgreSQL%20%28RDS%29-%234169E1?style=for-the-badge&logo=postgresql&logoColor=%23FFFFFF">  </td>
  </tr>
    <tr>
    <td><strong>Orchestration</strong></td>
    <td><img alt="Kubernetes" src="https://img.shields.io/badge/Kubernetes%20%28EKS%29-%23326CE5?style=for-the-badge&logo=kubernetes&logoColor=%23FFFFFF"> <img src="https://img.shields.io/badge/AWS%20ECS-%23FF9900?style=for-the-badge&logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSI4MDAiIGhlaWdodD0iODAwIiBmaWxsPSIjZmZmIiB2aWV3Qm94PSIwIDAgMzIgMzIiPjxwYXRoIGQ9Ik02LjU4NCA5LjAxYy0xLjM2IDAtMi43NC41My0yLjk3LjgyLS4wNi4xMi0uMiAxLjA5LjEzIDEuMDkuMTEgMCAuMTYuMDIuNDgtLjEzIDEuMi0uNDcgMS45Ni0uNDYgMi4wNy0uNDYgMS4zNS0uMTMgMi4xMy43OSAyLjAxIDEuOTh2LjdjLTEuMTQtLjI3LTEuNzktLjI4LTIuMTEtLjI4LTEuNjYtLjEtMy4xOTQuNzc2LTMuMTk0IDIuNyAwIDIuMTEgMS44ODMgMi41NiAyLjYxMyAyLjUzIDEuMDkuMDEgMi4xMy0uNDggMi44Mi0xLjMzLjU1IDEuMjMuOSAxLjE1LjkxIDEuMTUuMSAwIC4xOC0uMDQuMjYtLjA5bC41Ny0uNGMuMS0uMDYuMTgtLjE2LjE5LS4yOC0uMDEtLjI5LS41My0uNzQtLjQ5LTEuNzV2LTMuMTJhMy4xOCAzLjE4IDAgMCAwLS43OTktMi4zNSAzLjQyIDMuNDIgMCAwIDAtMi40OS0uNzhtMTkuMzczIDBjLTIgMC0zLjE1IDEuMjUtMy4xMiAyLjUyIDAgMS43NCAxLjc2IDIuMjkgMS45NiAyLjM1IDEuNjkuNTMgMS45Mi41NSAyLjM5Ljk1LjQuNDEuMzUgMS4yMS0uMjQgMS41Ni0uMTcuMS0uOS41NC0yLjU1LjItLjU1LS4xMS0uODQtLjI0LTEuMjktLjQzLS4xMi0uMDQtLjQtLjExLS40LjI2di40OWMwIC4yMy4xNC40NC4zNS41NCAxLjA1LjUzIDIuMzEuNTUgMi41OC41NS4wNCAwIDIuMzQuMDAxIDMuMTEtMS41NS4xNTgtLjMyLjU3LTEuNDktLjItMi40OS0uNjQtLjc1LTEuMTktLjgzLTIuODMtMS4zMy0uMTQtLjA0LTEuMzUtLjM1LTEuMzQtMS4yLS4wNi0xLjA5IDEuNDItMS4xNSAxLjczLTEuMTMgMS4yNS0uMDIgMS44Ny40NSAyLjIxLjQ4LjE1IDAgLjIyLS4wOS4yMi0uMjl2LS40NmEuNS41IDAgMCAwLS4wOS0uMzFjLS40LS41Mi0xLjkzLS43MS0yLjQ5LS43MW0tMTUuMTguMjVjLS4xMS4wMi0uMTkuMTMtLjE3LjI0LjAyLjEzLjA0LjI2LjA5LjM5bDIuMjQgNy4zOWMuMDUuMjQuMjEuNS41Ni40NmguODJjLjUuMDUuNTctLjQzLjU4LS40OGwxLjQ3LTYuMTYgMS40OSA2LjE3Yy4wMS4wNS4wOC41My41Ny40OGguODNjLjM2LjA0LjUzLS4yMi41OC0uNDYgMi41Mi04LjExIDIuMzUtNy41NiAyLjM3LTcuNjQuMDQtLjQyLS4yLS4zOS0uMjQtLjM4aC0uODljLS40NS0uMDUtLjU0LjM2LS41Ni40NmwtMS42NiA2LjQxLTEuNS02LjQxYy0uMDctLjQ5LS40Ny0uNDctLjU3LS40NmgtLjc3Yy0uNDQtLjA0LS41NS4zMS0uNTguNDZsLTEuNDkgNi4zMi0xLjYtNi4zMmMtLjA0LS4yLS4xNy0uNTEtLjU2LS40N3ptLTQuMjU0IDQuNjNjLjcyLjAxIDEuMzQyLjEyIDEuNzcyLjIyIDAgLjUuMDE4Ljc4LS4wOTIgMS4yMy0uMTQuNDgtLjc1OSAxLjM1LTIuMjE5IDEuMzctLjg0LjA0LTEuMzktLjYyLTEuMzQtMS4zNy0uMDUtMS4yIDEuMTktMS41IDEuODgtMS40NW0yMi41MTggNi4xMTJjLS45MzMuMDEzLTIuMDM1LjIyMi0yLjg3MS44MDktLjI1OC4xNzktLjIxMy40MjcuMDc0LjM5NC45NC0uMTEzIDMuMDMyLS4zNjcgMy40MDYuMTExcy0uNDE0IDIuNDUtLjc2MyAzLjMzMmMtLjEwOC4yNjMuMTIuMzcyLjM2MS4xNzIgMS41NjQtMS4zMSAxLjk3LTQuMDU2IDEuNjUtNC40NS0uMTYtLjE5OC0uOTI0LS4zODEtMS44NTctLjM2OG0tMjcuODI0IDFjLS4yMTguMDMtLjMxMi4zMDYtLjA4NC41MjVDNS4wNSAyNS4yMDEgMTAuMjI2IDI3IDE1Ljk3MyAyN2M0LjA5OSAwIDguODU3LTEuMzM3IDEyLjE0Mi0zLjg1Ny41NDMtLjQyLjA4LTEuMDQ3LS40NzYtLjgtMy42ODMgMS42MjYtNy42ODQgMi40MDktMTEuMzI1IDIuNDA5LTUuMzk2IDAtMTAuNjItMS4xMjctMTQuODQ1LTMuNjg2YS40LjQgMCAwIDAtLjI1Mi0uMDY0Ii8+PC9zdmc+&logoColor=white" alt="AWS" /> <img alt="Docker" src="https://img.shields.io/badge/Docker-%232496ED?style=for-the-badge&logo=docker&logoColor=%23FFFFFF"> </td>
  </tr>
  <tr>
    <td><strong>Platform</strong></td>
    <td> <img alt="Terraform" src="https://img.shields.io/badge/Terraform-%23844FBA?style=for-the-badge&logo=terraform&logoColor=%23FFFFFF"> <img alt="GitHub Actions" src="https://img.shields.io/badge/GitHub%20Actions-%232088FF?style=for-the-badge&logo=githubactions&logoColor=%23FFFFFF"> <img alt="OpenTelemetry" src="https://img.shields.io/badge/OpenTelemetry-%23425CC7?style=for-the-badge&logo=opentelemetry&logoColor=%23FFFFFF"></td>
  </tr>
</table>
