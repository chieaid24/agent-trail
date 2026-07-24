# Agent Trail

Agent Trail is a control plane for coding agents. Comment `/agent-trail run` on a GitHub issue and it creates a durable task, runs a coding agent in an isolated workspace with scoped credentials, streams every action to a dashboard, independently validates the result, and opens a draft pull request with an evidence report. A human approves the merge.

Status: design phase. The docs below are the spec; implementation follows the milestone queue in GitHub Issues.

## Documentation

- [VISION.md](VISION.md) - standing direction, operating rules, principles, definition of done
- [docs/product/](docs/product/) - positioning, user stories, MVP scope, milestones, backlog
- [docs/architecture/](docs/architecture/) - system design, data model, state machine, API, runner
- [docs/security/](docs/security/) - threat model and risks
- [docs/operations/](docs/operations/) - local development, AWS deployment, observability
- [docs/testing/](docs/testing/) - testing strategy and benchmark plan
- [docs/adr/](docs/adr/) - architecture decision records

## Planned stack

Go control plane, PostgreSQL, Next.js dashboard, Docker and Kubernetes runners, AWS with Terraform, OpenTelemetry.
