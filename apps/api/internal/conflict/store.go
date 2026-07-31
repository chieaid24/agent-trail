package conflict

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// Store persists conflict warnings and reads sibling diffs.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ActiveSiblings returns published diffs for active repository tasks.
func (s *Store) ActiveSiblings(ctx context.Context, repositoryID, excludeTaskID string) ([]Sibling, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.title, a.base_commit_sha, a.final_commit_sha
		FROM tasks t
		JOIN LATERAL (
			SELECT base_commit_sha, final_commit_sha
			FROM task_attempts
			WHERE task_id = t.id
			  AND base_commit_sha IS NOT NULL
			  AND final_commit_sha IS NOT NULL
			ORDER BY attempt_number DESC
			LIMIT 1
		) a ON true
		WHERE t.repository_id = $1 AND t.id <> $2 AND t.phase <> 'terminal'
		ORDER BY t.created_at`, repositoryID, excludeTaskID)
	if err != nil {
		return nil, fmt.Errorf("conflict: active siblings: %w", err)
	}
	defer rows.Close()

	var siblings []Sibling
	for rows.Next() {
		var sib Sibling
		if err := rows.Scan(&sib.TaskID, &sib.Title, &sib.BaseSHA, &sib.FinalSHA); err != nil {
			return nil, fmt.Errorf("conflict: scan sibling: %w", err)
		}
		siblings = append(siblings, sib)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conflict: active siblings: %w", err)
	}
	return siblings, nil
}

// Reconcile atomically replaces taskID's conflict set.
func (s *Store) Reconcile(ctx context.Context, repositoryID, taskID string, detections []Detection) (retErr error) {
	desired := make(map[string]Detection, len(detections))
	for _, detection := range detections {
		if len(detection.Kinds) == 0 {
			return fmt.Errorf("conflict: reconcile needs at least one kind")
		}
		desired[detection.OtherTaskID] = detection
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("conflict: begin reconcile: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx, `
		SELECT CASE WHEN task_a_id = $1 THEN task_b_id ELSE task_a_id END
		FROM task_conflicts
		WHERE task_a_id = $1 OR task_b_id = $1
		ORDER BY task_a_id, task_b_id
		FOR UPDATE`, taskID)
	if err != nil {
		return fmt.Errorf("conflict: lock existing pairs: %w", err)
	}
	var existing []string
	for rows.Next() {
		var otherTaskID string
		if err := rows.Scan(&otherTaskID); err != nil {
			rows.Close()
			return fmt.Errorf("conflict: scan existing pair: %w", err)
		}
		existing = append(existing, otherTaskID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("conflict: close existing pairs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("conflict: read existing pairs: %w", err)
	}
	for _, otherTaskID := range existing {
		if _, ok := desired[otherTaskID]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM task_conflicts
			WHERE task_a_id = LEAST($1::uuid, $2::uuid)
			  AND task_b_id = GREATEST($1::uuid, $2::uuid)`, taskID, otherTaskID); err != nil {
			return fmt.Errorf("conflict: delete stale pair: %w", err)
		}
	}
	otherTaskIDs := make([]string, 0, len(desired))
	for otherTaskID := range desired {
		otherTaskIDs = append(otherTaskIDs, otherTaskID)
	}
	sort.Strings(otherTaskIDs)
	for _, otherTaskID := range otherTaskIDs {
		detection := desired[otherTaskID]
		kindsJSON, err := json.Marshal(detection.Kinds)
		if err != nil {
			return fmt.Errorf("conflict: encode kinds: %w", err)
		}
		files := detection.Files
		if files == nil {
			files = []string{}
		}
		filesJSON, err := json.Marshal(files)
		if err != nil {
			return fmt.Errorf("conflict: encode files: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_conflicts (repository_id, task_a_id, task_b_id, kinds, files)
			VALUES ($1, LEAST($2::uuid, $3::uuid), GREATEST($2::uuid, $3::uuid), $4, $5)
			ON CONFLICT ON CONSTRAINT task_conflicts_pair_key
			DO UPDATE SET kinds = EXCLUDED.kinds, files = EXCLUDED.files,
				updated_at = now()`, repositoryID, taskID, otherTaskID, kindsJSON, filesJSON); err != nil {
			return fmt.Errorf("conflict: upsert pair: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("conflict: commit reconcile: %w", err)
	}
	return nil
}

// ListForTask returns active warnings oriented toward the other task.
func (s *Store) ListForTask(ctx context.Context, taskID string) ([]TaskConflict, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, taskID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("conflict: find task: %w", err)
	}
	if !exists {
		return nil, task.ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, o.id, o.title, c.kinds, c.files, c.detected_at, c.updated_at
		FROM task_conflicts c
		JOIN tasks self ON self.id = $1
		JOIN tasks o ON o.id = CASE WHEN c.task_a_id = $1
			THEN c.task_b_id ELSE c.task_a_id END
		WHERE (c.task_a_id = $1 OR c.task_b_id = $1)
		  AND self.phase <> 'terminal' AND o.phase <> 'terminal'
		ORDER BY c.detected_at DESC, c.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("conflict: list for task: %w", err)
	}
	defer rows.Close()

	conflicts := []TaskConflict{}
	for rows.Next() {
		var tc TaskConflict
		var kindsJSON, filesJSON []byte
		if err := rows.Scan(&tc.ID, &tc.OtherTaskID, &tc.OtherTaskTitle,
			&kindsJSON, &filesJSON, &tc.DetectedAt, &tc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("conflict: scan conflict: %w", err)
		}
		if err := json.Unmarshal(kindsJSON, &tc.Kinds); err != nil {
			return nil, fmt.Errorf("conflict: decode kinds: %w", err)
		}
		if err := json.Unmarshal(filesJSON, &tc.Files); err != nil {
			return nil, fmt.Errorf("conflict: decode files: %w", err)
		}
		conflicts = append(conflicts, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conflict: list for task: %w", err)
	}
	return conflicts, nil
}
