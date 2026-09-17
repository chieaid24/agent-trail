package httpapi

import (
	"context"
	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/insights"
)

type InsightService interface {
	TaskInsights(ctx context.Context, taskID string) (insights.TaskInsights, error)
}

func WithInsights(service InsightService) Option {
	return func(s *Server) { s.insights = service }
}

func (s *Server) handleTaskInsights(w http.ResponseWriter, r *http.Request) {
	if s.insights == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	result, err := s.insights.TaskInsights(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
