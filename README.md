# Agent Trail

Agent Trail is a control plane for coding agents. Comment `/agent-trail run` on a GitHub issue and it creates a durable task, runs a coding agent in an isolated workspace with scoped credentials, streams every action to a dashboard, independently validates the result, and opens a draft pull request with an evidence report. A human approves the merge.

Status: early. The monorepo, CI gate, dev environment, and task domain (state machine, activity timeline, tasks API) exist; the GitHub integration is next in the milestone queue.

## Quickstart

Requires Go 1.26+, Node 24+, and Docker.

```bash
make dev      # compose infra + migrations + api, worker, web
make test     # unit tests for both apps
make hooks    # activate the pre-commit hook (once per clone)
```

`make dev` serves the API on :8080 and the dashboard on :3000. See
[docs/operations/local-development.md](docs/operations/local-development.md)
for every target and port.

## Layout

- `apps/api/` - Go control plane: `api` (HTTP), `worker` (scheduler skeleton), `migrate` (goose)
- `apps/web/` - Next.js dashboard
- `deploy/dev/` - compose configs for the dev infrastructure
- `scripts/` - `gate.sh` (the CI gate), `dev.sh` (app runner)
- `docs/` - the spec; implementation follows it, and PRs that diverge update it

## Documentation

- [VISION.md](VISION.md) - standing direction, operating rules, principles, definition of done
- [docs/product/](docs/product/) - positioning, user stories, MVP scope, milestones, backlog
- [docs/architecture/](docs/architecture/) - system design, data model, state machine, API, runner
- [docs/security/](docs/security/) - threat model and risks
- [docs/operations/](docs/operations/) - local development, AWS deployment, observability
- [docs/testing/](docs/testing/) - testing strategy and benchmark plan
- [docs/adr/](docs/adr/) - architecture decision records

## Stack

Go control plane, PostgreSQL, Next.js dashboard, Docker and Kubernetes runners, AWS with Terraform, OpenTelemetry.
