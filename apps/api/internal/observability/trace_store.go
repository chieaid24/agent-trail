package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type TaskSpan struct {
	TraceID       string         `json:"trace_id"`
	SpanID        string         `json:"span_id"`
	ParentSpanID  *string        `json:"parent_span_id"`
	TaskAttemptID *string        `json:"task_attempt_id"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	StartTime     time.Time      `json:"start_time"`
	EndTime       time.Time      `json:"end_time"`
	Attributes    map[string]any `json:"attributes"`
	StatusCode    string         `json:"status_code"`
	StatusMessage string         `json:"status_message"`
}

type TaskTrace struct {
	Spans []TaskSpan `json:"spans"`
}

type TraceStore struct {
	db *sql.DB
}

func NewTraceStore(db *sql.DB) *TraceStore {
	return &TraceStore{db: db}
}

// task-scoped spans only; process-level spans ignored
func (s *TraceStore) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin span export: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, span := range spans {
		attributes := make(map[string]any, len(span.Attributes()))
		for _, value := range span.Attributes() {
			attributes[string(value.Key)] = value.Value.AsInterface()
		}
		taskID, ok := attributes["task.id"].(string)
		if !ok || taskID == "" {
			continue
		}
		var attemptID *string
		if value, ok := attributes["task.attempt_id"].(string); ok && value != "" {
			attemptID = &value
		}
		raw, err := json.Marshal(attributes)
		if err != nil {
			return fmt.Errorf("marshal span attributes: %w", err)
		}
		var parentID *string
		if span.Parent().SpanID().IsValid() {
			value := span.Parent().SpanID().String()
			parentID = &value
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO task_spans (
				task_id, task_attempt_id, trace_id, span_id, parent_span_id,
				name, kind, start_time, end_time, attributes_json,
				status_code, status_message
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (task_id, trace_id, span_id) DO UPDATE SET
				task_attempt_id = EXCLUDED.task_attempt_id,
				parent_span_id = EXCLUDED.parent_span_id,
				name = EXCLUDED.name,
				kind = EXCLUDED.kind,
				start_time = EXCLUDED.start_time,
				end_time = EXCLUDED.end_time,
				attributes_json = EXCLUDED.attributes_json,
				status_code = EXCLUDED.status_code,
				status_message = EXCLUDED.status_message`,
			taskID, attemptID, span.SpanContext().TraceID().String(),
			span.SpanContext().SpanID().String(), parentID, span.Name(),
			span.SpanKind().String(), span.StartTime(), span.EndTime(), raw,
			span.Status().Code.String(), span.Status().Description)
		if err != nil {
			return fmt.Errorf("store span %s: %w", span.Name(), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit span export: %w", err)
	}
	return nil
}

// db is owned by main
func (s *TraceStore) Shutdown(context.Context) error { return nil }

func (s *TraceStore) ListTaskSpans(ctx context.Context, taskID string) (TaskTrace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT trace_id, span_id, parent_span_id, task_attempt_id, name, kind,
			start_time, end_time, attributes_json, status_code, status_message
		FROM task_spans
		WHERE task_id = $1
		ORDER BY start_time, trace_id, span_id`, taskID)
	if err != nil {
		return TaskTrace{}, fmt.Errorf("list task spans: %w", err)
	}
	defer rows.Close()

	trace := TaskTrace{Spans: []TaskSpan{}}
	for rows.Next() {
		var span TaskSpan
		var parentID, attemptID sql.NullString
		var raw []byte
		if err := rows.Scan(&span.TraceID, &span.SpanID, &parentID, &attemptID,
			&span.Name, &span.Kind, &span.StartTime, &span.EndTime, &raw,
			&span.StatusCode, &span.StatusMessage); err != nil {
			return TaskTrace{}, fmt.Errorf("scan task span: %w", err)
		}
		if parentID.Valid {
			span.ParentSpanID = &parentID.String
		}
		if attemptID.Valid {
			span.TaskAttemptID = &attemptID.String
		}
		if err := json.Unmarshal(raw, &span.Attributes); err != nil {
			return TaskTrace{}, fmt.Errorf("decode task span attributes: %w", err)
		}
		trace.Spans = append(trace.Spans, span)
	}
	if err := rows.Err(); err != nil {
		return TaskTrace{}, fmt.Errorf("list task spans: %w", err)
	}
	return trace, nil
}

var _ sdktrace.SpanExporter = (*TraceStore)(nil)
