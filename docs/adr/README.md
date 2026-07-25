# Architecture Decision Records

One file per decision, numbered `NNNN-short-title.md`, using [template.md](template.md). Write the ADR in the same PR as the change it justifies; update or supersede it when the architecture moves.

Create ADRs for:

1. GitHub augmentation versus repository hosting
2. Go versus NestJS
3. PostgreSQL queue versus SQS
4. Docker runner versus Kubernetes Job
5. SSE versus WebSockets
6. Standard-library GitHub client versus an SDK
7. Git mirror cache and worktrees
7. Agent adapter boundary
8. Trusted validation
9. Log storage
10. GitHub App permissions
11. Runner authentication
12. Secret lifecycle
13. Network policy
14. Task leases
15. Evidence schema
16. Data retention
17. Conflict detection

Each ADR should include:

- Context
- Decision
- Alternatives
- Consequences
- Security implications
- Revisit conditions
