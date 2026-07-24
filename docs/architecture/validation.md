# Validation

Agent Trail should support two validation classes.

### Agent-run validation

Commands the agent chooses to run.

Useful, but not independently trusted.

### Trusted platform validation

Commands Agent Trail runs after editing ends.

These receive:

```json
{
  "trusted_execution": true
}
```

### Repository validation file

Example:

```yaml
version: 1

validation:
  - name: format-check
    category: format
    command: ["npm", "run", "format:check"]
    timeout_seconds: 300

  - name: lint
    category: lint
    command: ["npm", "run", "lint"]
    timeout_seconds: 300

  - name: unit-tests
    category: unit_test
    command: ["npm", "test", "--", "--runInBand"]
    timeout_seconds: 600

  - name: build
    category: build
    command: ["npm", "run", "build"]
    timeout_seconds: 600
```

Requirements:

- Validate syntax.
- Limit command count.
- Limit runtime.
- Preserve exit codes.
- Preserve reports where available.
- Distinguish test failures from infrastructure failures.
- Never convert a failure into success based on agent text.
