# Core Data Model

### Organization

```text
id
name
slug
github_account_id
github_account_login
github_account_type
created_at
updated_at
```

### User

```text
id
github_user_id
github_login
display_name
avatar_url
created_at
last_login_at
```

### Membership

```text
organization_id
user_id
role
created_at
```

Roles:

- owner
- admin
- member
- viewer

### GitHub installation

```text
id
organization_id
github_installation_id
account_login
account_type
permissions_json
events_json
suspended_at
created_at
updated_at
```

### Repository

```text
id
organization_id
github_repository_id
owner
name
full_name
default_branch
clone_url
is_private
is_enabled
settings_json
created_at
updated_at
```

### Task

```text
id
organization_id
repository_id
source_type
source_issue_number
source_comment_id
title
instructions
status
phase
priority
base_branch
base_commit_sha
working_branch
agent_provider
agent_model
policy_id
requested_by_user_id
max_runtime_seconds
max_cost_usd
started_at
completed_at
cancel_requested_at
failure_code
failure_message
created_at
updated_at
version
```

### Task attempt

```text
id
task_id
attempt_number
runner_id
status
base_commit_sha
final_commit_sha
pull_request_number
started_at
completed_at
failure_code
failure_message
created_at
```

### Runner

```text
id
runner_type
hostname_or_pod
status
capacity
labels_json
last_heartbeat_at
created_at
updated_at
```

### Workspace

```text
id
task_attempt_id
repository_path
worktree_path
container_id_or_pod
branch
retention_until
cleaned_at
created_at
```

### Activity event

Append-only task timeline:

```text
id
task_attempt_id
sequence_number
event_type
source
timestamp
payload_json
redaction_status
created_at
```

Example event types:

```text
task.queued
workspace.provisioning
workspace.ready
agent.started
agent.message
plan.created
command.requested
command.started
command.output
command.completed
file.read
file.changed
validation.started
validation.completed
commit.created
branch.pushed
pull_request.created
task.cancelled
task.failed
task.completed
cleanup.completed
```

### Command execution

```text
id
task_attempt_id
sequence_number
command
arguments_json
working_directory
requested_by
policy_decision
exit_code
started_at
completed_at
stdout_object_key
stderr_object_key
output_truncated
created_at
```

### Validation result

```text
id
task_attempt_id
name
category
command_json
status
exit_code
duration_ms
summary
report_object_key
trusted_execution
created_at
```

Categories:

```text
unit_test
integration_test
lint
format
typecheck
security
dependency
migration
build
custom
```

### Policy

```text
id
organization_id
repository_id
name
version
policy_json
is_default
created_by
created_at
```

### Evidence report

```text
id
task_attempt_id
schema_version
summary_markdown
report_json
report_object_key
created_at
```
