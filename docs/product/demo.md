# Demonstration Script

## Recommended Demonstration

### Successful flow

1. Open a real issue in a sample repository.
2. Comment `/agent-trail run`.
3. Agent Trail creates a task.
4. The dashboard shows it as queued.
5. A runner creates a worktree and branch.
6. The agent generates a plan.
7. Agent actions stream live.
8. The agent changes code.
9. Agent Trail independently runs tests.
10. Agent Trail pushes the branch.
11. Agent Trail opens a draft pull request.
12. The pull request contains verified evidence.
13. The workspace is cleaned up.

### Failure flow

1. Start a deliberately long task.
2. Cancel it from the dashboard.
3. Confirm the agent process stops.
4. Confirm logs remain available.
5. Confirm no pull request is created.
6. Confirm cleanup occurs.
