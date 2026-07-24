# Portfolio Presentation

The final project page should include:

- One-sentence pitch
- 60 to 90 second demo
- Architecture diagram
- Dashboard screenshots
- Security model
- Evidence-report example
- Benchmark methodology
- Benchmark results
- Known limitations
- Local setup
- Cloud deployment
- Engineering tradeoffs
- Roadmap

Strong demo moments:

- Live agent timeline
- Two isolated tasks
- Denied dangerous command
- Trusted tests versus agent claims
- Draft PR with evidence
- Cancellation and cleanup
- Active-task overlap warning


## Sample Resume Entry

Do not use these numbers until measured.

**Agent Trail - Agent-Native Development Platform** | Go, PostgreSQL, Kubernetes, Docker, GitHub Apps, AWS, Terraform

- Built an agent-native development platform that converted GitHub issues into isolated coding tasks, orchestrating **20 concurrent agent sessions** across ephemeral Kubernetes Jobs with independent Git worktrees, resource limits, and live execution logs.
- Designed an idempotent GitHub webhook and task-processing pipeline that handled **10,000 simulated deliveries without duplicate task creation**, generating evidence-backed pull requests with verified tests, command history, permission usage, and risk summaries.
- Implemented scoped execution policies, heartbeat-based failure recovery, cancellation, and TTL workspace cleanup, achieving **100% resource cleanup across 100 forced runner-failure and cancellation tests**.

Only use a review-time metric if a real controlled study supports it.
