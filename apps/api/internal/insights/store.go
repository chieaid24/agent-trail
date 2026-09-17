package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// one snapshot for every source so attempts, spans, events, and checks agree
func (s *Store) TaskInsights(ctx context.Context, taskID string) (TaskInsights, error) {
	if !task.IsUUID(taskID) {
		return TaskInsights{}, task.ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead, ReadOnly: true,
	})
	if err != nil {
		return TaskInsights{}, fmt.Errorf("begin insights read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result := TaskInsights{TaskID: taskID, Attempts: []AttemptInsight{}}
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&result.TaskStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskInsights{}, task.ErrNotFound
	}
	if err != nil {
		return TaskInsights{}, fmt.Errorf("load task: %w", err)
	}

	attempts, index, err := loadAttempts(ctx, tx, taskID)
	if err != nil {
		return TaskInsights{}, err
	}
	if err := applySpanDurations(ctx, tx, taskID, result.TaskStatus, attempts, index); err != nil {
		return TaskInsights{}, err
	}
	if err := applyEvents(ctx, tx, taskID, attempts, index); err != nil {
		return TaskInsights{}, err
	}
	if err := applyValidations(ctx, tx, taskID, attempts, index); err != nil {
		return TaskInsights{}, err
	}
	if err := applyCost(ctx, tx, taskID, attempts, index); err != nil {
		return TaskInsights{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskInsights{}, fmt.Errorf("commit insights read: %w", err)
	}
	result.Attempts = attempts
	result.Overall = summarize(attempts)
	return result, nil
}

// queue wait is attempt creation to runner start; both come from task_attempts
func loadAttempts(ctx context.Context, tx *sql.Tx, taskID string) ([]AttemptInsight, map[string]int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, attempt_number, status, created_at, started_at,
			completed_at, failure_code
		FROM task_attempts
		WHERE task_id = $1
		ORDER BY attempt_number`, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("list attempts: %w", err)
	}
	defer rows.Close()

	attempts := []AttemptInsight{}
	index := map[string]int{}
	for rows.Next() {
		var a AttemptInsight
		var createdAt sql.NullTime
		var startedAt, completedAt sql.NullTime
		var failureCode sql.NullString
		if err := rows.Scan(&a.AttemptID, &a.AttemptNumber, &a.Status,
			&createdAt, &startedAt, &completedAt, &failureCode); err != nil {
			return nil, nil, fmt.Errorf("scan attempt: %w", err)
		}
		if startedAt.Valid {
			a.StartedAt = &startedAt.Time
			if createdAt.Valid && !startedAt.Time.Before(createdAt.Time) {
				wait := startedAt.Time.Sub(createdAt.Time).Milliseconds()
				a.QueueWaitMS = &wait
			}
		}
		if completedAt.Valid {
			a.CompletedAt = &completedAt.Time
		}
		if failureCode.Valid {
			a.FailureCode = &failureCode.String
		}
		index[a.AttemptID] = len(attempts)
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list attempts: %w", err)
	}
	return attempts, index, nil
}

// a recovered attempt records one span per claim, so durations sum per name
func applySpanDurations(ctx context.Context, tx *sql.Tx, taskID, taskStatus string, attempts []AttemptInsight, index map[string]int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT task_attempt_id, name,
			round(SUM(EXTRACT(EPOCH FROM (end_time - start_time)) * 1000))::bigint
		FROM task_spans
		WHERE task_id = $1 AND task_attempt_id IS NOT NULL
			AND name IN ($2, $3, $4, $5)
		GROUP BY task_attempt_id, name`,
		taskID, spanAttempt, spanProvisioning, spanAgentSession, spanValidation)
	if err != nil {
		return fmt.Errorf("sum task spans: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var attemptID, name string
		var total int64
		if err := rows.Scan(&attemptID, &name, &total); err != nil {
			return fmt.Errorf("scan span sum: %w", err)
		}
		i, ok := index[attemptID]
		if !ok {
			continue
		}
		value := total
		switch name {
		case spanAttempt:
			if attempts[i].Status != "active" || taskStatus == "awaiting_review" || taskStatus == "revision_requested" {
				attempts[i].TotalRuntimeMS = &value
			}
		case spanProvisioning:
			attempts[i].ProvisioningMS = &value
		case spanAgentSession:
			attempts[i].AgentSessionMS = &value
		case spanValidation:
			attempts[i].ValidationMS = &value
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sum task spans: %w", err)
	}
	return nil
}

// publishing runs from the task.publishing transition to task.awaiting_review
func applyEvents(ctx context.Context, tx *sql.Tx, taskID string, attempts []AttemptInsight, index map[string]int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.task_attempt_id, count(*),
			min(e."timestamp") FILTER (WHERE e.event_type = $2),
			min(e."timestamp") FILTER (WHERE e.event_type = $3)
		FROM activity_events e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1
		GROUP BY e.task_attempt_id`,
		taskID, eventPublishing, eventAwaitingReview)
	if err != nil {
		return fmt.Errorf("count attempt events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var attemptID string
		var count int
		var publishing, awaitingReview sql.NullTime
		if err := rows.Scan(&attemptID, &count, &publishing, &awaitingReview); err != nil {
			return fmt.Errorf("scan attempt events: %w", err)
		}
		i, ok := index[attemptID]
		if !ok {
			continue
		}
		attempts[i].EventCount = count
		if publishing.Valid && awaitingReview.Valid &&
			!awaitingReview.Time.Before(publishing.Time) {
			value := awaitingReview.Time.Sub(publishing.Time).Milliseconds()
			attempts[i].PublishingMS = &value
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("count attempt events: %w", err)
	}
	return nil
}

func applyValidations(ctx context.Context, tx *sql.Tx, taskID string, attempts []AttemptInsight, index map[string]int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT v.task_attempt_id, v.status, v.trusted_execution, count(*)
		FROM validation_results v
		JOIN task_attempts a ON a.id = v.task_attempt_id
		WHERE a.task_id = $1
		GROUP BY v.task_attempt_id, v.status, v.trusted_execution`, taskID)
	if err != nil {
		return fmt.Errorf("count validation results: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var attemptID, status string
		var trusted bool
		var count int
		if err := rows.Scan(&attemptID, &status, &trusted, &count); err != nil {
			return fmt.Errorf("scan validation counts: %w", err)
		}
		i, ok := index[attemptID]
		if !ok {
			continue
		}
		if attempts[i].Validation == nil {
			attempts[i].Validation = &ValidationSummary{}
		}
		addValidation(attempts[i].Validation, status, trusted, count)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("count validation results: %w", err)
	}
	return nil
}

func addValidation(summary *ValidationSummary, status string, trusted bool, count int) {
	summary.Total += count
	if trusted {
		summary.Trusted += count
	}
	switch status {
	case "passed":
		summary.Passed += count
	case "failed":
		summary.Failed += count
	case "timed_out":
		summary.TimedOut += count
	case "error":
		summary.Error += count
	}
}

func applyCost(ctx context.Context, tx *sql.Tx, taskID string, attempts []AttemptInsight, index map[string]int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.task_attempt_id, e.payload_json
		FROM activity_events e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1 AND e.event_type = $2
		ORDER BY a.attempt_number, e.sequence_number`, taskID, eventCostUpdate)
	if err != nil {
		return fmt.Errorf("list cost events: %w", err)
	}
	defer rows.Close()

	byAttempt := map[string][]json.RawMessage{}
	for rows.Next() {
		var attemptID string
		var payload []byte
		if err := rows.Scan(&attemptID, &payload); err != nil {
			return fmt.Errorf("scan cost event: %w", err)
		}
		byAttempt[attemptID] = append(byAttempt[attemptID], json.RawMessage(payload))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list cost events: %w", err)
	}
	for attemptID, payloads := range byAttempt {
		if i, ok := index[attemptID]; ok {
			attempts[i].Cost = aggregateCost(payloads)
		}
	}
	return nil
}

func summarize(attempts []AttemptInsight) OverallInsight {
	overall := OverallInsight{AttemptCount: len(attempts)}
	var runtime int64
	runtimeComplete := len(attempts) > 0
	for _, a := range attempts {
		overall.EventCount += a.EventCount
		if a.TotalRuntimeMS == nil {
			runtimeComplete = false
		} else {
			runtime += *a.TotalRuntimeMS
		}
		if a.Validation != nil {
			if overall.Validation == nil {
				overall.Validation = &ValidationSummary{}
			}
			overall.Validation.Total += a.Validation.Total
			overall.Validation.Passed += a.Validation.Passed
			overall.Validation.Failed += a.Validation.Failed
			overall.Validation.TimedOut += a.Validation.TimedOut
			overall.Validation.Error += a.Validation.Error
			overall.Validation.Trusted += a.Validation.Trusted
		}
		if a.Cost != nil {
			if overall.Cost == nil {
				overall.Cost = &CostSummary{}
			}
			overall.Cost.TotalUSD += a.Cost.TotalUSD
			overall.Cost.UpdateCount += a.Cost.UpdateCount
		}
	}
	if runtimeComplete {
		overall.TotalRuntimeMS = &runtime
	}
	return overall
}
