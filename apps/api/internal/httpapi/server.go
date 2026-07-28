// Package httpapi wires the control-plane HTTP surface: health endpoints and
// the /api/v1 task API (docs/architecture/api.md).
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// DBPinger is the slice of *sql.DB readiness needs; narrowed for tests.
type DBPinger interface {
	PingContext(ctx context.Context) error
}

// Server holds the HTTP API dependencies.
type Server struct {
	logger      *slog.Logger
	db          DBPinger          // nil when DATABASE_URL is not configured
	tasks       TaskService       // nil when DATABASE_URL is not configured
	validations ValidationService // nil when DATABASE_URL is not configured
	evidence    EvidenceService   // nil when DATABASE_URL is not configured
	webhook     http.Handler      // nil when the GitHub integration is not configured
	metrics     http.Handler      // nil disables GET /metrics

	// SSE stream cadence; defaulted in New, shortened in tests.
	streamPollInterval time.Duration
	streamHeartbeat    time.Duration
}

var _ DBPinger = (*sql.DB)(nil)

// New returns a Server. Nil dependencies degrade cleanly: readiness reports
// the database as not configured, and the task API and webhook answer 503.
func New(logger *slog.Logger, db DBPinger, tasks TaskService,
	validations ValidationService, ev EvidenceService,
	webhook, metrics http.Handler) *Server {
	return &Server{
		logger: logger, db: db, tasks: tasks,
		validations: validations, evidence: ev,
		webhook: webhook, metrics: metrics,
		streamPollInterval: time.Second,
		streamHeartbeat:    15 * time.Second,
	}
}

// Handler returns the routed HTTP handler with observability middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/tasks/{taskId}", s.handleGetTask)
	mux.HandleFunc("POST /api/v1/tasks/{taskId}/cancel", s.handleCancelTask)
	mux.HandleFunc("GET /api/v1/tasks/{taskId}/events", s.handleTaskEvents)
	mux.HandleFunc("GET /api/v1/tasks/{taskId}/stream", s.handleTaskStream)
	mux.HandleFunc("GET /api/v1/tasks/{taskId}/validations", s.handleTaskValidations)
	mux.HandleFunc("GET /api/v1/tasks/{taskId}/evidence", s.handleTaskEvidence)
	mux.HandleFunc("POST /webhooks/github", s.handleWebhook)
	if s.metrics != nil {
		mux.Handle("GET /metrics", s.metrics)
	}
	return observability.Middleware(s.logger)(mux)
}

// handleWebhook forwards to the GitHub webhook handler, or reports the
// integration unconfigured.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhook == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "github integration is not configured",
		})
		return
	}
	s.webhook.ServeHTTP(w, r)
}

// handleHealthz reports process liveness only.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz reports readiness to serve: the database must answer a ping
// when one is configured.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":   "ok",
			"database": "not_configured",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		s.logger.LogAttrs(r.Context(), slog.LevelError, "database ping failed",
			slog.String("event", "readyz_db_ping_failed"),
			slog.String("trace_id", observability.TraceIDFrom(r.Context())),
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "unavailable",
			"database": "unreachable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"database": "ok",
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Bodies are maps and domain structs; encoding them cannot fail.
	_ = json.NewEncoder(w).Encode(body)
}
