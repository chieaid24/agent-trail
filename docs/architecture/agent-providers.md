# Agent Provider Abstraction

The provider-neutral interface, implemented in `internal/agent` (the fake
adapter with it):

```go
package agent

type Adapter interface {
    Name() string
    ValidateConfiguration(ctx context.Context) error
    Start(ctx context.Context, req Request) (Session, error)
}

type Session interface {
    Events() <-chan Event
    Send(ctx context.Context, message string) error
    Cancel(ctx context.Context) error
    Wait(ctx context.Context) (Result, error)
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
