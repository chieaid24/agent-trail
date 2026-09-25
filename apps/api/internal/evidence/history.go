package evidence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chieaid24/agent-trail/apps/api/internal/insights"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// one pull request body row per attempt; empty strings and nil cost mean never recorded
type AttemptHistory struct {
	Number      int
	Status      string
	BaseCommit  string
	FinalCommit string
	// overall trusted outcome; empty when no trusted check ran
	Validation validation.Status
	CostUSD    *float64
}

func (s *Store) AttemptHistory(ctx context.Context, taskID string) ([]AttemptHistory, error) {
	if !task.IsUUID(taskID) {
		return nil, task.ErrNotFound
	}
	// failed if any trusted check failed; passed only when every trusted check passed
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.attempt_number, a.status,
			COALESCE(a.base_commit_sha, ''), COALESCE(a.final_commit_sha, ''),
			COALESCE((
				SELECT CASE
					WHEN count(*) = 0 THEN NULL
					WHEN bool_or(v.status = 'failed') THEN 'failed'
					WHEN bool_and(v.status = 'passed') THEN 'passed'
					ELSE 'error' END
				FROM validation_results v
				WHERE v.task_attempt_id = a.id AND v.trusted_execution), '')
		FROM task_attempts a
		WHERE a.task_id = $1
		ORDER BY a.attempt_number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attempt history: %w", err)
	}
	defer rows.Close()
	history := []AttemptHistory{}
	ids := []string{}
	for rows.Next() {
		var h AttemptHistory
		var id string
		if err := rows.Scan(&id, &h.Number, &h.Status, &h.BaseCommit, &h.FinalCommit, &h.Validation); err != nil {
			return nil, fmt.Errorf("scan attempt history: %w", err)
		}
		ids = append(ids, id)
		history = append(history, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attempt history: %w", err)
	}
	costs, err := s.attemptCosts(ctx, taskID)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		if summary := insights.AggregateCost(costs[id]); summary != nil {
			usd := summary.TotalUSD
			history[i].CostUSD = &usd
		}
	}
	return history, nil
}

func (s *Store) attemptCosts(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.task_attempt_id, e.payload_json
		FROM activity_events e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1 AND e.event_type = 'agent.cost_update'
		ORDER BY a.attempt_number, e.sequence_number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list cost events: %w", err)
	}
	defer rows.Close()
	costs := map[string][]json.RawMessage{}
	for rows.Next() {
		var attemptID string
		var payload []byte
		if err := rows.Scan(&attemptID, &payload); err != nil {
			return nil, fmt.Errorf("scan cost event: %w", err)
		}
		costs[attemptID] = append(costs[attemptID], json.RawMessage(payload))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cost events: %w", err)
	}
	return costs, nil
}
