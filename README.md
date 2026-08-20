# Agent Trail

Agent Trail is a control plane for coding agents. Comment `/agent-trail run` on a GitHub issue and it creates a durable task, runs a coding agent in an isolated workspace with scoped credentials, streams every action to a dashboard, independently validates the result, and opens a draft pull request with an evidence report. A human approves the merge.

Status: the issue-to-PR path runs end to end. A signed GitHub webhook creates a durable task; the worker claims it from a PostgreSQL queue, runs an agent (a no-cost fake, or the Claude Code CLI behind `AGENT_PROVIDER`) in an isolated Git worktree, validates the result outside the agent's session, and opens a draft pull request backed by an evidence report, streaming every step to the dashboard over SSE. Conflict detection, OpenTelemetry, and a hardened Kubernetes runner Job (verified in a local kind cluster) are in place; the AWS Terraform is authored and validated but not applied.

## Quickstart

Requires Go 1.26+, Node 24+, and Docker.

```bash
make dev      # compose infra + migrations + api, worker, web
make test     # unit tests for both apps
make hooks    # activate the pre-commit hook (once per clone)
```

`make dev` serves the API on :8080 and the dashboard on :3000. See the
[Makefile](Makefile) for every target and port.

## Layout

- `apps/api/` - Go control plane: `api` (HTTP), `worker` (runner host), `migrate` (goose)
- `apps/web/` - Next.js dashboard
- `deploy/dev/` - compose configs for the local infrastructure
- `scripts/` - `gate.sh` (the CI gate), `dev.sh` (app runner)
- `docs/` - benchmark results and dashboard screenshots

## Documentation

- [docs/testing/benchmarks.md](docs/testing/benchmarks.md) - benchmark and failure-injection plan
- [docs/testing/benchmark-results.md](docs/testing/benchmark-results.md) - measured results

## Tools Used

<table>
  <tr>
    <td><strong>Backend</strong></td>
    <td><img alt="Go" src="https://img.shields.io/badge/Go-%2300ADD8?style=for-the-badge&logo=go&logoColor=%23FFFFFF"> <img alt="GitHub App" src="https://img.shields.io/badge/GitHub%20App-%23181717?style=for-the-badge&logo=github&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Frontend</strong></td>
    <td><img alt="TypeScript" src="https://img.shields.io/badge/TypeScript-%233178C6?style=for-the-badge&logo=typescript&logoColor=%23FFFFFF"> <img alt="Next.js" src="https://img.shields.io/badge/Next.js-black?style=for-the-badge&logo=nextdotjs&logoColor=%23FFFFFF"> <img alt="React" src="https://img.shields.io/badge/React-%2361DAFB?style=for-the-badge&logo=react&logoColor=%23000000"> <img alt="Tailwind CSS" src="https://img.shields.io/badge/Tailwind%20CSS-%2306B6D4?style=for-the-badge&logo=tailwindcss&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Data</strong></td>
    <td><img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-%234169E1?style=for-the-badge&logo=postgresql&logoColor=%23FFFFFF"> <img alt="pgx" src="https://img.shields.io/badge/pgx-%23336791?style=for-the-badge&logoColor=%23FFFFFF"> <img alt="goose" src="https://img.shields.io/badge/goose-%235B4B8A?style=for-the-badge&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>AI Inference</strong></td>
    <td><img alt="Claude Code" src="https://img.shields.io/badge/Claude%20Code-%23D97757?style=for-the-badge&logo=claude&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Observability</strong></td>
    <td><img alt="OpenTelemetry" src="https://img.shields.io/badge/OpenTelemetry-%23425CC7?style=for-the-badge&logo=opentelemetry&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Infrastructure</strong></td>
    <td><img alt="Docker" src="https://img.shields.io/badge/Docker-%232496ED?style=for-the-badge&logo=docker&logoColor=%23FFFFFF"> <img alt="Kubernetes" src="https://img.shields.io/badge/Kubernetes-%23326CE5?style=for-the-badge&logo=kubernetes&logoColor=%23FFFFFF"> <img alt="Terraform" src="https://img.shields.io/badge/Terraform-%23844FBA?style=for-the-badge&logo=terraform&logoColor=%23FFFFFF"> <img alt="AWS" src="https://img.shields.io/badge/AWS-%23FF9900?style=for-the-badge&logo=amazonwebservices&logoColor=%23FFFFFF"> <img alt="GitHub Actions" src="https://img.shields.io/badge/GitHub%20Actions-%232088FF?style=for-the-badge&logo=githubactions&logoColor=%23FFFFFF"></td>
  </tr>
  <tr>
    <td><strong>Testing</strong></td>
    <td><img alt="Vitest" src="https://img.shields.io/badge/Vitest-%236E9F18?style=for-the-badge&logo=vitest&logoColor=%23FFFFFF"> <img alt="Playwright" src="https://img.shields.io/badge/Playwright-%232EAD33?style=for-the-badge&logo=playwright&logoColor=%23FFFFFF"> <img alt="ESLint" src="https://img.shields.io/badge/ESLint-%234B32C3?style=for-the-badge&logo=eslint&logoColor=%23FFFFFF"></td>
  </tr>
</table>
