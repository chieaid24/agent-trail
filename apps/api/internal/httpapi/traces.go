package httpapi

import (
	"context"
	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

type TraceService interface {
	ListTaskSpans(ctx context.Context, taskID string) (observability.TaskTrace, error)
}

func (s *Server) handleTaskTrace(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil || s.traces == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	if _, err := s.tasks.Get(r.Context(), id); err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	trace, err := s.traces.ListTaskSpans(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}
