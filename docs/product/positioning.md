# Positioning

## Product Positioning

### One-sentence pitch

> Agent Trail is an agent-native control plane for safely running, reviewing, and coordinating coding agents across GitHub repositories.

### User-facing explanation

Agent Trail turns GitHub issues into isolated coding-agent tasks. Each task gets its own workspace, branch, limits, permissions, logs, validation results, and pull request.

### What Agent Trail is not

Agent Trail is not:

- A replacement for Git
- A replacement for GitHub repository hosting
- A new large language model
- A full GitHub clone
- A general CI/CD system
- A prompt collection
- A Claude-only skill
- An autonomous merge bot
- A production-grade service for executing arbitrary hostile public repositories in version one

GitHub remains the source of truth for:

- Repositories
- Issues
- Pull requests
- Review comments
- Branch protection
- Merge approval

Agent Trail adds the agent execution and governance layer.


## Why This Product Should Exist

Coding agents already write code, but the surrounding workflow remains weak.

Common problems include:

- Agent sessions live only inside local terminals.
- Reviewers cannot easily see what commands were run.
- Agents may receive more filesystem or secret access than necessary.
- Tests shown in a pull request may only be claims made by the agent.
- Multiple agents may edit overlapping systems.
- Context disappears between sessions.
- Abandoned worktrees, containers, and branches require cleanup.
- Different agent providers have incompatible interfaces.
- Agent-generated pull requests can be difficult to understand.
- Teams lack a consistent audit trail.

Agent Trail should answer:

> Can a developer delegate a real issue to an agent, observe exactly what happens, restrict what the agent can access, independently verify the result, and review it through a normal GitHub pull request?


## Target Users

### Individual developer

Wants to delegate an issue without manually managing terminals, branches, logs, and cleanup.

### Reviewer

Wants to understand what changed, why, what was verified, and what remains risky.

### Team lead

Wants standardized review evidence and approval requirements.

### Platform engineer

Wants scoped credentials, resource controls, audit history, and repeatable infrastructure.
