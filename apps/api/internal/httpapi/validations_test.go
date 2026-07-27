package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// fakeValidations returns canned validation results.
type fakeValidations struct {
	results []validation.StoredResult
	err     error
}

func (f *fakeValidations) ListForTask(_ context.Context, taskID string) ([]validation.StoredResult, error) {
	return f.results, f.err
}

// fakeEvidence returns a canned evidence report.
type fakeEvidence struct {
	stored evidence.Stored
	err    error
}

func (f *fakeEvidence) GetForTask(_ context.Context, taskID string) (evidence.Stored, error) {
	return f.stored, f.err
}

func TestValidationAndEvidenceRoutesUnavailableWithoutDatabase(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil).Handler()
	for _, path := range []string{
		"/api/v1/tasks/" + testUUID + "/validations",
		"/api/v1/tasks/" + testUUID + "/evidence",
	} {
		rec := do(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, rec.Code)
		}
	}
}

func TestTaskValidations(t *testing.T) {
	code := 1
	f := &fakeValidations{results: []validation.StoredResult{{
		ID: testUUID, Name: "unit", Category: "unit_test",
		Status: validation.StatusFailed, ExitCode: &code,
		TrustedExecution: true,
	}}}
	h := New(testLogger(), nil, nil, f, nil, nil, nil).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/validations", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Validations []validation.StoredResult `json:"validations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Validations) != 1 || body.Validations[0].Name != "unit" ||
		!body.Validations[0].TrustedExecution {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestTaskValidationsErrors(t *testing.T) {
	h := New(testLogger(), nil, nil,
		&fakeValidations{err: task.ErrNotFound}, nil, nil, nil).Handler()
	if rec := do(t, h, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/validations", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown task = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodGet,
		"/api/v1/tasks/not-a-uuid/validations", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad uuid = %d, want 400", rec.Code)
	}
}

func TestTaskEvidence(t *testing.T) {
	f := &fakeEvidence{stored: evidence.Stored{
		ID: testUUID, SchemaVersion: evidence.SchemaVersion,
		SummaryMarkdown: "# Evidence: t",
		Report:          json.RawMessage(`{"schema_version":1}`),
	}}
	h := New(testLogger(), nil, nil, nil, f, nil, nil).Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/evidence", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got evidence.Stored
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != evidence.SchemaVersion ||
		got.SummaryMarkdown != "# Evidence: t" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestTaskEvidenceErrors(t *testing.T) {
	noReport := New(testLogger(), nil, nil, nil,
		&fakeEvidence{err: evidence.ErrNoReport}, nil, nil).Handler()
	if rec := do(t, noReport, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/evidence", ""); rec.Code != http.StatusNotFound {
		t.Errorf("no report = %d, want 404", rec.Code)
	}
	unknown := New(testLogger(), nil, nil, nil,
		&fakeEvidence{err: task.ErrNotFound}, nil, nil).Handler()
	if rec := do(t, unknown, http.MethodGet,
		"/api/v1/tasks/"+testUUID+"/evidence", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown task = %d, want 404", rec.Code)
	}
}
