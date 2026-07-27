package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

func drain(t *testing.T, s Session) []Event {
	t.Helper()
	events := []Event{}
	for e := range s.Events() {
		events = append(events, e)
	}
	return events
}

func eventTypes(events []Event) []EventType {
	types := make([]EventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func TestFakeSessionHappyPath(t *testing.T) {
	dir := t.TempDir()
	fake := NewFake()
	if fake.Name() != "fake" {
		t.Fatalf("Name() = %q", fake.Name())
	}
	if err := fake.ValidateConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}

	sess, err := fake.Start(context.Background(), Request{
		WorkspaceDir: dir,
		Instructions: "Add refresh-token rotation.",
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drain(t, sess)
	want := []EventType{
		EventSessionStarted, EventPlan, EventFileWritten, EventFileWritten,
		EventToolRequested, EventToolStarted, EventToolOutput,
		EventToolCompleted, EventAssistantMessage, EventSessionCompleted,
	}
	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
	for _, e := range events {
		if e.Timestamp.IsZero() {
			t.Fatalf("event %s has zero timestamp", e.Type)
		}
		if !json.Valid(e.Payload) {
			t.Fatalf("event %s payload is not valid JSON", e.Type)
		}
	}

	result, err := sess.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary == "" {
		t.Fatal("empty summary")
	}
	if len(result.FilesChanged) != 2 || result.FilesChanged[0] != FixtureFile ||
		result.FilesChanged[1] != validation.FileName {
		t.Fatalf("FilesChanged = %v", result.FilesChanged)
	}

	content, err := os.ReadFile(filepath.Join(dir, FixtureFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Add refresh-token rotation.") {
		t.Fatalf("fixture missing instructions:\n%s", content)
	}

	// The written validation file must itself parse under the platform's
	// own limits, or every fake run would end in a config error.
	if _, found, err := validation.Load(dir); err != nil || !found {
		t.Fatalf("validation.Load = found %v, err %v", found, err)
	}
}

func TestFakeSessionAppendsOnSecondRun(t *testing.T) {
	dir := t.TempDir()
	fake := NewFake()
	for i := 0; i < 2; i++ {
		sess, err := fake.Start(context.Background(), Request{
			WorkspaceDir: dir, Instructions: "run twice",
		})
		if err != nil {
			t.Fatal(err)
		}
		drain(t, sess)
		if _, err := sess.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(dir, FixtureFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "## Fake agent run"); got != 2 {
		t.Fatalf("fixture has %d run headers, want 2:\n%s", got, content)
	}
}

func TestFakeSessionCancel(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewFake().Start(context.Background(), Request{
		WorkspaceDir: dir, Instructions: "cancel me",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Consume session_started so the producer is mid-flight, then cancel:
	// the unbuffered channel guarantees the next stopped() check sees it.
	first := <-sess.Events()
	if first.Type != EventSessionStarted {
		t.Fatalf("first event = %s, want %s", first.Type, EventSessionStarted)
	}
	if err := sess.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := drain(t, sess)
	last := events[len(events)-1]
	if last.Type != EventSessionFailed {
		t.Fatalf("last event = %s, want %s", last.Type, EventSessionFailed)
	}
	if _, err := sess.Wait(context.Background()); err == nil {
		t.Fatal("Wait() = nil error after cancel")
	}
}

func TestFakeStartRejectsBadWorkspace(t *testing.T) {
	if _, err := NewFake().Start(context.Background(), Request{}); err == nil {
		t.Fatal("Start with empty workspace dir succeeded")
	}
	if _, err := NewFake().Start(context.Background(), Request{
		WorkspaceDir: filepath.Join(t.TempDir(), "missing"),
	}); err == nil {
		t.Fatal("Start with missing workspace dir succeeded")
	}
}

func TestFakeSessionRejectsSend(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewFake().Start(context.Background(), Request{
		WorkspaceDir: dir, Instructions: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Send(context.Background(), "hello"); err == nil {
		t.Fatal("Send() = nil error")
	}
	drain(t, sess)
	if _, err := sess.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
