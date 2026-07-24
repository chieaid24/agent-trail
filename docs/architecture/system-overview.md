# System Overview

## High-Level Architecture

```text
                           +-------------------------+
                           |         GitHub          |
                           | Issues, PRs, Checks     |
                           +------------+------------+
                                        |
                                    Webhooks
                                        |
                                        v
+--------------------+     +-------------------------+     +------------------+
| Next.js Dashboard  |<--->| Agent Trail Control API |<--->| PostgreSQL       |
+---------+----------+     +------------+------------+     +------------------+
          ^                             |
          | SSE                         | enqueue
          |                             v
          |                +------------+------------+
          +----------------| Task Scheduler          |
                           +------------+------------+
                                        |
                                        v
                           +------------+------------+
                           | Runner Controller       |
                           +------------+------------+
                                        |
                               Docker or K8s Job
                                        |
                                        v
                        +---------------+----------------+
                        | Isolated Agent Runner          |
                        | Git worktree, agent, validation|
                        +---------------+----------------+
                                        |
                                Logs and artifacts
                                        |
                        +---------------+----------------+
                        | S3 or MinIO                    |
                        +--------------------------------+
```


## Recommended Technology Stack

### Backend control plane

- Go
- Chi or standard `net/http`
- PostgreSQL
- pgx or sqlc
- Goose or Atlas migrations
- OpenTelemetry
- Structured JSON logging

### Frontend

- Next.js
- TypeScript
- React
- TanStack Query
- Tailwind CSS
- Server-sent events

### Queue

Start with one of:

1. PostgreSQL task claiming using `FOR UPDATE SKIP LOCKED`
2. Redis Streams
3. AWS SQS

Recommended MVP: PostgreSQL-backed queue.

Recommended cloud version: AWS SQS.

### Runner

Initial:

- Docker container on a dedicated runner host

Later:

- Kubernetes Job per task

### Object storage

Local:

- MinIO

Cloud:

- Amazon S3

### Infrastructure

- Terraform
- AWS EKS or ECS
- RDS PostgreSQL
- S3
- SQS
- Secrets Manager
- CloudWatch
- OpenTelemetry Collector
- Prometheus
- Grafana

### Authentication

- GitHub OAuth
- GitHub App installation
- Secure HTTP-only session cookies

Avoid custom email/password authentication.


## Suggested Repository Structure

```text
agent-trail/
|-- README.md
|-- LICENSE
|-- Makefile
|-- docker-compose.yml
|-- .env.example
|-- .github/
|   `-- workflows/
|-- apps/
|   |-- api/
|   |   |-- cmd/
|   |   |   |-- api/
|   |   |   |-- worker/
|   |   |   `-- migrate/
|   |   |-- internal/
|   |   |   |-- auth/
|   |   |   |-- github/
|   |   |   |-- tasks/
|   |   |   |-- runners/
|   |   |   |-- policy/
|   |   |   |-- evidence/
|   |   |   |-- storage/
|   |   |   `-- observability/
|   |   `-- migrations/
|   `-- web/
|-- services/
|   `-- runner/
|       |-- cmd/
|       |-- internal/
|       |   |-- agent/
|       |   |-- git/
|       |   |-- sandbox/
|       |   |-- command/
|       |   |-- validation/
|       |   `-- cleanup/
|       `-- Dockerfile
|-- packages/
|   `-- contracts/
|-- deploy/
|   |-- terraform/
|   |-- helm/
|   `-- kubernetes/
|-- examples/
|   `-- demo-repository/
|-- scripts/
|   |-- benchmark/
|   `-- failure-injection/
`-- docs/
    |-- product/
    |-- architecture/
    |-- security/
    |-- operations/
    |-- testing/
    `-- adr/
```
