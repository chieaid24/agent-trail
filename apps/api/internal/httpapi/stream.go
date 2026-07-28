package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// streamBatchLimit bounds one EventsAfter fetch inside the stream loop.
const streamBatchLimit = 500

// streamRetryMillis is the reconnect delay hint sent to EventSource clients.
const streamRetryMillis = 2000

// streamCursor is the resume position of an SSE client: events strictly
// after (attempt, sequence) have not been delivered. The wire form is
// "<attempt>:<sequence>", carried in the SSE id field and echoed back by
// browsers as Last-Event-ID on reconnect.
type streamCursor struct {
	attempt  int
	sequence int64
}

func (c streamCursor) String() string {
	return fmt.Sprintf("%d:%d", c.attempt, c.sequence)
}

func parseStreamCursor(raw string) (streamCursor, error) {
	attemptPart, seqPart, ok := strings.Cut(raw, ":")
	if !ok {
		return streamCursor{}, errors.New("cursor must be <attempt>:<sequence>")
	}
	attempt, err := strconv.Atoi(attemptPart)
	if err != nil || attempt < 0 {
		return streamCursor{}, errors.New("cursor attempt must be a non-negative integer")
	}
	seq, err := strconv.ParseInt(seqPart, 10, 64)
	if err != nil || seq < 0 {
		return streamCursor{}, errors.New("cursor sequence must be a non-negative integer")
	}
	return streamCursor{attempt: attempt, sequence: seq}, nil
}

// requestCursor reads the client's resume position: the Last-Event-ID header
// (set by EventSource on automatic reconnect) wins over the last_event_id
// query parameter (for callers that cannot set headers on the first request).
func requestCursor(r *http.Request) (streamCursor, error) {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	if raw == "" {
		return streamCursor{}, nil
	}
	c, err := parseStreamCursor(raw)
	if err != nil {
		return streamCursor{}, fmt.Errorf("invalid Last-Event-ID: %w", err)
	}
	return c, nil
}

// handleTaskStream serves GET /api/v1/tasks/{taskId}/stream: the task's
// activity timeline as server-sent events (docs/architecture/api.md). Each
// event's data is the same JSON object GET /events returns; the SSE id field
// carries the resume cursor. When the task is terminal and the timeline is
// drained the server emits a "done" event and closes; clients should close
// on "done" rather than reconnect.
func (s *Server) handleTaskStream(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		s.writeTasksUnavailable(w)
		return
	}
	id, ok := pathTaskID(w, r)
	if !ok {
		return
	}
	cursor, err := requestCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.tasks.Get(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", streamRetryMillis); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		s.logStreamEnd(r, id, "flush unsupported", err)
		return
	}

	poll := time.NewTicker(s.streamPollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(s.streamHeartbeat)
	defer heartbeat.Stop()

	for {
		wrote, err := s.streamDrain(w, rc, r, id, &cursor)
		if err != nil {
			s.logStreamEnd(r, id, "stream drain failed", err)
			return
		}
		if t.Status.Terminal() && !wrote {
			// Drained a finished task: tell the client to stop reconnecting.
			_, _ = fmt.Fprintf(w, "event: done\ndata: {\"status\":%q}\n\n", t.Status)
			_ = rc.Flush()
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case <-poll.C:
		}

		t, err = s.tasks.Get(r.Context(), id)
		if err != nil {
			s.logStreamEnd(r, id, "stream task refresh failed", err)
			return
		}
	}
}

// streamDrain writes every undelivered event and advances the cursor.
// It reports whether anything was written.
func (s *Server) streamDrain(w http.ResponseWriter, rc *http.ResponseController,
	r *http.Request, id string, cursor *streamCursor) (bool, error) {
	wrote := false
	for {
		events, err := s.tasks.EventsAfter(r.Context(), id,
			cursor.attempt, cursor.sequence, streamBatchLimit)
		if err != nil {
			return wrote, err
		}
		for _, e := range events {
			if err := writeSSEEvent(w, e); err != nil {
				return wrote, err
			}
			*cursor = streamCursor{attempt: e.AttemptNumber, sequence: e.SequenceNumber}
			wrote = true
		}
		if wrote {
			if err := rc.Flush(); err != nil {
				return wrote, err
			}
		}
		if len(events) < streamBatchLimit {
			return wrote, nil
		}
	}
}

// writeSSEEvent frames one timeline event. json.Marshal output never contains
// raw newlines, so a single data line is always a valid SSE frame.
func writeSSEEvent(w http.ResponseWriter, e task.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d:%d\ndata: %s\n\n",
		e.AttemptNumber, e.SequenceNumber, data)
	return err
}

func (s *Server) logStreamEnd(r *http.Request, taskID, msg string, err error) {
	if errors.Is(err, r.Context().Err()) {
		return // client went away; nothing to report
	}
	s.logger.LogAttrs(r.Context(), slog.LevelError, msg,
		slog.String("event", "task_stream_failed"),
		slog.String("trace_id", observability.TraceIDFrom(r.Context())),
		slog.String("task_id", taskID),
		slog.String("error", err.Error()),
	)
}
