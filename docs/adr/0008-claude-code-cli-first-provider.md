# ADR-0008: Claude Code CLI as the first real agent provider

- Status: accepted
- Date: 2026-07-27

## Context

Milestone 5 needs the first real agent behind the provider-neutral
`agent.Adapter` interface that the fake adapter already implements. The
open choice in docs/architecture/agent-providers.md was which provider to
integrate first and through which surface: a CLI subprocess, a provider
SDK, or a bare model API around a homegrown agent loop. The provider is an
external dependency the control plane cannot version together with itself,
so whatever surface is chosen must tolerate drift.

## Decision

The first real provider is the Claude Code CLI, run as a subprocess in the
task workspace with `--print` and `--output-format stream-json`, normalized
into the neutral event stream inside the `agent` package. Provider
selection is worker configuration (`AGENT_PROVIDER`), and every
Claude-specific type stays behind the adapter boundary.

## Alternatives

- Claude Agent SDK: a typed stream instead of parsed stdout, but it moves
  the agent loop into the control plane's dependency tree and pins the
  control plane's release cadence to the SDK's. The CLI ships the same
  loop as a self-contained binary the runner host already needs for local
  development.
- Codex CLI: comparable surface, but the project runs on Anthropic
  credentials today; a second provider is the test that the adapter
  boundary holds, and it lands later behind the same interface.
- Bare model API with a homegrown loop: full control of the event shape at
  the cost of rebuilding tool execution, permissions, and planning that the
  CLI already provides. Rejected for scope.

## Consequences

- The stream-json output is an informal contract. Contract tests feed
  recorded stream output through the normalizer, and `AGENT_CLI_VERSION`
  pins the CLI version at startup, so drift fails loudly instead of
  corrupting timelines.
- Print mode takes no follow-up input, so `Session.Send` is unsupported
  for this provider until an interactive surface is adopted.
- The CLI spawns tool subprocesses; the adapter owns the whole process
  group so timeout and cancellation kill everything the session started.

## Security implications

The CLI runs unsandboxed on the worker host with the worker's `HOME`, so
host-level Claude configuration and credentials are reachable from the
agent process until runner isolation lands (milestone 9 track). Its
environment is reduced to PATH, HOME, and Anthropic credentials;
credential values are redacted from failure detail best-effort. The
permission mode is a validated allowlist; `bypassPermissions` removes the
CLI's own confirmation gate and should wait for isolated runners.
