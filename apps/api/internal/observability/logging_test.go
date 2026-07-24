package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewTraceIDShape(t *testing.T) {
	id := NewTraceID()
	if len(id) != 32 {
		t.Fatalf("len = %d, want 32", len(id))
	}
	if id == NewTraceID() {
		t.Fatal("two ids collided")
	}
}

func TestMiddlewareGeneratesTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "test", slog.LevelInfo)

	var seen string
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = TraceIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if seen == "" {
		t.Fatal("no trace id in request context")
	}
	if got := rec.Header().Get(TraceIDHeader); got != seen {
		t.Errorf("response header %q != context id %q", got, seen)
	}

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	// Key names per docs/operations/observability.md.
	for _, key := range []string{"timestamp", "level", "service", "event", "trace_id", "message"} {
		if _, ok := line[key]; !ok {
			t.Errorf("log line missing %q: %s", key, buf.String())
		}
	}
	if line["trace_id"] != seen {
		t.Errorf("logged trace_id %v != %q", line["trace_id"], seen)
	}
}

func TestMiddlewareHonoursValidInboundID(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(TraceIDHeader, "abc12345-def6-7890")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(TraceIDHeader); got != "abc12345-def6-7890" {
		t.Errorf("valid inbound id replaced: %q", got)
	}
}

func TestMiddlewareReplacesInvalidInboundID(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	for _, bad := range []string{"short", "has spaces here", "line\nbreak0000", string(make([]byte, 100))} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(TraceIDHeader, bad)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get(TraceIDHeader); got == bad {
			t.Errorf("invalid inbound id %q was kept", bad)
		}
	}
}

func TestMiddlewareRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if line["status"] != float64(http.StatusTeapot) {
		t.Errorf("logged status = %v, want 418", line["status"])
	}
}
