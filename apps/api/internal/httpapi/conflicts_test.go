package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type fakeConflicts struct {
	conflicts []conflict.TaskConflict
	err       error
}

func (f *fakeConflicts) ListForTask(context.Context, string) ([]conflict.TaskConflict, error) {
	return f.conflicts, f.err
}

func TestTaskConflictsUnavailableWithoutDB(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/conflicts", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestTaskConflictsRejectsBadID(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithConflicts(&fakeConflicts{})).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/not-a-uuid/conflicts", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTaskConflictsList(t *testing.T) {
	f := &fakeConflicts{conflicts: []conflict.TaskConflict{{
		ID: "c-1", OtherTaskID: "t-2", OtherTaskTitle: "sibling task",
		Kinds:      []conflict.Kind{conflict.KindFileOverlap, conflict.KindMergeConflict},
		Files:      []string{"app.go"},
		DetectedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}}}
	h := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithConflicts(f)).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/conflicts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Conflicts []struct {
			OtherTaskID    string   `json:"other_task_id"`
			OtherTaskTitle string   `json:"other_task_title"`
			Kinds          []string `json:"kinds"`
			Files          []string `json:"files"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want 1", body.Conflicts)
	}
	got := body.Conflicts[0]
	if got.OtherTaskID != "t-2" || got.OtherTaskTitle != "sibling task" ||
		len(got.Kinds) != 2 || got.Kinds[0] != "file_overlap" ||
		len(got.Files) != 1 || got.Files[0] != "app.go" {
		t.Fatalf("conflict = %+v", got)
	}
}

func TestTaskConflictsEmptyList(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithConflicts(&fakeConflicts{conflicts: []conflict.TaskConflict{}})).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/conflicts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "{\"conflicts\":[]}\n" {
		t.Fatalf("body = %q, want empty conflicts array", body)
	}
}

func TestTaskConflictsUnknownTask(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithConflicts(&fakeConflicts{err: task.ErrNotFound})).Handler()
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/conflicts", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
