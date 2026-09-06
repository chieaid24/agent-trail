package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

const FixtureFile = "AGENT_NOTES.md"

const fixtureValidationFile = `version: 1

validation:
  - name: smoke
    category: custom
    command: ["true"]
    timeout_seconds: 60
`

// never executed; exit code and output are simulated
var fakeCommand = []string{"echo", "fake agent validation"}

// deterministic no-model adapter: orchestration path testable without model cost
type Fake struct{}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) Name() string { return "fake" }

func (f *Fake) ValidateConfiguration(ctx context.Context) error { return nil }

func (f *Fake) Start(ctx context.Context, req Request) (Session, error) {
	if req.WorkspaceDir == "" {
		return nil, errors.New("fake adapter: workspace dir required")
	}
	info, err := os.Stat(req.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("fake adapter: workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("fake adapter: workspace %q is not a directory",
			req.WorkspaceDir)
	}

	// unbuffered: producer in lockstep with consumer, cancel takes effect at next step
	s := &fakeSession{
		events: make(chan Event),
		done:   make(chan struct{}),
	}
	go s.run(ctx, req)
	return s, nil
}

type fakeSession struct {
	events chan Event
	done   chan struct{}

	mu        sync.Mutex
	cancelled bool
	result    Result
	err       error
}

func (s *fakeSession) Events() <-chan Event { return s.events }

func (s *fakeSession) Send(ctx context.Context, message string) error {
	return errors.New("fake adapter: session does not accept messages")
}

func (s *fakeSession) Cancel(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
	return nil
}

func (s *fakeSession) Wait(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-s.done:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.err
}

func (s *fakeSession) stopped(ctx context.Context) bool {
	s.mu.Lock()
	cancelled := s.cancelled
	s.mu.Unlock()
	return cancelled || ctx.Err() != nil
}

func (s *fakeSession) emit(t EventType, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	s.events <- Event{Type: t, Timestamp: time.Now().UTC(), Payload: raw}
}

func (s *fakeSession) run(ctx context.Context, req Request) {
	defer close(s.done)
	defer close(s.events)

	fail := func(reason string) {
		s.emit(EventSessionFailed, map[string]any{"reason": reason})
		s.mu.Lock()
		s.err = errors.New("fake adapter: " + reason)
		s.mu.Unlock()
	}

	s.emit(EventSessionStarted, map[string]any{"adapter": "fake"})
	s.emit(EventPlan, map[string]any{
		"plan": "1. Read the task instructions\n" +
			"2. Record them in " + FixtureFile + "\n" +
			"3. Run a verification command\n" +
			"4. Summarize the result",
	})
	if s.stopped(ctx) {
		fail("cancelled")
		return
	}

	fixture := filepath.Join(req.WorkspaceDir, FixtureFile)
	note := fmt.Sprintf("## Fake agent run %s\n\nInstructions:\n%s\n",
		time.Now().UTC().Format(time.RFC3339), req.Instructions)
	f, err := os.OpenFile(fixture, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fail("edit fixture: " + err.Error())
		return
	}
	_, writeErr := f.WriteString(note)
	if err := f.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		fail("edit fixture: " + writeErr.Error())
		return
	}
	s.emit(EventFileWritten, map[string]any{"path": FixtureFile})
	if s.stopped(ctx) {
		fail("cancelled")
		return
	}

	vfile := filepath.Join(req.WorkspaceDir, validation.FileName)
	if err := os.MkdirAll(filepath.Dir(vfile), 0o755); err != nil {
		fail("write validation file: " + err.Error())
		return
	}
	if err := os.WriteFile(vfile, []byte(fixtureValidationFile), 0o644); err != nil {
		fail("write validation file: " + err.Error())
		return
	}
	s.emit(EventFileWritten, map[string]any{"path": validation.FileName})
	if s.stopped(ctx) {
		fail("cancelled")
		return
	}

	command := map[string]any{"command": fakeCommand[0], "args": fakeCommand[1:]}
	s.emit(EventToolRequested, command)
	s.emit(EventToolStarted, command)
	s.emit(EventToolOutput, map[string]any{"stream": "stdout", "chunk": fakeCommand[1]})
	s.emit(EventToolCompleted, map[string]any{
		"command": fakeCommand[0], "exit_code": 0, "simulated": true,
	})
	if s.stopped(ctx) {
		fail("cancelled")
		return
	}

	summary := "Fake agent recorded the instructions in " + FixtureFile +
		" and simulated one verification command."
	s.emit(EventAssistantMessage, map[string]any{"message": summary})
	s.emit(EventSessionCompleted, map[string]any{"summary": summary})

	s.mu.Lock()
	s.result = Result{
		Summary:      summary,
		FilesChanged: []string{FixtureFile, validation.FileName},
	}
	s.mu.Unlock()
}
