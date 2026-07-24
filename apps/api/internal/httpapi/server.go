// Package httpapi wires the control-plane HTTP surface. Milestone 0 exposes
// health endpoints only; the task API lands with the task domain milestone.
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
	logger *slog.Logger
	db     DBPinger // nil when DATABASE_URL is not configured
}

var _ DBPinger = (*sql.DB)(nil)

// New returns a Server. db may be nil; readiness then reports the database
// as not configured instead of failing.
func New(logger *slog.Logger, db DBPinger) *Server {
	return &Server{logger: logger, db: db}
}

// Handler returns the routed HTTP handler with observability middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	return observability.Middleware(s.logger)(mux)
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
	// Encoding a map of strings cannot fail; ignore the error.
	_ = json.NewEncoder(w).Encode(body)
}
