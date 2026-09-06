package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
)

// instruction prefixes selecting scripted session behaviour
const (
	modeComplete = "bench-mode=complete"
	modeBarrier  = "bench-mode=barrier"
	modeFail     = "bench-mode=fail"
	modeCancel   = "bench-mode=cancel" // "bench-mode=cancel key=<n>"
	modeHang     = "bench-mode=hang"
)

type barrier struct {
	mu      sync.Mutex
	n       int
	arrived int
	release chan struct{}
}

func newBarrier(n int) *barrier {
	return &barrier{n: n, release: make(chan struct{})}
}

func (b *barrier) arrive(stop <-chan struct{}) error {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
		return nil
	case <-stop:
		return errors.New("session stopped at barrier")
	case <-time.After(60 * time.Second):
		return fmt.Errorf("barrier timed out waiting for %d sessions", b.n)
	}
}

type scriptAdapter struct {
	barrier *barrier
	// runs synchronously right after session_started, mid-flight; used to cancel own task
	onStarted func(instructions string)
}

func (a *scriptAdapter) Name() string { return "bench-script" }

func (a *scriptAdapter) ValidateConfiguration(ctx context.Context) error { return nil }

func (a *scriptAdapter) Start(ctx context.Context, req agent.Request) (agent.Session, error) {
	if req.WorkspaceDir == "" {
		return nil, errors.New("bench adapter: workspace dir required")
	}
	s := &scriptSession{
		adapter: a,
		events:  make(chan agent.Event),
		done:    make(chan struct{}),
		stop:    make(chan struct{}),
	}
	go s.run(ctx, req)
	return s, nil
}

type scriptSession struct {
	adapter *scriptAdapter
	events  chan agent.Event
	done    chan struct{}

	stopOnce sync.Once
	stop     chan struct{}

	mu     sync.Mutex
	result agent.Result
	err    error
}

func (s *scriptSession) Events() <-chan agent.Event { return s.events }

func (s *scriptSession) Send(ctx context.Context, message string) error {
	return errors.New("bench adapter: session does not accept messages")
}

func (s *scriptSession) Cancel(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stop) })
	return nil
}

func (s *scriptSession) Wait(ctx context.Context) (agent.Result, error) {
	select {
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	case <-s.done:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.err
}

func (s *scriptSession) emit(t agent.EventType, payload map[string]any) bool {
	raw, _ := json.Marshal(payload)
	select {
	case s.events <- agent.Event{Type: t, Timestamp: time.Now().UTC(), Payload: raw}:
		return true
	case <-s.stop:
		return false
	}
}

func (s *scriptSession) fail(reason string) {
	s.emit(agent.EventSessionFailed, map[string]any{"reason": reason})
	s.mu.Lock()
	s.err = errors.New("bench adapter: " + reason)
	s.mu.Unlock()
}

func (s *scriptSession) run(ctx context.Context, req agent.Request) {
	defer close(s.done)
	defer close(s.events)

	if !s.emit(agent.EventSessionStarted, map[string]any{"adapter": s.adapter.Name()}) {
		s.fail("cancelled")
		return
	}

	mode := strings.Fields(req.Instructions)
	switch {
	case len(mode) > 0 && mode[0] == modeFail:
		s.fail("forced failure injected by benchmark")
		return

	case len(mode) > 0 && mode[0] == modeHang:
		select {
		case <-ctx.Done():
		case <-s.stop:
		}
		s.fail("hang interrupted")
		return

	case len(mode) > 0 && mode[0] == modeCancel:
		if s.adapter.onStarted != nil {
			s.adapter.onStarted(req.Instructions)
		}

	case len(mode) > 0 && mode[0] == modeBarrier:
		if s.adapter.barrier != nil {
			if err := s.adapter.barrier.arrive(s.stop); err != nil {
				s.fail(err.Error())
				return
			}
		}
	}

	note := filepath.Join(req.WorkspaceDir, "BENCH_NOTES.md")
	body := fmt.Sprintf("# bench session\n\nworkspace: %s\ninstructions: %s\n",
		req.WorkspaceDir, req.Instructions)
	if err := os.WriteFile(note, []byte(body), 0o644); err != nil {
		s.fail("write note: " + err.Error())
		return
	}
	if !s.emit(agent.EventFileWritten, map[string]any{
		"path": "BENCH_NOTES.md", "workspace": req.WorkspaceDir,
	}) {
		s.fail("cancelled")
		return
	}

	summary := "bench session wrote BENCH_NOTES.md in " + req.WorkspaceDir
	if !s.emit(agent.EventSessionCompleted, map[string]any{"summary": summary}) {
		s.fail("cancelled")
		return
	}
	s.mu.Lock()
	s.result = agent.Result{Summary: summary, FilesChanged: []string{"BENCH_NOTES.md"}}
	s.mu.Unlock()
}
