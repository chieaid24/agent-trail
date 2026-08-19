# Observability

The API and worker use the OpenTelemetry SDK for metrics and traces. In local
development they export OTLP/gRPC to the Compose collector, whose Prometheus
exporter feeds the provisioned Grafana dashboards and alerts. The API also
keeps its unauthenticated `GET /metrics` Prometheus endpoint. Cloud-side alarms
live in `deploy/terraform/modules/observability`.

`OTEL_EXPORTER_OTLP_ENDPOINT` is a plaintext `host:port` target. It defaults to
`localhost:4317` for native processes alongside the Compose collector; empty or
`off` disables OTLP while leaving API `/metrics` available. Export failures are
logged and do not stop task execution. Process shutdown gets five seconds to
flush telemetry. See [ADR-0015](../adr/0015-opentelemetry-runtime.md).

### Metrics

Control plane counters:

```text
agent_trail_webhook_received_total
agent_trail_webhook_invalid_signature_total
agent_trail_task_created_total
agent_trail_task_status_total
agent_trail_task_queue_wait_seconds
agent_trail_task_duration_seconds
agent_trail_task_failures_total
agent_trail_github_api_requests_total
agent_trail_github_api_errors_total
agent_trail_auth_github_requests_total
agent_trail_auth_github_errors_total
agent_trail_runner_heartbeats_missed_total
```

Worker metrics:

```text
agent_trail_runner_active_tasks
agent_trail_runner_cpu_usage
agent_trail_runner_memory_usage
agent_trail_runner_disk_usage
agent_trail_command_duration_seconds
agent_trail_command_exit_total
agent_trail_validation_duration_seconds
agent_trail_policy_denials_total
agent_trail_log_bytes_total
agent_trail_workspace_cleanup_total
```

Metric contract:

| Metric | Type and unit | Labels |
| --- | --- | --- |
| `agent_trail_task_queue_wait_seconds` | Histogram, seconds | None |
| `agent_trail_task_duration_seconds` | Histogram, seconds | None |
| `agent_trail_task_status_total` | Counter | `status` (task status enum) |
| `agent_trail_task_failures_total` | Counter | `code` (runner failure code) |
| `agent_trail_runner_active_tasks` | Up/down counter | None |
| `agent_trail_runner_cpu_usage` | Gauge, fraction of one CPU core | None |
| `agent_trail_runner_memory_usage` | Gauge, resident bytes | None |
| `agent_trail_runner_disk_usage` | Gauge, used filesystem fraction | None |
| `agent_trail_command_duration_seconds` | Histogram, seconds | None |
| `agent_trail_command_exit_total` | Counter | `status` (validation status enum) |
| `agent_trail_validation_duration_seconds` | Histogram, seconds | None |
| `agent_trail_policy_denials_total` | Counter | `policy` (`forbidden_remote`, `forbidden_branch`, `force_push`) |
| `agent_trail_log_bytes_total` | Counter, bytes | None |
| `agent_trail_workspace_cleanup_total` | Counter | `outcome` (`removed`, `failed`) |

The remaining control-plane counters have no labels. Resource gauges are
available on Linux. Disk usage samples the filesystem holding
`WORKSPACE_ROOT`, falling back to the system temporary directory when that path
does not yet exist. The first CPU collection establishes a baseline and reports
zero.

Product metrics remain planned and are not emitted by the runtime:

```text
task_completion_rate
pull_request_creation_rate
validation_pass_rate
median_task_runtime
revision_count
agent_cost_per_completed_task
tasks_completed_without_intervention
```

### Tracing

The runtime emits spans for:

- Webhook to task creation
- Queue wait
- Runner provisioning
- GitHub token exchange
- Git fetch
- Agent session
- Validation
- Push
- Pull-request creation

The runner also emits a parent attempt span. The queue-wait span is backdated
from task creation to the first claim. Webhook processing reuses the request
correlation ID as its trace ID, so structured logs and spans join directly.
Local traces go to the collector's debug exporter; local development does not
persist them in a trace backend.

### Structured logs

Required fields:

```text
timestamp
service
level
trace_id
task_id
task_attempt_id
runner_id
event
message
```

Scoping: `trace_id` is required on request-scoped lines (process lifecycle
lines carry none); `task_id`, `task_attempt_id`, and `runner_id` are required
once the task domain and runners exist, on lines with that context.

Do not log:

- Tokens
- Private keys
- Authorization headers
- Full private prompts
- Full private repository contents

### Dashboards

Compose provisions these dashboards under the `Agent Trail` folder:

1. System health
2. Queue and runner capacity
3. Task outcomes
4. GitHub API health
5. Validation outcomes
6. Policy denials
7. Resource usage

### Alerts

Compose provisions these nine Grafana-managed alert rules:

- High webhook error rate
- Long queue wait
- No healthy runners
- Missed heartbeats
- Elevated task failure rate
- Database saturation
- Object storage errors
- Cleanup backlog
- Unusual policy denials

Thresholds are development defaults, not measured service-level objectives.
Tune them against observed workloads before production use.
