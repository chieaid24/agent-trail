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

The first real provider is the Claude Code CLI (ADR-0008), documented
below. Codex CLI/SDK and API-based agents land later behind the same
interface.

A fake adapter is implemented first.

The fake adapter should:

- Emit a plan
- Edit a known fixture file
- Emit command events
- Return a final summary

This allows the orchestration system to be tested without model cost.

## Provider selection

The worker builds one adapter at startup from configuration and uses it for
every attempt (`agent.New`). Per-task provider fields exist on the `tasks` row
(`agent_provider`, `agent_model`) but are not yet read; switching providers per
task is future work.

| Variable | Default | Meaning |
| --- | --- | --- |
| `AGENT_PROVIDER` | `fake` | `fake` or `claude-code` |
| `AGENT_CLI_PATH` | `claude` | Claude Code executable, resolved from PATH when bare |
| `AGENT_MODEL` | (CLI default) | provider model |
| `AGENT_PERMISSION_MODE` | `acceptEdits` | one of `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `AGENT_CLI_VERSION` | (unset) | required whole version token of `claude --version`; unset skips the check |
| `AGENT_TIMEOUT_SECONDS` | `2700` | hard per-attempt runtime cap |

The worker calls `ValidateConfiguration` once at startup and refuses to start
if the selected provider is misconfigured (CLI missing, or a pinned version
that does not match), so the failure surfaces once rather than on every task.

## Claude Code CLI adapter

The first real provider runs the Claude Code CLI as a subprocess in the task
workspace:

```text
claude --print <instructions> --output-format stream-json --verbose \
       --permission-mode <mode> [--model <model>]
```

The CLI is exec'd directly with an argument array, never through a shell, so
nothing in the instructions is interpreted by one. Print mode takes no
follow-up input: `Session.Send` returns an error for this provider. Its
environment is reduced to PATH, HOME, and the Anthropic settings
(`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_BASE_URL`);
telemetry and the auto-updater are disabled. Credentials pass through from
the worker's environment, and their values are redacted out of any failure
detail before it reaches a log or an event.

### Stream normalization

The adapter reads the CLI's newline-delimited stream-json and maps each record
onto the neutral event stream. This mapping is the whole provider boundary;
nothing Claude-specific leaves the `agent` package.

| stream-json record | Neutral event |
| --- | --- |
| `system` / `init` | `session_started` |
| assistant `text` block | `assistant_message` |
| assistant `tool_use` `ExitPlanMode` or `TodoWrite` | `plan` |
| assistant `tool_use` (other) | `tool_requested` |
| `tool_use` `Read` | `file_read` (after `tool_requested`) |
| `tool_use` `Write`/`Edit`/`MultiEdit`/`NotebookEdit` | `file_written` (after `tool_requested`) |
| user `tool_result` | `tool_output` (if content), then `tool_completed` |
| `result` | `cost_update`, then `session_completed` or `session_failed` |

Known housekeeping records (non-`init` `system` records, `rate_limit_event`)
are ignored; a record type the normalizer does not know becomes a `warning`
event rather than being dropped silently.

`Result.Summary` is the final `result` text; `Result.FilesChanged` is the set
of paths from file-writing tools. A non-success `result` subtype (for example
`error_max_turns`) becomes `session_failed`. `TodoWrite` fires on every todo
update, so one session usually carries several `plan` events; the latest one
is the current plan.

A session may end without ever emitting a `plan` (the event that advances a
task from planning to executing); the executor advances such a task to
executing at session end rather than stranding it in planning.

### Timeout and cancellation

`AGENT_TIMEOUT_SECONDS` and the caller's context both back the subprocess
through `exec.CommandContext`: whichever fires first kills the CLI's whole
process group, so tool subprocesses the CLI spawned die with it instead of
surviving to hold the output pipe open. A timeout ends the session as
`session_failed` with reason `timeout` and a `DeadlineExceeded` error; a
`Cancel` ends it with reason `cancelled`. Either way the processes are
stopped, not left running.

### Guarding against CLI changes

The CLI is an external dependency that can change under us
(docs/security/risks.md). Two guards apply: `AGENT_CLI_VERSION` pins the
version at startup (a whole version token, so `2.1.3` does not accept
`2.1.30`), and contract tests feed recorded stream-json through the
normalizer and assert the resulting neutral events, so a changed output shape
fails a test rather than corrupting the timeline. The tests drive the adapter
against a stub CLI, so the whole path runs with no model cost.

### Security limitations

The CLI runs unsandboxed on the worker host with the worker's `HOME`, so
host Claude configuration and credentials are reachable from the agent
process until runner isolation lands. The permission mode is validated
against an allowlist, but `bypassPermissions` removes the CLI's own
confirmation gate and belongs only on an isolated runner. Redaction is
best-effort: it strips URL userinfo and the credential values the worker
knows, nothing else.
