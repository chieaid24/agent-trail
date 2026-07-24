# Threat Model, Security, and Privacy

## Sandbox Security Model

### Threat model

Assume:

- Repository code may be unsafe.
- Package installation scripts may execute code.
- Agents may generate unsafe commands.
- Repository files may contain prompt injection.
- Logs may expose sensitive data.
- The MVP runs only trusted repositories selected by the operator.

### Container requirements

Use:

- Non-root user
- Dropped Linux capabilities
- `no-new-privileges`
- Seccomp runtime default
- Read-only root filesystem where practical
- CPU limit
- Memory limit
- Process limit
- Disk limit
- Timeout
- No host network
- No Docker socket
- No privileged mode
- No host PID namespace
- No hostPath mounts
- Restricted Kubernetes security policy

### Network controls

Block by default:

- Cloud metadata services
- Private address ranges
- Kubernetes control plane
- Internal databases
- Arbitrary external hosts

Allow only:

- Approved package registries
- GitHub
- Required agent-provider endpoints

### Secrets

- GitHub App private key remains in the control plane.
- Installation tokens are short-lived.
- Agent-provider credentials are mounted only for the agent process.
- Secret values are never stored in task events.
- Logs are redacted.
- Temporary credentials are revoked or discarded after completion.
- Record secret names, never values.

### Prompt injection

Mitigations:

- Treat repository content as untrusted data.
- Keep policy external to the model.
- Expose minimal credentials.
- Require approval for high-risk operations.
- Record denied actions.
- Run validation outside the agent session.
- Do not let repository files redefine platform policy.


## Security and Privacy

### Sensitive data

- Repository contents
- Issue bodies
- Review comments
- Agent messages
- Diffs
- Logs
- Organization membership

### Highly sensitive data

- GitHub App private key
- Installation tokens
- Agent-provider credentials
- Cloud credentials
- Repository secrets

### Retention defaults

Suggested:

- Timeline metadata: 90 days
- Command logs: 30 days
- Workspaces: 1 to 24 hours
- Raw provider events: 7 to 30 days
- Evidence: retained with task
- Secrets: never persisted in logs

### Authorization

Every resource request must verify:

- Organization membership
- Repository membership
- Required user role
- Repository belongs to the installed GitHub account

Do not rely on frontend checks.

### Audit log

Record:

- App installation
- Repository enablement
- Policy changes
- Task creation
- Cancellation
- Retry
- Approval
- User role changes
- Retention changes
