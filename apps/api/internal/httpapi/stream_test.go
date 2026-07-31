package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// streamServer returns a handler whose stream loop runs fast enough to
// finish inside a test.
func streamServer(f *fakeTasks) http.Handler {
	s := New(testLogger(), nil, f, nil, nil, nil, nil, nil)
	s.streamPollInterval = time.Millisecond
	s.streamHeartbeat = time.Millisecond
	return s.Handler()
}

func streamEvents(attempts ...[2]int64) []task.Event {
	events := make([]task.Event, len(attempts))
	for i, a := range attempts {
		events[i] = task.Event{
			ID:              testUUID,
			AttemptNumber:   int(a[0]),
			SequenceNumber:  a[1],
			EventType:       "agent.message",
			Source:          "agent",
			Payload:         json.RawMessage(`{}`),
			RedactionStatus: "none",
		}
	}
	return events
}

func TestTaskStreamReplaysAndCloses(t *testing.T) {
	f := &fakeTasks{
		task:   task.Task{ID: testUUID, Status: task.StatusCompleted},
		events: streamEvents([2]int64{1, 1}, [2]int64{1, 2}),
	}
	h := streamServer(f)

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"retry: 2000\n\n",
		"id: 1:1\n",
		"id: 1:2\n",
		`"event_type":"agent.message"`,
		"event: done\ndata: {\"status\":\"completed\"}\n\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestTaskStreamResumesFromLastEventID(t *testing.T) {
	f := &fakeTasks{
		task:   task.Task{ID: testUUID, Status: task.StatusCompleted},
		events: streamEvents([2]int64{1, 1}, [2]int64{1, 2}, [2]int64{2, 1}),
	}
	h := streamServer(f)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", nil)
	req.Header.Set("Last-Event-ID", "1:2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "id: 1:1\n") || strings.Contains(body, "id: 1:2\n") {
		t.Errorf("replayed events before the cursor:\n%s", body)
	}
	if !strings.Contains(body, "id: 2:1\n") {
		t.Errorf("missing event after the cursor:\n%s", body)
	}
}

func TestTaskStreamResumesFromQueryParam(t *testing.T) {
	f := &fakeTasks{
		task:   task.Task{ID: testUUID, Status: task.StatusCompleted},
		events: streamEvents([2]int64{1, 1}, [2]int64{1, 2}),
	}
	h := streamServer(f)

	rec := do(t, h, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/stream?last_event_id=1:1", "")
	body := rec.Body.String()
	if strings.Contains(body, "id: 1:1\n") {
		t.Errorf("replayed event before the cursor:\n%s", body)
	}
	if !strings.Contains(body, "id: 1:2\n") {
		t.Errorf("missing event after the cursor:\n%s", body)
	}
}

func TestTaskStreamRejectsMalformedCursor(t *testing.T) {
	f := &fakeTasks{task: task.Task{ID: testUUID, Status: task.StatusCompleted}}
	h := streamServer(f)

	for _, raw := range []string{"nonsense", "1", "1:x", "-1:2", "1:-2"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", nil)
		req.Header.Set("Last-Event-ID", raw)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("cursor %q: status = %d, want 400", raw, rec.Code)
		}
	}
}

func TestTaskStreamUnknownTask(t *testing.T) {
	f := &fakeTasks{err: task.ErrNotFound}
	h := streamServer(f)

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestTaskStreamHeartbeatsUntilClientCloses(t *testing.T) {
	// Running task with no new events: the stream must stay open and
	// heartbeat until the client disconnects.
	f := &fakeTasks{task: task.Task{ID: testUUID, Status: task.StatusExecuting}}
	h := streamServer(f)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))

	body := rec.Body.String()
	if !strings.Contains(body, ": heartbeat\n\n") {
		t.Errorf("no heartbeat written:\n%s", body)
	}
	if strings.Contains(body, "event: done") {
		t.Errorf("running task must not close with done:\n%s", body)
	}
}

func TestTaskStreamUnavailableWithoutDatabase(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil, nil).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
