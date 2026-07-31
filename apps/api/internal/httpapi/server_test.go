package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

type fakePinger struct{ err error }

func (f fakePinger) PingContext(context.Context) error { return f.err }

func get(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	return rec, body
}

func TestHealthz(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil, nil).Handler()
	rec, body := get(t, h, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestReadyzWithoutDatabase(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil, nil).Handler()
	rec, body := get(t, h, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["database"] != "not_configured" {
		t.Errorf("body = %v", body)
	}
}

func TestReadyzWithHealthyDatabase(t *testing.T) {
	h := New(testLogger(), fakePinger{}, nil, nil, nil, nil, nil, nil).Handler()
	rec, body := get(t, h, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["database"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestReadyzWithUnreachableDatabase(t *testing.T) {
	h := New(testLogger(), fakePinger{err: errors.New("connection refused")}, nil, nil, nil, nil, nil, nil).Handler()
	rec, body := get(t, h, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if body["database"] != "unreachable" {
		t.Errorf("body = %v", body)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil, nil).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
