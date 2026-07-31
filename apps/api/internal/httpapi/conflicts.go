package httpapi

import (
	"context"
	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
)

// ConflictService reads stored warnings.
type ConflictService interface {
	ListForTask(ctx context.Context, taskID string) ([]conflict.TaskConflict, error)
}

func (s *Server) handleTaskConflicts(w http.ResponseWriter, r *http.Request) {
	if s.conflicts == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	conflicts, err := s.conflicts.ListForTask(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}
