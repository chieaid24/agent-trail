# Command Execution and Policy

## Command Execution

All agent shell commands should pass through a runner-owned wrapper where technically possible.

The wrapper must:

- Accept command and argument arrays
- Resolve the working directory
- Check path boundaries
- Evaluate policy
- Emit start event
- Execute the process
- Stream stdout and stderr
- Apply timeout
- Capture exit code
- Redact secrets
- Emit completion event

Policy decisions:

- allow
- deny
- require_approval
- allow_with_redaction


## Example Policy

```yaml
version: 1

filesystem:
  workspace_only: true
  writable_paths:
    - .
    - /tmp/agent-trail
  denied_paths:
    - /var/run/docker.sock
    - /root/.ssh
    - /home/runner/.aws
    - /var/run/secrets/kubernetes.io

git:
  allow_push: true
  allowed_remote: origin
  allowed_branch_prefixes:
    - agent-trail/
  allow_force_push: false
  allow_protected_branch_push: false

network:
  default: deny
  allow_domains:
    - registry.npmjs.org
    - pypi.org
    - files.pythonhosted.org
    - proxy.golang.org
    - sum.golang.org

commands:
  deny:
    - pattern: "sudo *"
    - pattern: "mount *"
    - pattern: "docker run --privileged *"
  require_approval:
    - pattern: "terraform apply *"
    - pattern: "kubectl apply *"

limits:
  runtime_seconds: 2700
  command_timeout_seconds: 900
  output_bytes_per_command: 10485760
```

Start with code-based policies if a declarative engine becomes too large.
