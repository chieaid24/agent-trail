package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/dashboard"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// DashboardService serves the operator dashboard read models.
type DashboardService interface {
	ListOrganizations(ctx context.Context) ([]dashboard.Organization, error)
	GetOrganization(ctx context.Context, id string) (dashboard.Organization, error)
	ListRepositories(ctx context.Context, organizationID string, limit int) ([]dashboard.Repository, error)
	GetRepository(ctx context.Context, id string) (dashboard.RepositoryDetail, error)
	GetRepositorySettings(ctx context.Context, id string) (dashboard.RepositorySettings, error)
	ListRunners(ctx context.Context) ([]dashboard.Runner, error)
	GetRunner(ctx context.Context, id string) (dashboard.RunnerDetail, error)
}

func (s *Server) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	organizations, err := s.dashboard.ListOrganizations(r.Context())
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": organizations})
}

func (s *Server) handleGetOrganization(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	id, ok := pathUUID(w, r, "organizationId")
	if !ok {
		return
	}
	organization, err := s.dashboard.GetOrganization(r.Context(), id)
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, organization)
}

func (s *Server) handleOrganizationRepositories(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	id, ok := pathUUID(w, r, "organizationId")
	if !ok {
		return
	}
	if _, err := s.dashboard.GetOrganization(r.Context(), id); err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	limit, ok := queryLimit(w, r, 200)
	if !ok {
		return
	}
	repositories, err := s.dashboard.ListRepositories(r.Context(), id, limit)
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) handleListRepositories(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	limit, ok := queryLimit(w, r, 200)
	if !ok {
		return
	}
	repositories, err := s.dashboard.ListRepositories(r.Context(), "", limit)
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) handleGetRepository(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	id, ok := pathUUID(w, r, "repositoryId")
	if !ok {
		return
	}
	repository, err := s.dashboard.GetRepository(r.Context(), id)
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, repository)
}

func (s *Server) handleRepositorySettings(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	id, ok := pathUUID(w, r, "repositoryId")
	if !ok {
		return
	}
	settings, err := s.dashboard.GetRepositorySettings(r.Context(), id)
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleListRunners(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	runners, err := s.dashboard.ListRunners(r.Context())
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runners": runners})
}

func (s *Server) handleGetRunner(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAvailable(w) {
		return
	}
	id, ok := pathUUID(w, r, "runnerId")
	if !ok {
		return
	}
	runner, err := s.dashboard.GetRunner(r.Context(), id)
	if err != nil {
		s.writeDashboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runner)
}

func (s *Server) dashboardAvailable(w http.ResponseWriter) bool {
	if s.dashboard != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable,
		"dashboard API unavailable: database not configured")
	return false
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if !task.IsUUID(id) {
		writeError(w, http.StatusBadRequest, name+" must be a UUID")
		return "", false
	}
	return id, true
}

func (s *Server) writeDashboardError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, dashboard.ErrOrganizationNotFound):
		writeError(w, http.StatusNotFound, "organization not found")
	case errors.Is(err, dashboard.ErrRepositoryNotFound):
		writeError(w, http.StatusNotFound, "repository not found")
	case errors.Is(err, dashboard.ErrRunnerNotFound):
		writeError(w, http.StatusNotFound, "runner not found")
	default:
		s.logger.LogAttrs(r.Context(), slog.LevelError, "dashboard request failed",
			slog.String("event", "dashboard_request_failed"),
			slog.String("trace_id", observability.TraceIDFrom(r.Context())),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
