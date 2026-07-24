# Observability

### Metrics

Control plane:

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
agent_trail_runner_heartbeats_missed_total
```

Runner:

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

Product metrics:

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

Trace:

- Webhook to task creation
- Queue wait
- Runner provisioning
- GitHub token exchange
- Git fetch
- Agent session
- Validation
- Push
- Pull-request creation

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

Create:

1. System health
2. Queue and runner capacity
3. Task outcomes
4. GitHub API health
5. Validation outcomes
6. Policy denials
7. Resource usage

### Alerts

Alert on:

- High webhook error rate
- Long queue wait
- No healthy runners
- Missed heartbeats
- Elevated task failure rate
- Database saturation
- Object storage errors
- Cleanup backlog
- Unusual policy denials
