# Evidence Report and PR Body

## Evidence Report

The report should answer:

- What was requested?
- What changed?
- Why?
- What was verified?
- What failed?
- What remains uncertain?
- What permissions were granted?
- What human actions occurred?

Schema v1 (ADR-0010) implements the task, execution, plan, changes,
validation, risks, and unverified fields below; permissions, policy, and
human-intervention fields arrive with their subsystems as measured facts.

Example JSON:

```json
{
  "schema_version": 1,
  "task": {
    "id": "uuid",
    "source_issue": 42,
    "title": "Add refresh-token rotation",
    "requested_by": "aidan"
  },
  "execution": {
    "agent_provider": "claude-code",
    "agent_model": "configured-default",
    "base_commit": "abc123",
    "final_commit": "def456",
    "runner_image": "agent-trail-runner@sha256:...",
    "policy_version": 3,
    "duration_seconds": 842,
    "human_interventions": 1
  },
  "plan": [
    "Inspect authentication service",
    "Add token rotation",
    "Add replay detection",
    "Add tests"
  ],
  "changes": {
    "files_changed": 8,
    "insertions": 214,
    "deletions": 43,
    "areas": ["authentication", "database", "tests"]
  },
  "validation": [
    {
      "name": "unit-tests",
      "status": "passed",
      "trusted_execution": true,
      "duration_ms": 18342
    }
  ],
  "permissions": {
    "network_profile": "package-registries",
    "secrets_granted": ["AGENT_PROVIDER_TOKEN"],
    "denied_actions": 0
  },
  "risks": [
    "Existing sessions may require reauthentication"
  ],
  "unverified": [
    "Browser cookie behavior was not manually tested"
  ]
}
```


## Pull-Request Body

```markdown
## Agent Trail task

Closes #42

Task `9f3c1e20-8a4b-4c2d-9e11-2b7f5a0c1d34`.

## Summary

Added single-use refresh-token rotation and replay detection.

## Implementation

- Added hashed refresh-token identifiers.
- Rotated tokens transactionally.
- Rejected replayed tokens.
- Added migration and integration tests.

## Verified by Agent Trail

Checks the platform executed in the workspace after editing ended.

| Check | Category | Result | Exit code | Duration |
|---|---|---|---:|---:|
| formatting | format | passed | 0 | 4200ms |
| unit-tests | test | passed | 0 | 18342ms |
| integration-tests | test | passed | 0 | 31004ms |
| build | build | passed | 0 | 22001ms |

## Risks

- Existing sessions may require reauthentication.

## Unverified

- Browser-specific cookie behavior was not manually tested.

## Execution metadata

- Base commit: `abc123`
- Final commit: `def456`
- Agent provider: fake
- Agent model: claude-sonnet-5
- Duration: 75s
- Changes: 9 files
```

The exact shape is rendered by `apps/api/internal/evidence/prbody.go`. Trusted
results and agent claims never share a table: when the agent reports checks the
platform did not independently run, a separate `## Agent-reported (not
independently verified)` table follows the verified one, and the unexecuted
claims are also counted under `## Unverified`.

Do not expose raw prompts, private repository content, or secrets.
