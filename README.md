# Agent Trail

A more secure way to run coding agents.

Comment `/agent-trail run` on a GitHub issue and it creates a task, runs an agent in an isolated container, streams every action to a dashboard, validates the result, and opens a draft pull request with evidence report. Review the pull request as usual, then comment `/agent-trail revise` and the agent continues the same branch with your feedback; merging the pull request completes the task.


<p align="center">
<img width="800" alt="Agent Trail System Design" src="https://github.com/user-attachments/assets/ce203d5b-0ed2-4a93-83de-e0a1e187cc7b" />
</p>

## Technical Highlights
**Infrastructure**
- AWS architecture is **100% Infrastructure as Code** with Terraform.
- Isolated agent execution on **Kubernetes Jobs**, with a nonroot + default-deny NetworkPolicy container per task attempt.

**Data Layer**
- Idempotent **GitHub webhook ingestion** with delivery-id dedup and an append-only sequenced event log.

**Conflict Detection**
- **LLM-based semantic conflict detection** across overlapping agent branches.

**Observability**
- End-to-end agent telemetry via **OpenTelemetry** and **Server-Sent Events**, streaming live execution, cost, runtime, and results to the dashboard.

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


## Quickstart

Requires Go 1.26+, Node 24+, and Docker.

```bash
make dev      # compose infra + migrations + api, worker, web
make test     # unit tests for both apps
```

`make dev` serves the API on :8080, the dashboard on :3000, and Grafana on
http://127.0.0.1:3300.


## Production Deployment

Terraform in `deploy/terraform/envs/prod` provisions everything on AWS: VPC,
RDS, ECR, the Fargate API and dashboard, the EKS runner cluster, and
CloudWatch. You need an AWS account, a Route 53 hosted zone, and an S3 bucket
for Terraform state.

1. Build and push the `control-plane`, `web`, and `runner` images from
   `deploy/docker/Dockerfile` to ECR (ARM64).
2. Apply Terraform with your domain and image tags, then fill in the Secrets
   Manager entries it creates (GitHub App key, webhook secret, agent API key,
   dashboard auth secret).

   ```bash
   terraform -chdir=deploy/terraform/envs/prod init -backend-config="bucket=<state-bucket>"
   terraform -chdir=deploy/terraform/envs/prod apply -var-file=prod.tfvars
   ```

3. Run `migrate up` from the `tools` image against RDS, then apply
   `deploy/k8s/runner/` to the EKS cluster with `RUNNER_IMAGE` set to the
   runner image.
4. Register a GitHub App pointing at `https://<domain>/webhooks/github`,
   subscribed to the `issue_comment` and `pull_request` events, install it
   on a repository, and comment `/agent-trail run` on an issue. Once the
   draft pull request is open, leave review feedback and comment
   `/agent-trail revise` on it to start a revision. Each repository allows
   `max_attempts` attempts per task (default 5), set through the repository
   settings API and shown on the repository page.


## Layout

- `apps/api/` - Go control plane: `api` (HTTP), `worker` (process runner or Kubernetes controller), `migrate` (goose)
- `apps/web/` - Next.js dashboard
- `docker-compose.yml` - local PostgreSQL and Grafana LGTM infrastructure
- `deploy/docker/` - one Dockerfile with the `control-plane`, `runner`, `tools`, and `web` targets
- `deploy/k8s/` - `runner/` controller and Job manifests, `local/` kind verification manifests
- `deploy/terraform/` - AWS foundations: `modules/` and one root per environment under `envs/`
- `scripts/` - `gate.sh` (the CI gate), `dev.sh` (app runner), `verify-local-telemetry.sh` (Grafana LGTM smoke), `verify-production-telemetry.sh` (CloudWatch agent config check), `verify-web-image.sh` (dashboard image check), `verify-k8s-runner.sh` (kind verifier)
- `docs/` - benchmark plans and measured results

