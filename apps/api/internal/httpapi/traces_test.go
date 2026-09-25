package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type fakeTraces struct {
	trace observability.TaskTrace
	err   error

	attemptNumber *int
}

func (f fakeTraces) ListTaskSpans(context.Context, string) (observability.TaskTrace, error) {
	return f.trace, f.err
}

func (f fakeTraces) ListTaskAttemptSpans(_ context.Context, _ string, attemptNumber int) (observability.TaskTrace, error) {
	if f.attemptNumber != nil {
		*f.attemptNumber = attemptNumber
	}
	return f.trace, f.err
}

func TestTaskTrace(t *testing.T) {
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	tasks := &fakeTasks{task: task.Task{ID: testUUID}}
	traces := fakeTraces{trace: observability.TaskTrace{
		Spans: []observability.TaskSpan{{
			TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Name: "task.execute", StartTime: start, EndTime: start.Add(time.Second),
		}},
	}}
	h := New(testLogger(), nil, tasks, nil, nil, nil, nil, WithTraces(traces)).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body observability.TaskTrace
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Spans) != 1 || body.Spans[0].Name != "task.execute" {
		t.Fatalf("body = %+v", body)
	}
}

func TestTaskTraceMapsMissingTask(t *testing.T) {
	tasks := &fakeTasks{err: task.ErrNotFound}
	h := New(testLogger(), nil, tasks, nil, nil, nil, nil,
		WithTraces(fakeTraces{})).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTaskTraceMapsStoreError(t *testing.T) {
	tasks := &fakeTasks{task: task.Task{ID: testUUID}}
	h := New(testLogger(), nil, tasks, nil, nil, nil, nil,
		WithTraces(fakeTraces{err: errors.New("read failed")})).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTaskTraceAttemptFilter(t *testing.T) {
	tasks := &fakeTasks{task: task.Task{ID: testUUID}}
	var filtered int
	traces := fakeTraces{attemptNumber: &filtered, trace: observability.TaskTrace{
		Spans: []observability.TaskSpan{{Name: "runner.attempt"}},
	}}
	h := New(testLogger(), nil, tasks, nil, nil, nil, nil, WithTraces(traces)).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace?attempt=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if filtered != 2 {
		t.Fatalf("attempt filter = %d, want 2", filtered)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace?attempt=0", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("attempt=0 = %d, want 400", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace?attempt=3000000000", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("attempt above int32 = %d, want 400", rec.Code)
	}

	missing := New(testLogger(), nil, tasks, nil, nil, nil, nil,
		WithTraces(fakeTraces{err: task.ErrAttemptNotFound})).Handler()
	rec = do(t, missing, http.MethodGet, "/api/v1/tasks/"+testUUID+"/trace?attempt=9", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown attempt = %d, body = %s", rec.Code, rec.Body.String())
	}
}
