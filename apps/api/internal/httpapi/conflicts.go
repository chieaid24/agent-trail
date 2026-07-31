package httpapi

import (
	"context"

	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/conflict"
)

// ConflictService serves stored conflict warnings; implemented by
// *conflict.Store, faked in tests.
type ConflictService interface {
	ListForTask(ctx context.Context, taskID string) ([]conflict.TaskConflict, error)
}

// handleTaskConflicts lists the task's active overlap warnings
// (docs/architecture/conflict-detection.md). An empty list is the normal
// no-conflict state; pairs with a finished task are filtered by the store.
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
