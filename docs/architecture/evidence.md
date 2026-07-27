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

Schema v1 (ADR-0009) implements the task, execution, plan, changes,
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

## Summary

Added single-use refresh-token rotation and replay detection.

## Implementation

- Added hashed refresh-token identifiers.
- Rotated tokens transactionally.
- Rejected replayed tokens.
- Added migration and integration tests.

## Verified by Agent Trail

| Check | Result | Duration |
|---|---:|---:|
| Formatting | Passed | 4s |
| Unit tests | Passed, 183 tests | 18s |
| Integration tests | Passed, 14 tests | 31s |
| Build | Passed | 22s |

## Risks

- Existing sessions may require reauthentication.

## Unverified

- Browser-specific cookie behavior was not manually tested.

## Execution metadata

- Base commit: `abc123`
- Final commit: `def456`
- Agent provider: Claude Code
- Human interventions: 1
- Full evidence: [Open in Agent Trail](...)
```

Do not expose raw prompts, private repository content, or secrets.
