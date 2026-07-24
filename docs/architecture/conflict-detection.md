# Conflict Detection

### Phase 1: file overlap

Detect active tasks that:

- Use the same repository
- Modify the same files
- Modify adjacent lines
- Cannot cleanly merge

Tools:

- `git diff --name-only`
- `git merge-tree`
- Temporary merge attempts

### Phase 2: structural overlap

Detect:

- Same function
- Same class
- Same dependency file
- Same database table
- Same API schema
- Same Terraform module
- Same configuration keys

Possible tools:

- Tree-sitter
- OpenAPI parser
- SQL migration parser
- HCL parser

### Phase 3: semantic explanation

An LLM may explain deterministic conflicts.

Example:

```text
Potential conflict: high

Task A removes `users.refresh_token`.
Task B adds a reference to `users.refresh_token`.

Evidence:
- Both modify the users table.
- Task B references a column removed by Task A.
```

Do not block the MVP on semantic conflict detection.
