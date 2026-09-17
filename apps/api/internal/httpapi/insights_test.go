package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/insights"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type fakeInsights struct {
	result insights.TaskInsights
	err    error
	taskID string
}

func (f *fakeInsights) TaskInsights(_ context.Context, taskID string) (insights.TaskInsights, error) {
	f.taskID = taskID
	return f.result, f.err
}

func TestTaskInsights(t *testing.T) {
	runtime := int64(12000)
	f := &fakeInsights{result: insights.TaskInsights{
		TaskID: testUUID, TaskStatus: "completed",
		Overall: insights.OverallInsight{AttemptCount: 1, TotalRuntimeMS: &runtime, EventCount: 9},
		Attempts: []insights.AttemptInsight{{
			AttemptID: testUUID, AttemptNumber: 1, Status: "completed",
			TotalRuntimeMS: &runtime, EventCount: 9,
			Validation: &insights.ValidationSummary{Total: 2, Passed: 2, Trusted: 2},
		}},
	}}
	h := New(testLogger(), nil, nil, nil, nil, nil, nil, WithInsights(f)).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/insights", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.taskID != testUUID {
		t.Fatalf("service received task %q", f.taskID)
	}
	var body insights.TaskInsights
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TaskStatus != "completed" || len(body.Attempts) != 1 ||
		body.Attempts[0].TotalRuntimeMS == nil || *body.Attempts[0].TotalRuntimeMS != 12000 ||
		body.Attempts[0].Validation == nil || body.Attempts[0].Validation.Passed != 2 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	// absent sources serialize as explicit null, not zero
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	attempt := raw["attempts"].([]any)[0].(map[string]any)
	for _, key := range []string{"queue_wait_ms", "provisioning_ms", "agent_session_ms",
		"validation_ms", "publishing_ms", "cost", "started_at", "failure_code"} {
		if value, ok := attempt[key]; !ok || value != nil {
			t.Errorf("attempt[%q] = %v, want explicit null", key, value)
		}
	}
}

func TestTaskInsightsErrors(t *testing.T) {
	unavailable := New(testLogger(), nil, nil, nil, nil, nil, nil).Handler()
	if rec := do(t, unavailable, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/insights", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no database = %d, want 503", rec.Code)
	}
	missing := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithInsights(&fakeInsights{err: task.ErrNotFound})).Handler()
	if rec := do(t, missing, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/insights", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown task = %d, want 404", rec.Code)
	}
	if rec := do(t, missing, http.MethodGet,
		"/api/v1/tasks/not-a-uuid/insights", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad uuid = %d, want 400", rec.Code)
	}
	failing := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithInsights(&fakeInsights{err: errors.New("read failed")})).Handler()
	if rec := do(t, failing, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/insights", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("store error = %d, want 500", rec.Code)
	}
}

func TestTaskInsightsPreservesAttemptStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attempts []insights.AttemptInsight
	}{
		{"empty", []insights.AttemptInsight{}},
		{"partial", []insights.AttemptInsight{{AttemptID: testUUID, AttemptNumber: 1, Status: "active"}}},
		{"failed and retried", []insights.AttemptInsight{{AttemptID: testUUID, AttemptNumber: 1, Status: "failed"}, {AttemptID: "00000000-0000-0000-0000-000000000002", AttemptNumber: 2, Status: "active"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeInsights{result: insights.TaskInsights{TaskID: testUUID, Attempts: tc.attempts}}
			h := New(testLogger(), nil, nil, nil, nil, nil, nil, WithInsights(f)).Handler()
			rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/insights", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var got insights.TaskInsights
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Attempts == nil || len(got.Attempts) != len(tc.attempts) {
				t.Fatalf("attempts = %+v", got.Attempts)
			}
			for i, a := range got.Attempts {
				if a.AttemptID != tc.attempts[i].AttemptID || a.Status != tc.attempts[i].Status || a.AttemptNumber != tc.attempts[i].AttemptNumber || a.TotalRuntimeMS != nil || a.Cost != nil || a.Validation != nil {
					t.Fatalf("attempt = %+v", a)
				}
			}
		})
	}
}
