package httpapi

import (
	"context"
	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

type TraceService interface {
	ListTaskSpans(ctx context.Context, taskID string) (observability.TaskTrace, error)
	ListTaskAttemptSpans(ctx context.Context, taskID string, attemptNumber int) (observability.TaskTrace, error)
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
	attempt, ok := queryAttempt(w, r)
	if !ok {
		return
	}
	if _, err := s.tasks.Get(r.Context(), id); err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	var trace observability.TaskTrace
	var err error
	if attempt == 0 {
		trace, err = s.traces.ListTaskSpans(r.Context(), id)
	} else {
		trace, err = s.traces.ListTaskAttemptSpans(r.Context(), id, attempt)
	}
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}
