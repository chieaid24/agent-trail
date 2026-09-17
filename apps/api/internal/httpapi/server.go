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

type DBPinger interface {
	PingContext(ctx context.Context) error
}

// nil deps degrade cleanly; each is nil when its config is absent
type Server struct {
	logger      *slog.Logger
	db          DBPinger
	tasks       TaskService
	validations ValidationService
	evidence    EvidenceService
	dashboard   DashboardService
	conflicts   ConflictService
	traces      TraceService
	insights    InsightService
	webhook     http.Handler
	metrics     http.Handler
	auth        AuthService

	authPublicOrigin string
	authCookieSecure bool

	streamPollInterval time.Duration
	streamHeartbeat    time.Duration
}

var _ DBPinger = (*sql.DB)(nil)

type Option func(*Server)

func WithDashboard(service DashboardService) Option {
	return func(s *Server) { s.dashboard = service }
}

func WithConflicts(service ConflictService) Option {
	return func(s *Server) { s.conflicts = service }
}

func WithTraces(service TraceService) Option {
	return func(s *Server) { s.traces = service }
}

func New(logger *slog.Logger, db DBPinger, tasks TaskService,
	validations ValidationService, ev EvidenceService,
	webhook, metrics http.Handler, options ...Option) *Server {
	s := &Server{
		logger: logger, db: db, tasks: tasks,
		validations: validations, evidence: ev,
		webhook: webhook, metrics: metrics,
		streamPollInterval: time.Second,
		streamHeartbeat:    15 * time.Second,
	}
	for _, option := range options {
		option(s)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	api.HandleFunc("POST /api/v1/tasks", s.handleCreateTask)
	api.HandleFunc("GET /api/v1/tasks/{taskId}", s.handleGetTask)
	api.HandleFunc("POST /api/v1/tasks/{taskId}/cancel", s.handleCancelTask)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/events", s.handleTaskEvents)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/stream", s.handleTaskStream)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/validations", s.handleTaskValidations)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/evidence", s.handleTaskEvidence)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/conflicts", s.handleTaskConflicts)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/trace", s.handleTaskTrace)
	api.HandleFunc("GET /api/v1/tasks/{taskId}/insights", s.handleTaskInsights)
	api.HandleFunc("GET /api/v1/organizations", s.handleListOrganizations)
	api.HandleFunc("GET /api/v1/organizations/{organizationId}", s.handleGetOrganization)
	api.HandleFunc("GET /api/v1/organizations/{organizationId}/repositories", s.handleOrganizationRepositories)
	api.HandleFunc("GET /api/v1/repositories", s.handleListRepositories)
	api.HandleFunc("GET /api/v1/repositories/{repositoryId}", s.handleGetRepository)
	api.HandleFunc("GET /api/v1/repositories/{repositoryId}/settings", s.handleRepositorySettings)
	api.HandleFunc("POST /api/v1/repositories/{repositoryId}/enable", s.handleRepositoryEnable)
	api.HandleFunc("POST /api/v1/repositories/{repositoryId}/disable", s.handleRepositoryDisable)
	api.HandleFunc("GET /api/v1/runners", s.handleListRunners)
	api.HandleFunc("GET /api/v1/runners/{runnerId}", s.handleGetRunner)

	var apiHandler http.Handler = api
	meHandler := http.Handler(http.HandlerFunc(s.handleMe))
	if s.auth != nil {
		apiHandler = s.requireSession(api)
		meHandler = s.requireSession(meHandler)
	} else {
		meHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.authAvailable(w)
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("/api/v1/", apiHandler)
	mux.HandleFunc("GET /auth/github/start", s.handleAuthStart)
	mux.HandleFunc("GET /auth/github/callback", s.handleAuthCallback)
	mux.HandleFunc("POST /auth/logout", s.handleAuthLogout)
	mux.Handle("GET /me", meHandler)
	mux.HandleFunc("POST /webhooks/github", s.handleWebhook)
	if s.metrics != nil {
		mux.Handle("GET /metrics", s.metrics)
	}
	return observability.Middleware(s.logger)(mux)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhook == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "github integration is not configured",
		})
		return
	}
	s.webhook.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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
	// bodies are maps and domain structs; encoding cannot fail
	_ = json.NewEncoder(w).Encode(body)
}
