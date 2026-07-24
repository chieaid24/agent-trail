# Agent Provider Abstraction

Define a provider-neutral interface:

```go
type AgentAdapter interface {
    Name() string
    ValidateConfiguration(ctx context.Context) error
    Start(ctx context.Context, req AgentRequest) (AgentSession, error)
}

type AgentSession interface {
    Events() <-chan AgentEvent
    Send(ctx context.Context, message string) error
    Cancel(ctx context.Context) error
    Wait(ctx context.Context) (AgentResult, error)
}
```

Normalized events:

- session_started
- assistant_message
- plan
- tool_requested
- tool_started
- tool_output
- tool_completed
- file_read
- file_written
- cost_update
- warning
- session_completed
- session_failed

The web client should consume Agent Trail events, not raw provider formats.

### First provider

Choose one after verifying current automation support:

- Claude Code CLI
- Codex CLI or SDK
- API-based coding agent

A fake adapter must be implemented first.

The fake adapter should:

- Emit a plan
- Edit a known fixture file
- Emit command events
- Return a final summary

This allows the orchestration system to be tested without model cost.
