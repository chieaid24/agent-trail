# Agent Trail: Vision and Operating Rules

Agent Trail is a control plane for coding agents. A developer installs the Agent Trail GitHub App, enables a repository, and comments `/agent-trail run` on an issue. Agent Trail creates a durable task, provisions an isolated runner with scoped credentials, streams the agent's actions to a dashboard, independently validates the result, and opens a draft pull request backed by a structured evidence report. A human approves the merge.

The value is not calling an AI model - existing tools do that. The value is converting an unpredictable agent session into a controlled engineering workflow: durable task state, isolated execution, scoped credentials, live observability, reproducible validation, human approval, failure recovery, and audit history.

This file is the standing direction for the whole project. Every issue, plan, and PR in this repo answers to it.

## How work happens in this repo

**All work in this repo is done autonomously by agents.** Humans set direction; agents plan, implement, validate, and merge through the issue queue.

- Work flows through the dependency-aware GitHub Issues queue set up by bootstrap-issues. Every change starts as an issue, lands as a PR, passes the CI gate, and merges.
- Issues default to the `afk` (autonomous) label. Use `hitl` only when a step truly requires the user: registering the GitHub App, provisioning cloud accounts or spend, supplying credentials or secrets, or an irreversible externally visible action. Everything else is agent work.
- Do not wait for human review on `afk` issues. Merge when the acceptance criteria are demonstrated and CI is green.
- Decisions that shape architecture get an ADR in `docs/adr/` in the same PR. Docs in `docs/` are the spec; when implementation diverges from them, the same PR updates the doc.
- Never invent metrics. Benchmark numbers appear only after the benchmark runs.

## Product principles

These principles bind every milestone:

1. **Human approval is authoritative.** Agent Trail never merges into protected branches automatically in the MVP.
2. **Enforce rules outside the model.** Do not rely on prompts like "do not access production." The runner simply does not have production credentials.
3. **Evidence over claims.** Distinguish agent-reported tests, commands the runner actually executed, platform-verified tests, skipped checks, failed checks, and unverified assumptions.
4. **Provider independence.** Multiple agent providers sit behind one internal interface. The MVP may support only one.
5. **GitHub remains the collaboration surface.** Do not rebuild pull requests, repository hosting, or merge review.
6. **Scope control.** A polished single-agent workflow beats a shallow multi-agent demo.
7. **Safe failure.** A crash, timeout, cancellation, or rate limit results in a clear state with preserved logs.
8. **Reproducibility.** Record base commit, final commit, runner image, agent provider and model, policy version, validation commands, runtime, files changed, and human interventions.

## Required vertical slice

Before Kubernetes or advanced UI, build:

```text
Signed GitHub webhook
    -> persistent task
    -> fake agent runner
    -> activity timeline
    -> evidence report
    -> GitHub issue comment or draft PR
```

This proves the complete system shape.

## Definition of done

An issue closes only when:

- Code implemented
- Unit tests pass
- Integration tests pass
- Documentation updated
- Observability added
- Error behavior defined
- Security impact considered
- Acceptance criteria demonstrated
- No secrets committed

## Final direction

Build the smallest version that reliably completes this workflow:

```text
GitHub issue
  -> isolated agent task
  -> observable execution
  -> trusted validation
  -> evidence-backed pull request
  -> cleanup
```

Do not begin by building a GitHub replacement or an advanced multi-agent planner. The engineering value comes from turning a stateful, security-sensitive coding agent into a controlled distributed system.

## Where the detail lives

- [docs/product/](docs/product/) - positioning, user stories, MVP scope, milestones, backlog, demo script, completion checklist
- [docs/architecture/](docs/architecture/) - system overview, data model, state machine, GitHub App, runner, workspaces, providers, policy, validation, evidence, API, frontend, streaming, publishing, conflict detection
- [docs/security/](docs/security/) - threat model, risks
- [docs/operations/](docs/operations/) - local development, AWS deployment, observability, reliability targets
- [docs/testing/](docs/testing/) - testing strategy, benchmark plan
- [docs/adr/](docs/adr/) - decision records
