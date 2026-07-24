# ADR-0004: Docker runner before Kubernetes Jobs

- Status: accepted
- Date: 2026-07-24

## Context

Each task needs an isolated workspace with scoped credentials: an agent
must not see another task's repository, tokens, or logs. The isolation
mechanism also has to exist early, because the fake-runner milestone needs
somewhere to run. Candidates: Docker containers on a dedicated runner host,
or a Kubernetes Job per task.

## Decision

The initial runner is a Docker container per task on a dedicated runner
host. Kubernetes Jobs land with the cloud deployment milestone.

## Alternatives

- Kubernetes Job per task from day one: stronger scheduling, quotas, and
  network policy, but it puts a cluster between the MVP and its first
  end-to-end task. The required vertical slice in VISION.md must not wait
  on cluster infrastructure.
- Plain OS processes with no container: no meaningful isolation boundary;
  rejected outright.

## Consequences

- Local development and the first milestones run with nothing but Docker.
- The runner controller gets a provider-shaped seam (start, watch, kill,
  clean) so the Kubernetes implementation replaces Docker without touching
  task logic.
- Scale-out is manual (add runner hosts) until Kubernetes arrives.

## Security implications

- Containers share the host kernel: this is weaker isolation than VMs, and
  docs/security/risks.md must say so rather than claim complete isolation.
- The runner host holds no production credentials; each container receives
  only short-lived, task-scoped tokens.
- Container-to-container and container-to-metadata network access must be
  restricted; the concrete policy is tracked in the threat model.

## Revisit conditions

- The cloud deployment milestone (Kubernetes Job is already the plan).
- A tenant-isolation requirement that exceeds what containers provide.
