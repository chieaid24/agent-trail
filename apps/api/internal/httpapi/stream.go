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

const streamBatchLimit = 500

const streamRetryMillis = 2000

// wire form "<attempt>:<sequence>", carried in sse id, echoed back as last-event-id
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

// terminal task + drained timeline emits "done" and closes; clients close on it, not reconnect
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
	// no-transform keeps proxies from gzip-buffering the stream
	w.Header().Set("Cache-Control", "no-cache, no-transform")
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
			done, err := json.Marshal(map[string]task.Status{"status": t.Status})
			if err == nil {
				_, _ = fmt.Fprintf(w, "event: done\ndata: %s\n\n", done)
				_ = rc.Flush()
			}
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

// json.marshal output has no raw newlines, so one data line is a valid sse frame
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
		return // client went away
	}
	s.logger.LogAttrs(r.Context(), slog.LevelError, msg,
		slog.String("event", "task_stream_failed"),
		slog.String("trace_id", observability.TraceIDFrom(r.Context())),
		slog.String("task_id", taskID),
		slog.String("error", err.Error()),
	)
}
