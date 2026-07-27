package validation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// StoredResult is one validation_results row; JSON tags are the API wire
// shape. AttemptNumber is joined from the owning attempt.
type StoredResult struct {
	ID               string          `json:"id"`
	TaskAttemptID    string          `json:"task_attempt_id"`
	AttemptNumber    int             `json:"attempt_number"`
	Name             string          `json:"name"`
	Category         string          `json:"category"`
	Command          json.RawMessage `json:"command"`
	Status           Status          `json:"status"`
	ExitCode         *int            `json:"exit_code"`
	DurationMS       int64           `json:"duration_ms"`
	Summary          string          `json:"summary"`
	TrustedExecution bool            `json:"trusted_execution"`
	CreatedAt        time.Time       `json:"created_at"`
}

// Store persists validation results.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Insert records one result for the attempt. A replay of the same check
// name (zombie owner past its lease) is a no-op: the first recording wins
// and an exit code is never overwritten.
func (s *Store) Insert(ctx context.Context, attemptID string, r Result) error {
	command, err := json.Marshal(r.Command)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO validation_results (task_attempt_id, name, category,
			command_json, status, exit_code, duration_ms, summary,
			trusted_execution)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT ON CONSTRAINT validation_results_attempt_name_key
			DO NOTHING`,
		attemptID, r.Name, r.Category, command, string(r.Status),
		r.ExitCode, r.DurationMS, r.Summary, r.TrustedExecution)
	if err != nil {
		return fmt.Errorf("insert validation result: %w", err)
	}
	return nil
}

const storedResultColumns = `v.id, v.task_attempt_id, a.attempt_number,
	v.name, v.category, v.command_json, v.status, v.exit_code,
	v.duration_ms, v.summary, v.trusted_execution, v.created_at`

// ListForTask returns every result across the task's attempts, ordered by
// attempt then execution order. Unknown tasks return task.ErrNotFound.
func (s *Store) ListForTask(ctx context.Context, taskID string) ([]StoredResult, error) {
	if !task.IsUUID(taskID) {
		return nil, task.ErrNotFound
	}
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, taskID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check task: %w", err)
	}
	if !exists {
		return nil, task.ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+storedResultColumns+`
		FROM validation_results v
		JOIN task_attempts a ON a.id = v.task_attempt_id
		WHERE a.task_id = $1
		ORDER BY a.attempt_number, v.created_at, v.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list validation results: %w", err)
	}
	return scanStoredResults(rows)
}

// ListForAttempt returns the attempt's results in execution order.
func (s *Store) ListForAttempt(ctx context.Context, attemptID string) ([]StoredResult, error) {
	if !task.IsUUID(attemptID) {
		return nil, task.ErrAttemptNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+storedResultColumns+`
		FROM validation_results v
		JOIN task_attempts a ON a.id = v.task_attempt_id
		WHERE v.task_attempt_id = $1
		ORDER BY v.created_at, v.id`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("list attempt validation results: %w", err)
	}
	return scanStoredResults(rows)
}

func scanStoredResults(rows *sql.Rows) ([]StoredResult, error) {
	defer rows.Close()
	results := []StoredResult{}
	for rows.Next() {
		var r StoredResult
		var command []byte
		var status string
		if err := rows.Scan(&r.ID, &r.TaskAttemptID, &r.AttemptNumber,
			&r.Name, &r.Category, &command, &status, &r.ExitCode,
			&r.DurationMS, &r.Summary, &r.TrustedExecution,
			&r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan validation result: %w", err)
		}
		r.Command = json.RawMessage(command)
		r.Status = Status(status)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list validation results: %w", err)
	}
	return results, nil
}
