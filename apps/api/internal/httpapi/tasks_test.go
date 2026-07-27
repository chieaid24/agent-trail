package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const testUUID = "3b241101-e2bb-4255-8caf-4136c566a962"

// fakeTasks records calls and returns canned results per method.
type fakeTasks struct {
	task   task.Task
	tasks  []task.Task
	events []task.Event
	err    error

	createParams task.CreateParams
	listParams   task.ListParams
	cancelID     string
	cancelReason string
	eventsLimit  int
}

func (f *fakeTasks) Create(_ context.Context, p task.CreateParams) (task.Task, error) {
	f.createParams = p
	return f.task, f.err
}

func (f *fakeTasks) Get(_ context.Context, id string) (task.Task, error) {
	return f.task, f.err
}

func (f *fakeTasks) List(_ context.Context, p task.ListParams) ([]task.Task, error) {
	f.listParams = p
	return f.tasks, f.err
}

func (f *fakeTasks) Cancel(_ context.Context, id, reason string) (task.Task, error) {
	f.cancelID, f.cancelReason = id, reason
	return f.task, f.err
}

func (f *fakeTasks) Events(_ context.Context, id string, limit int) ([]task.Event, error) {
	f.eventsLimit = limit
	return f.events, f.err
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTaskRoutesUnavailableWithoutDatabase(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil).Handler()
	for _, tc := range [][2]string{
		{http.MethodGet, "/api/v1/tasks"},
		{http.MethodPost, "/api/v1/tasks"},
		{http.MethodGet, "/api/v1/tasks/" + testUUID},
		{http.MethodPost, "/api/v1/tasks/" + testUUID + "/cancel"},
		{http.MethodGet, "/api/v1/tasks/" + testUUID + "/events"},
	} {
		rec := do(t, h, tc[0], tc[1], "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", tc[0], tc[1], rec.Code)
		}
	}
}

func TestCreateTask(t *testing.T) {
	f := &fakeTasks{task: task.Task{ID: testUUID, Status: task.StatusQueued}}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/tasks",
		`{"title": "  Fix the bug  ", "instructions": "do it", "priority": 5}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.createParams.Title != "Fix the bug" {
		t.Errorf("title = %q, want trimmed", f.createParams.Title)
	}
	if f.createParams.Priority != 5 {
		t.Errorf("priority = %d", f.createParams.Priority)
	}
	var got task.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != testUUID {
		t.Errorf("id = %q", got.ID)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	f := &fakeTasks{}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	long := strings.Repeat("x", 501)
	for name, body := range map[string]string{
		"empty body":      `{}`,
		"missing title":   `{"instructions": "y"}`,
		"blank title":     `{"title": "   ", "instructions": "y"}`,
		"long title":      `{"title": "` + long + `", "instructions": "y"}`,
		"missing instr":   `{"title": "x"}`,
		"bad priority":    `{"title": "x", "instructions": "y", "priority": 101}`,
		"bad base branch": `{"title": "x", "instructions": "y", "base_branch": "a b"}`,
		"bad runtime":     `{"title": "x", "instructions": "y", "max_runtime_seconds": 0}`,
		"negative cost":   `{"title": "x", "instructions": "y", "max_cost_usd": -1}`,
		"unknown field":   `{"title": "x", "instructions": "y", "bogus": 1}`,
		"malformed json":  `{"title":`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/tasks", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

func TestGetTask(t *testing.T) {
	f := &fakeTasks{task: task.Task{ID: testUUID}}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/tasks/not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id status = %d, want 400", rec.Code)
	}

	f.err = task.ErrNotFound
	rec = do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing task status = %d, want 404", rec.Code)
	}
}

func TestListTasks(t *testing.T) {
	f := &fakeTasks{tasks: []task.Task{{ID: testUUID}}}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks?status=queued&limit=10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if f.listParams.Status != task.StatusQueued || f.listParams.Limit != 10 {
		t.Errorf("params = %+v", f.listParams)
	}

	for _, q := range []string{"?status=bogus", "?limit=0", "?limit=201", "?limit=abc"} {
		rec := do(t, h, http.MethodGet, "/api/v1/tasks"+q, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestCancelTask(t *testing.T) {
	f := &fakeTasks{task: task.Task{ID: testUUID, Status: task.StatusCancelled}}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/tasks/"+testUUID+"/cancel",
		`{"reason": "no longer needed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.cancelID != testUUID || f.cancelReason != "no longer needed" {
		t.Errorf("cancel call = %q %q", f.cancelID, f.cancelReason)
	}

	// Empty body is allowed.
	rec = do(t, h, http.MethodPost, "/api/v1/tasks/"+testUUID+"/cancel", "")
	if rec.Code != http.StatusOK {
		t.Errorf("empty body status = %d, want 200", rec.Code)
	}

	f.err = &task.InvalidTransitionError{From: task.StatusCompleted, To: task.StatusCancelled}
	rec = do(t, h, http.MethodPost, "/api/v1/tasks/"+testUUID+"/cancel", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("terminal cancel status = %d, want 409", rec.Code)
	}
}

func TestTaskEvents(t *testing.T) {
	f := &fakeTasks{events: []task.Event{{ID: testUUID, EventType: "task.created"}}}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/events?limit=100", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if f.eventsLimit != 100 {
		t.Errorf("limit = %d", f.eventsLimit)
	}
	var body map[string][]task.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body["events"]) != 1 {
		t.Errorf("events = %v", body)
	}
}

func TestVersionConflictMapsTo409(t *testing.T) {
	f := &fakeTasks{err: &task.VersionConflictError{Expected: 1, Actual: 3}}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/tasks/"+testUUID+"/cancel", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestUnexpectedErrorMapsTo500(t *testing.T) {
	f := &fakeTasks{err: context.DeadlineExceeded}
	h := New(testLogger(), nil, f, nil, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal error") {
		t.Errorf("body leaks details: %s", rec.Body.String())
	}
}
