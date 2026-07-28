package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// TaskService is the slice of the task domain the HTTP API consumes;
// implemented by *task.Store, faked in tests.
type TaskService interface {
	Create(ctx context.Context, p task.CreateParams) (task.Task, error)
	Get(ctx context.Context, id string) (task.Task, error)
	List(ctx context.Context, p task.ListParams) ([]task.Task, error)
	Cancel(ctx context.Context, id, reason string) (task.Task, error)
	Events(ctx context.Context, id string, limit int) ([]task.Event, error)
	EventsAfter(ctx context.Context, id string, afterAttempt int, afterSequence int64, limit int) ([]task.Event, error)
}

// ValidationService serves trusted validation results; implemented by
// *validation.Store, faked in tests.
type ValidationService interface {
	ListForTask(ctx context.Context, taskID string) ([]validation.StoredResult, error)
}

// EvidenceService serves evidence reports; implemented by *evidence.Store,
// faked in tests.
type EvidenceService interface {
	GetForTask(ctx context.Context, taskID string) (evidence.Stored, error)
}

const maxBodyBytes = 1 << 20 // 1 MiB request cap

// createTaskRequest is the POST /api/v1/tasks body.
type createTaskRequest struct {
	Title             string   `json:"title"`
	Instructions      string   `json:"instructions"`
	Priority          int      `json:"priority"`
	BaseBranch        string   `json:"base_branch"`
	MaxRuntimeSeconds *int     `json:"max_runtime_seconds"`
	MaxCostUSD        *float64 `json:"max_cost_usd"`
}

// cancelTaskRequest is the POST /api/v1/tasks/{taskId}/cancel body (optional).
type cancelTaskRequest struct {
	Reason string `json:"reason"`
}

func (r createTaskRequest) validate() error {
	if n := len(strings.TrimSpace(r.Title)); n == 0 || len(r.Title) > 500 {
		return errors.New("title is required and must be at most 500 characters")
	}
	if n := len(strings.TrimSpace(r.Instructions)); n == 0 || len(r.Instructions) > 100000 {
		return errors.New("instructions are required and must be at most 100000 characters")
	}
	if r.Priority < -100 || r.Priority > 100 {
		return errors.New("priority must be between -100 and 100")
	}
	if len(r.BaseBranch) > 255 || strings.ContainsAny(r.BaseBranch, " \t\n") {
		return errors.New("base_branch must be at most 255 characters with no whitespace")
	}
	if r.MaxRuntimeSeconds != nil && (*r.MaxRuntimeSeconds < 1 || *r.MaxRuntimeSeconds > 86400) {
		return errors.New("max_runtime_seconds must be between 1 and 86400")
	}
	if r.MaxCostUSD != nil && (*r.MaxCostUSD < 0 || *r.MaxCostUSD > 100000) {
		return errors.New("max_cost_usd must be between 0 and 100000")
	}
	return nil
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		s.writeTasksUnavailable(w)
		return
	}
	var req createTaskRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	t, err := s.tasks.Create(r.Context(), task.CreateParams{
		Title:             strings.TrimSpace(req.Title),
		Instructions:      req.Instructions,
		Priority:          req.Priority,
		BaseBranch:        req.BaseBranch,
		MaxRuntimeSeconds: req.MaxRuntimeSeconds,
		MaxCostUSD:        req.MaxCostUSD,
	})
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	s.logger.LogAttrs(r.Context(), slog.LevelInfo, "task created",
		slog.String("event", "task_created"),
		slog.String("trace_id", observability.TraceIDFrom(r.Context())),
		slog.String("task_id", t.ID),
	)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		s.writeTasksUnavailable(w)
		return
	}
	p := task.ListParams{}
	if v := r.URL.Query().Get("status"); v != "" {
		status := task.Status(v)
		if !status.Valid() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown status %q", v))
			return
		}
		p.Status = status
	}
	limit, ok := queryLimit(w, r, 200)
	if !ok {
		return
	}
	p.Limit = limit

	tasks, err := s.tasks.List(r.Context(), p)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	t, err := s.tasks.Get(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	var req cancelTaskRequest
	// The cancel body is optional; an empty body means no reason.
	if r.ContentLength != 0 && !s.decodeJSON(w, r, &req) {
		return
	}
	if len(req.Reason) > 1000 {
		writeError(w, http.StatusBadRequest, "reason must be at most 1000 characters")
		return
	}

	t, err := s.tasks.Cancel(r.Context(), id, req.Reason)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	// "requested", not "cancelled": the call is idempotent and may be a no-op
	// on an already-cancelled task; the activity timeline holds the truth.
	s.logger.LogAttrs(r.Context(), slog.LevelInfo, "task cancel requested",
		slog.String("event", "task_cancel_requested"),
		slog.String("trace_id", observability.TraceIDFrom(r.Context())),
		slog.String("task_id", t.ID),
	)
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r, 1000)
	if !ok {
		return
	}
	events, err := s.tasks.Events(r.Context(), id, limit)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleTaskValidations(w http.ResponseWriter, r *http.Request) {
	if s.validations == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	results, err := s.validations.ListForTask(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"validations": results})
}

func (s *Server) handleTaskEvidence(w http.ResponseWriter, r *http.Request) {
	if s.evidence == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	st, err := s.evidence.GetForTask(r.Context(), id)
	if errors.Is(err, evidence.ErrNoReport) {
		writeError(w, http.StatusNotFound, "no evidence report for task")
		return
	}
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// pathTaskID validates the {taskId} path segment as a UUID.
func pathTaskID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("taskId")
	if !task.IsUUID(id) {
		writeError(w, http.StatusBadRequest, "taskId must be a UUID")
		return "", false
	}
	return id, true
}

// queryLimit parses an optional positive ?limit= bounded by maxLimit.
func queryLimit(w http.ResponseWriter, r *http.Request, maxLimit int) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxLimit {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("limit must be an integer between 1 and %d", maxLimit))
		return 0, false
	}
	return n, true
}

// decodeJSON strictly decodes a bounded JSON body; on failure it writes a 400
// and returns false.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// writeTaskError maps domain errors to HTTP statuses.
func (s *Server) writeTaskError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *task.InvalidTransitionError
	var conflict *task.VersionConflictError
	switch {
	case errors.Is(err, task.ErrNotFound):
		writeError(w, http.StatusNotFound, "task not found")
	case errors.As(err, &invalid), errors.As(err, &conflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.logger.LogAttrs(r.Context(), slog.LevelError, "task request failed",
			slog.String("event", "task_request_failed"),
			slog.String("trace_id", observability.TraceIDFrom(r.Context())),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) writeTasksUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable,
		"task API unavailable: database not configured")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
