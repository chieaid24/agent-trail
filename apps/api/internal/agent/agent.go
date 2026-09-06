package agent

import (
	"context"
	"encoding/json"
	"time"
)

// control plane consumes these, never raw provider formats
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

type Event struct {
	Type      EventType
	Timestamp time.Time
	Payload   json.RawMessage
}

type Request struct {
	WorkspaceDir string
	Instructions string
}

type Result struct {
	Summary      string
	FilesChanged []string
}

type Adapter interface {
	Name() string
	ValidateConfiguration(ctx context.Context) error
	Start(ctx context.Context, req Request) (Session, error)
}

// cancel must honor its context; events closes only after provider stops, then wait returns
type Session interface {
	Events() <-chan Event
	Send(ctx context.Context, message string) error
	Cancel(ctx context.Context) error
	Wait(ctx context.Context) (Result, error)
}
