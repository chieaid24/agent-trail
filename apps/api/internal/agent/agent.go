// Package agent defines the provider-neutral agent adapter interface and its
// normalized event stream (docs/architecture/agent-providers.md), plus the
// fake adapter that exercises the orchestration path without model cost.
package agent

import (
	"context"
	"encoding/json"
	"time"
)

// EventType is a normalized agent event type. The control plane consumes
// these, never raw provider formats.
type EventType string

const (
	EventSessionStarted   EventType = "session_started"
	EventAssistantMessage EventType = "assistant_message"
	EventPlan             EventType = "plan"
	EventToolRequested    EventType = "tool_requested"
	EventToolStarted      EventType = "tool_started"
	EventToolOutput       EventType = "tool_output"
	EventToolCompleted    EventType = "tool_completed"
	EventFileRead         EventType = "file_read"
	EventFileWritten      EventType = "file_written"
	EventCostUpdate       EventType = "cost_update"
	EventWarning          EventType = "warning"
	EventSessionCompleted EventType = "session_completed"
	EventSessionFailed    EventType = "session_failed"
)

// Event is one normalized event emitted by an agent session.
type Event struct {
	Type      EventType
	Timestamp time.Time
	// Payload is event-specific JSON (plan text, tool arguments, file path...).
	Payload json.RawMessage
}

// Request describes one agent invocation inside a prepared workspace.
type Request struct {
	// WorkspaceDir is the directory the agent may read and write.
	WorkspaceDir string
	Instructions string
}

// Result is the final outcome of a completed session.
type Result struct {
	Summary      string
	FilesChanged []string
}

// Adapter is the provider-neutral entry point. Implementations: fake (this
// milestone), Claude Code CLI (milestone 5).
type Adapter interface {
	Name() string
	ValidateConfiguration(ctx context.Context) error
	Start(ctx context.Context, req Request) (Session, error)
}

// Session is one running agent invocation. Events must be drained; the
// channel closes when the session ends, after which Wait returns.
type Session interface {
	Events() <-chan Event
	Send(ctx context.Context, message string) error
	Cancel(ctx context.Context) error
	Wait(ctx context.Context) (Result, error)
}
