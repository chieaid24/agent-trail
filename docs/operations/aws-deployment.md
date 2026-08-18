# AWS Deployment

The AWS environments are **authored and validated, never applied**
(ADR-0014): `deploy/terraform` passes `terraform fmt` and
`terraform validate` in the CI gate with no backend and no credentials,
and standing up an account is a deliberate, human-gated action with real
cost. The runner-isolation acceptance criteria are verified locally in
kind instead (`scripts/verify-k8s-runner.sh`,
docs/operations/local-development.md). Consequently the IAM separation
below is expressed in Terraform but has not been exercised against a live
account.

### Control plane

ECS Fargate behind an Application Load Balancer
(`deploy/terraform/modules/control_plane`; ADR-0014 records why Fargate
over EKS). Supporting services:

- RDS PostgreSQL
- S3
- SQS
- Secrets Manager
- CloudWatch
- OpenTelemetry

### Runner execution

Use:

- EKS Kubernetes Jobs
- Dedicated namespace
- Restricted service account
- TTL cleanup
- Network policies
- Resource limits
- No service-account token unless required

### IAM roles

Separate:

- Control plane role
- Runner controller role
- Runner task role
- CI deployment role

The runner must not have:

- Database administrator credentials
- GitHub App private key
- Broad cloud permissions

### Terraform modules

Under `deploy/terraform/modules`:

```text
network
database
object_storage
queue
secrets
container_registry
control_plane
runner_cluster
observability
dns_tls
github_oidc_ci
```

Environments (`deploy/terraform/envs`):

- local - no Terraform: docker compose plus a kind cluster
  (`deploy/terraform/envs/local/README.md`)
- dev - 2 AZs, small instances, no deletion protection
- prod - 3 AZs, Multi-AZ RDS, deletion protection

Both environments use a single NAT gateway by design (cost); private
egress is not AZ-redundant.

Provider versions are pinned by the committed `.terraform.lock.hcl` in
each environment root.


## Kubernetes Job

The canonical hardened Job spec is `deploy/k8s/runner/job.yaml`; the
namespace, service accounts, and default-deny network policies are next to
it under `deploy/k8s/runner/`. Hardening it carries, asserted on the live
pod by `scripts/verify-k8s-runner.sh`:

- `restartPolicy: Never`, `backoffLimit: 0`, `activeDeadlineSeconds`
- `ttlSecondsAfterFinished` removes the finished Job and its pod
- `automountServiceAccountToken: false` on the `runner-task` service
  account and forced off per-pod
- `runAsNonRoot` as uid 65532, `fsGroup` 65532 so the 0440 secret mount
  is readable, seccomp `RuntimeDefault`
- `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
  capabilities drop ALL
- cpu/memory requests and limits; `/workspace` and `/tmp` are size-limited
  emptyDir volumes
- the runner namespace enforces the `restricted` Pod Security profile, so
  a Job that drops any of this is rejected at admission

The worker runs one-shot inside the Job (`WORKER_MAX_TASKS=1`,
`RUNNER_TYPE=kubernetes`, `WORKER_IDLE_EXIT_SECONDS` as a hang guard) and
claims through PostgreSQL (ADR-0003); Job creation and scaling on EKS is
the runner-controller's job and has not landed. Adjust image contents
based on the chosen agent CLI and package manager.

Do not mount the Docker socket.

Known limitations:

- Network-policy egress enforcement depends on the cluster CNI. The kind
  verification proves the allowed paths work with the default-deny
  policies applied; it does not exhaustively prove every disallowed path
  is blocked.
- The pull-model worker currently needs the GitHub App key to mint
  installation tokens, so the local verification Job mounts a throwaway
  key against a fake GitHub API. That contradicts the "runner must not
  have the GitHub App private key" rule above, which stands as the
  target: the Terraform runner roles already exclude the key
  (`modules/secrets` `runner_secret_arns`), so the internal runner API
  (docs/architecture/api.md) - where the control plane holds the key and
  hands runners short-lived tokens - must land before the cloud dispatch
  path goes live.
