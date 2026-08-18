# ADR-0014: Kubernetes Job runner and authored-only cloud infrastructure

- Status: accepted
- Date: 2026-08-18

## Context

The cloud deployment milestone (issue #12) asks for Terraform across AWS
and for tasks to run inside restricted Kubernetes Jobs. The project owner
directed that nothing may be applied to a cloud account - the platform is
exercised locally only, and cloud spend is a human-gated decision that has
not been taken. ADR-0004 planned a Docker-container-per-task runner as the
first isolation step; that step never landed, so the runner still executes
attempts in-process inside `cmd/worker`. Separately, the docs offered a
choice of ECS Fargate or EKS for the control plane.

## Decision

Three linked choices:

1. **Cloud infrastructure is authored and validated, never applied.**
   `deploy/terraform` holds the eleven modules and the dev/prod
   environment roots; the CI gate runs `terraform fmt` and
   `terraform validate` offline (`-backend=false`, no credentials), and
   no pipeline initialises a backend or touches an account.
2. **The control plane targets ECS Fargate; runners target EKS.** One
   Kubernetes cluster exists for the workload that needs Kubernetes
   semantics (Jobs, Pod Security admission, NetworkPolicy); the stateless
   API service does not need a cluster.
3. **Runner isolation reuses the existing worker as a one-shot Kubernetes
   Job** (`RUNNER_TYPE=kubernetes`, `WORKER_MAX_TASKS=1`): the Job's pod
   claims one attempt through PostgreSQL (ADR-0003), executes and
   publishes it, and exits, so `ttlSecondsAfterFinished` reclaims it. The
   hardened spec lives in `deploy/k8s/runner/job.yaml` and is proven
   locally in kind by `scripts/verify-k8s-runner.sh` against the fixture
   in `cmd/fixture-github`. Kubernetes objects are plain manifests under
   `deploy/k8s` - a Helm chart adds templating this handful of resources
   does not need. ADR-0004's intermediate Docker-runner step is dropped;
   this ADR supersedes it.

## Alternatives

- **Applying Terraform to a real account** proves the IAM separation live
  but costs money and needs credentials only the owner can grant; it lost
  to the owner's explicit no-spend constraint.
- **EKS for the control plane too** centralises everything on one
  orchestrator but adds cluster upgrades, node management, and IRSA
  plumbing to a service that a task definition serves fine.
- **A push-model runner controller** (control plane creates a Job per
  attempt via the Kubernetes API) matches the eventual cloud dispatch
  path, but it needs SQS and cluster credentials that do not exist
  locally, and the pull model needed no new claim semantics - the lease
  arbiter already guarantees single ownership. The controller lands with
  a live cluster.
- **Docker-container-per-task first (ADR-0004)** was overtaken: the
  Kubernetes Job gives the same isolation with an admission-enforced
  profile, and building the Docker intermediary now would be throwaway.

## Consequences

- The full issue-to-PR pipeline runs inside a restricted pod today, and
  the acceptance criteria (restricted Job, TTL cleanup) are demonstrable
  on any machine with docker and kind - no cloud dependency.
- The worker binary stays one artifact; process and kubernetes modes
  cannot drift apart.
- Job creation and scaling on EKS (the runner-controller), the internal
  runner HTTP API, and live IAM verification remain open work; the
  Terraform expresses the IAM separation but no live account has
  exercised it.
- The runner pod still reaches PostgreSQL directly; the network policy
  must therefore allow database egress from the runner namespace until
  the runner API lands.

## Security implications

- Pod Security admission (`restricted`) on the runner namespace rejects
  any Job that drops the hardening; the verification script additionally
  asserts the live pod spec (non-root, read-only rootfs, no capabilities,
  no service-account token, no docker socket).
- Default-deny NetworkPolicy bounds runner egress, but enforcement is the
  CNI's; the kind run proves allowed paths work, not that every
  disallowed path is blocked.
- Anonymous git push and throwaway credentials exist only in the fixture
  (`cmd/fixture-github`), which is built for the verification stack and
  never deployed.
- Direct database access from runner pods is a wider blast radius than
  the planned runner API; the lease arbiter and row-level claim scoping
  are the current mitigations.
- The pull-model worker mints installation tokens itself, so the runner
  pod currently carries the GitHub App private key (throwaway in the
  local verification), violating the aws-deployment.md target that
  runners never hold it. The Terraform runner IAM roles already exclude
  the key; closing the gap is the runner internal API's job and blocks
  the live cloud dispatch path.

## Revisit conditions

- The owner approves cloud spend: apply dev, verify IAM separation and
  IRSA live, and build the runner-controller dispatch path.
- The runner internal API lands: drop database egress from the runner
  namespace and revisit the network policy.
- Task volume outgrows one-Job-per-attempt scheduling latency.
