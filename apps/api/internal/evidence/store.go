package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// ErrNoReport marks a task that has no evidence report yet.
var ErrNoReport = errors.New("no evidence report")

// Stored is one evidence_reports row; JSON tags are the API wire shape.
type Stored struct {
	ID              string          `json:"id"`
	TaskAttemptID   string          `json:"task_attempt_id"`
	AttemptNumber   int             `json:"attempt_number"`
	SchemaVersion   int             `json:"schema_version"`
	SummaryMarkdown string          `json:"summary_markdown"`
	Report          json.RawMessage `json:"report"`
	CreatedAt       time.Time       `json:"created_at"`
}

// Store persists evidence reports.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Insert records the attempt's report. An attempt has exactly one report:
// a replayed generation (recovered owner) is a no-op and the first wins.
func (s *Store) Insert(ctx context.Context, attemptID string, r Report, markdown string) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO evidence_reports (task_attempt_id, schema_version,
			summary_markdown, report_json)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (task_attempt_id) DO NOTHING`,
		attemptID, r.SchemaVersion, markdown, body)
	if err != nil {
		return fmt.Errorf("insert evidence report: %w", err)
	}
	return nil
}

// GetForTask returns the task's newest report (the latest attempt's).
// Unknown tasks return task.ErrNotFound; a task without a report returns
// ErrNoReport.
func (s *Store) GetForTask(ctx context.Context, taskID string) (Stored, error) {
	if !task.IsUUID(taskID) {
		return Stored{}, task.ErrNotFound
	}
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, taskID).Scan(&exists)
	if err != nil {
		return Stored{}, fmt.Errorf("check task: %w", err)
	}
	if !exists {
		return Stored{}, task.ErrNotFound
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT e.id, e.task_attempt_id, a.attempt_number, e.schema_version,
			e.summary_markdown, e.report_json, e.created_at
		FROM evidence_reports e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1
		ORDER BY a.attempt_number DESC
		LIMIT 1`, taskID)
	var st Stored
	var report []byte
	err = row.Scan(&st.ID, &st.TaskAttemptID, &st.AttemptNumber,
		&st.SchemaVersion, &st.SummaryMarkdown, &report, &st.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, ErrNoReport
	}
	if err != nil {
		return Stored{}, fmt.Errorf("get evidence report: %w", err)
	}
	st.Report = json.RawMessage(report)
	return st, nil
}
