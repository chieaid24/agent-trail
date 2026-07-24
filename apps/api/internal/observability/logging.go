// Package observability provides structured JSON logging and request
// correlation for the control plane. Field names follow
// docs/operations/observability.md (timestamp, service, level, message);
// request-scoped lines carry trace_id, the correlation id until
// OpenTelemetry tracing lands. Handlers add task_id on task-context lines;
// task_attempt_id and runner_id join when the scheduler and runners start
// logging attempt-scoped work (the activity timeline carries attempt
// context until then).
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

// TraceIDHeader is the inbound/outbound correlation header.
const TraceIDHeader = "X-Trace-Id"

type ctxKey struct{}

// validTraceID bounds accepted inbound ids to alphanumerics plus dash, so a
// hostile header cannot inject log content or unbounded data.
var validTraceID = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// NewLogger returns a JSON slog.Logger writing to w, tagged with the service
// name. Keys are renamed to the observability doc's names (timestamp, message).
func NewLogger(w io.Writer, service string, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: renameDefaultKeys,
	})
	return slog.New(h).With("service", service)
}

// renameDefaultKeys maps slog's built-in keys to the names required by
// docs/operations/observability.md.
func renameDefaultKeys(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			a.Key = "timestamp"
		case slog.MessageKey:
			a.Key = "message"
		}
	}
	return a
}

// NewTraceID returns a 32-char hex correlation id.
func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on supported platforms; degrade loudly.
		return "trace-id-unavailable"
	}
	return hex.EncodeToString(b[:])
}

// TraceIDFrom returns the correlation id stored in ctx, or "" if absent.
func TraceIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// WithTraceID returns a child context carrying the correlation id.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// statusRecorder captures the response status for the request log line.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Middleware assigns each request a correlation id (honouring a valid inbound
// X-Trace-Id), echoes it on the response, and logs one structured line per
// request.
func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(TraceIDHeader)
			if !validTraceID.MatchString(id) {
				id = NewTraceID()
			}
			ctx := WithTraceID(r.Context(), id)
			w.Header().Set(TraceIDHeader, id)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(ctx))

			logger.LogAttrs(ctx, slog.LevelInfo, "http request",
				slog.String("event", "http_request"),
				slog.String("trace_id", id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}
