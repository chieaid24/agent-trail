# Validation pipeline

Every push runs a gate pipeline before it can land. A PR merges automatically
when the pipeline passes. No human is in the loop.

Adapted from [no-mistakes](https://github.com/kunchenguid/no-mistakes) - the same
gate sequence (review, test, document, lint), with the human-approval steps
removed. no-mistakes parks judgment calls for a human and stops after opening the
PR; this pipeline resolves them automatically and merges on green.

This is the repo's own dev merge gate. It is not the platform's
[trusted validation](../architecture/validation.md) feature, which validates a
coding agent's output for end users.

## Gates, in order

Two layers, split by whether the gate needs judgment.

Agent-run, pre-merge (the AI gates):

- **review** - the agent reviews its own diff for defects, regressions, and spec
  drift.
- **document** - the agent updates the docs an implementation change touches, per
  the ownership map in `AGENTS.md`. `docs/` is authoritative: when code diverges
  from a doc, the same PR updates the doc.

Deterministic, in CI (the required `test` check, `scripts/gate.sh`):

- **format** - `gofmt` for Go, `format:check` for Node. Absent tooling skips.
- **lint** - `go vet`, or the `lint` npm script.
- **test** - `go test ./...`, or the `test` npm script.
- **docs ASCII** - markdown is printable ASCII only.

Then **push** -> **PR** (`Closes #<issue>`) -> **CI** -> **merge**.

## Full-auto policy

The pipeline never waits for a human. It replaces no-mistakes' `ask-user` parking
with a decision the agent makes and records.

- Objective finding (a lint break, a failing test, a gofmt diff): the agent fixes
  it and re-runs the gate. Bounded to 3 attempts per gate, matching the CI babysit
  cap.
- Judgment call (an ambiguous review finding, a debatable doc placement): the
  agent decides, proceeds, and records the decision in the PR body. It does not
  pause.
- Deterministic gate red after 3 fix attempts: the agent labels the issue
  `blocked`, writes the failure into it, releases its claim, and stops the loop.
  It never merges a red `test` check.

## Enforcement

- CI runs `scripts/gate.sh` on every push and every PR, as the job named `test`.
- `main` is branch-protected: `test` is a required check, with no required
  reviews. The agent merges its own PR - `gh pr merge <pr> --squash
  --delete-branch` - once `test` is green, then confirms the PR reads `MERGED`.

Branch protection is the hard gate: a red or missing `test` check cannot merge.
The agent-run gates raise quality before CI; they are not the enforcement point.

## Run it locally

```bash
bash scripts/gate.sh
```

The deterministic gates run in the no-mistakes order and report each as PASS,
FAIL, or SKIP. Absent tooling skips and stays green, so the gates fill in as the
Go/Next.js monorepo lands - add a `go.mod`, or `format:check`/`lint`/`test`
scripts to `package.json`, and the gate picks them up with no config change.
