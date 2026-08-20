# Local environment

The local environment does not use Terraform: there is no cloud
infrastructure to manage. It is composed of

- `docker-compose.yml` at the repo root for Postgres and the OpenTelemetry
  collector, and
- a kind cluster for verifying the Kubernetes runner Job locally (see
  `deploy/k8s/` and `scripts/verify-k8s-runner.sh`).

The dev and prod roots next to this directory are the AWS environments.
They are authored and validated (`terraform fmt`, `terraform validate`)
but deliberately never applied: standing up AWS infrastructure is a
deliberate, human-gated action with real cost. Nothing in CI initialises
a backend or touches an AWS account.
