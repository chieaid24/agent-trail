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
8. Agent adapter boundary
9. Trusted validation
10. Log storage
11. GitHub App permissions
12. Runner authentication
13. Dashboard sessions
14. Secret lifecycle
15. Network policy
16. Task leases
17. Evidence schema
18. Data retention
19. Conflict detection

Each ADR should include:

- Context
- Decision
- Alternatives
- Consequences
- Security implications
- Revisit conditions
