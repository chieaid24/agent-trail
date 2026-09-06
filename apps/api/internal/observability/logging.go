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

const TraceIDHeader = "X-Trace-Id"

type ctxKey struct{}

// bounds inbound ids so a hostile header cannot inject log content
var validTraceID = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func NewLogger(w io.Writer, service string, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: renameDefaultKeys,
	})
	return slog.New(h).With("service", service)
}

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

func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on supported platforms; degrade loudly
		return "trace-id-unavailable"
	}
	return hex.EncodeToString(b[:])
}

func TraceIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// lets responsecontroller reach the flusher for sse
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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
