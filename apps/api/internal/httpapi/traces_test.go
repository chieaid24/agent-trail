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
}

func (f fakeTraces) ListTaskSpans(context.Context, string) (observability.TaskTrace, error) {
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
