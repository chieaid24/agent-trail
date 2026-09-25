// claiming is for update skip locked: one owner per attempt, expiring lease, expired lease re-claimable
package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

var ErrLeaseLost = errors.New("lease lost")

var ErrRunnerNotFound = errors.New("runner not found")

type Runner struct {
	ID              string            `json:"id"`
	Type            string            `json:"runner_type"`
	HostnameOrPod   string            `json:"hostname_or_pod"`
	Status          string            `json:"status"`
	Capacity        int               `json:"capacity"`
	Labels          map[string]string `json:"labels"`
	LastHeartbeatAt time.Time         `json:"last_heartbeat_at"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type RegisterParams struct {
	Type          string
	HostnameOrPod string
	Capacity      int
	Labels        map[string]string
}

// taskstatus at claim time: queued fresh, later status when recovering a lost owner's attempt
type Claim struct {
	AttemptID      string
	AttemptNumber  int
	TaskID         string
	TaskStatus     task.Status
	Title          string
	Instructions   string
	TaskCreatedAt  time.Time
	LeaseExpiresAt time.Time
}

type DispatchAttempt struct {
	AttemptID  string
	TaskID     string
	MaxRuntime time.Duration
}

// awaiting_review excluded: published task rests there for a human; re-claiming spins runners on finished work
const claimableStatuses = `('queued', 'provisioning', 'planning',
	'executing', 'validating', 'publishing')`

// no-repo tasks recover through awaiting_review: owner dying before auto-complete would strand them unclaimable
const claimableWhere = `(t.status IN ` + claimableStatuses + `
	OR (t.status = 'awaiting_review' AND t.repository_id IS NULL))`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const runnerColumns = `id, runner_type, hostname_or_pod, status, capacity,
	labels_json, last_heartbeat_at, created_at, updated_at`

func (s *Store) Register(ctx context.Context, p RegisterParams) (Runner, error) {
	switch p.Type {
	case "process", "docker", "kubernetes":
	default:
		return Runner{}, fmt.Errorf("unknown runner type %q", p.Type)
	}
	if p.HostnameOrPod == "" || len(p.HostnameOrPod) > 255 {
		return Runner{}, fmt.Errorf("hostname_or_pod must be 1-255 characters")
	}
	if p.Capacity <= 0 {
		p.Capacity = 1
	}
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	labels, err := json.Marshal(p.Labels)
	if err != nil {
		return Runner{}, fmt.Errorf("marshal labels: %w", err)
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO runners (runner_type, hostname_or_pod, capacity, labels_json)
		VALUES ($1, $2, $3, $4)
		RETURNING `+runnerColumns,
		p.Type, p.HostnameOrPod, p.Capacity, labels)
	r, err := scanRunner(row)
	if err != nil {
		return Runner{}, fmt.Errorf("register runner: %w", err)
	}
	return r, nil
}

// offline is deliberate shutdown; heartbeat must not resurrect it
func (s *Store) Heartbeat(ctx context.Context, runnerID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE runners
		SET last_heartbeat_at = now(), status = 'online', updated_at = now()
		WHERE id = $1 AND status <> 'offline'`, runnerID)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if n == 0 {
		return ErrRunnerNotFound
	}
	return nil
}

func (s *Store) MarkOffline(ctx context.Context, runnerID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE runners SET status = 'offline', updated_at = now()
		WHERE id = $1`, runnerID)
	if err != nil {
		return fmt.Errorf("mark offline: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark offline: %w", err)
	}
	if n == 0 {
		return ErrRunnerNotFound
	}
	return nil
}

// leases untouched: expiry, not runner status, makes an attempt claimable, so no duplicate execution
func (s *Store) MarkLost(ctx context.Context, threshold time.Duration) ([]Runner, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE runners SET status = 'lost', updated_at = now()
		WHERE status = 'online'
		  AND last_heartbeat_at < now() - make_interval(secs => $1)
		RETURNING `+runnerColumns, threshold.Seconds())
	if err != nil {
		return nil, fmt.Errorf("mark lost: %w", err)
	}
	defer rows.Close()

	lost := []Runner{}
	for rows.Next() {
		r, err := scanRunner(rows)
		if err != nil {
			return nil, fmt.Errorf("scan runner: %w", err)
		}
		lost = append(lost, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mark lost: %w", err)
	}
	return lost, nil
}

func (s *Store) LeasedAttemptIDs(ctx context.Context, runnerID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM task_attempts
		WHERE lease_owner = $1 AND status = 'active'`, runnerID)
	if err != nil {
		return nil, fmt.Errorf("leased attempts: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan attempt id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leased attempts: %w", err)
	}
	return ids, nil
}

// skip locked makes concurrent claims race-free; winner's lease keeps the row invisible after commit
func (s *Store) Claim(ctx context.Context, runnerID string, leaseDuration time.Duration) (*Claim, error) {
	return s.claim(ctx, runnerID, leaseDuration, "")
}

func (s *Store) ClaimAttempt(ctx context.Context, runnerID, attemptID string, leaseDuration time.Duration) (*Claim, error) {
	if !task.IsUUID(attemptID) {
		return nil, nil
	}
	return s.claim(ctx, runnerID, leaseDuration, attemptID)
}

func (s *Store) claim(ctx context.Context, runnerID string, leaseDuration time.Duration, attemptID string) (*Claim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var c Claim
	var status string
	// a revision attempt carries composed instructions; attempt 1 inherits the task's
	query := `
		SELECT a.id, a.attempt_number, t.id, t.status, t.title,
			COALESCE(a.instructions, t.instructions), t.created_at
		FROM task_attempts a
		JOIN tasks t ON t.id = a.task_id
		WHERE a.status = 'active'
		  AND (a.lease_expires_at IS NULL OR a.lease_expires_at < now())
		  AND ` + claimableWhere + `
	`
	args := []any{}
	if attemptID != "" {
		query += ` AND a.id = $1`
		args = append(args, attemptID)
	}
	query += ` ORDER BY t.priority DESC, t.created_at
		FOR UPDATE OF a SKIP LOCKED
		LIMIT 1`
	err = tx.QueryRowContext(ctx, query, args...).
		Scan(&c.AttemptID, &c.AttemptNumber, &c.TaskID, &status,
			&c.Title, &c.Instructions, &c.TaskCreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select claimable: %w", err)
	}
	c.TaskStatus = task.Status(status)

	err = tx.QueryRowContext(ctx, `
		UPDATE task_attempts
		SET runner_id = $2, lease_owner = $2,
			lease_expires_at = now() + make_interval(secs => $3),
			heartbeat_at = now()
		WHERE id = $1
		RETURNING lease_expires_at`,
		c.AttemptID, runnerID, leaseDuration.Seconds()).
		Scan(&c.LeaseExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("write lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &c, nil
}

// lists without claiming: k8s job creation is the scheduling cas; each job claims its exact row
func (s *Store) DispatchableAttempts(ctx context.Context, defaultRuntime time.Duration, limit int) ([]DispatchAttempt, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, t.id, COALESCE(t.max_runtime_seconds, $1)
		FROM task_attempts a
		JOIN tasks t ON t.id = a.task_id
		WHERE a.status = 'active'
		  AND (a.lease_expires_at IS NULL OR a.lease_expires_at < now())
		  AND `+claimableWhere+`
		ORDER BY t.priority DESC, t.created_at
		LIMIT $2`, int(defaultRuntime.Seconds()), limit)
	if err != nil {
		return nil, fmt.Errorf("list dispatchable attempts: %w", err)
	}
	defer rows.Close()

	attempts := []DispatchAttempt{}
	for rows.Next() {
		var a DispatchAttempt
		var runtimeSeconds int
		if err := rows.Scan(&a.AttemptID, &a.TaskID, &runtimeSeconds); err != nil {
			return nil, fmt.Errorf("scan dispatchable attempt: %w", err)
		}
		a.MaxRuntime = time.Duration(runtimeSeconds) * time.Second
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list dispatchable attempts: %w", err)
	}
	return attempts, nil
}

func (s *Store) RecordAttemptBase(ctx context.Context, attemptID, baseSHA string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET base_commit_sha = COALESCE(base_commit_sha, $2)
		WHERE id = $1`, attemptID, baseSHA)
	if err != nil {
		return fmt.Errorf("record attempt base: %w", err)
	}
	return nil
}

func (s *Store) RecordFinalCommit(ctx context.Context, attemptID, sha string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET final_commit_sha = COALESCE(final_commit_sha, $2)
		WHERE id = $1`, attemptID, sha)
	if err != nil {
		return fmt.Errorf("record final commit: %w", err)
	}
	return nil
}

func (s *Store) RecordPullRequest(ctx context.Context, attemptID string, number int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET pull_request_number = COALESCE(pull_request_number, $2)
		WHERE id = $1`, attemptID, number)
	if err != nil {
		return fmt.Errorf("record pull request: %w", err)
	}
	return nil
}

func (s *Store) AttemptStartedAt(ctx context.Context, attemptID string) (*time.Time, error) {
	var startedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT started_at FROM task_attempts WHERE id = $1`,
		attemptID).Scan(&startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("attempt started_at: %w", err)
	}
	if !startedAt.Valid {
		return nil, nil
	}
	return &startedAt.Time, nil
}

// expired lease never resurrected: another runner may own the attempt; caller stops on ErrLeaseLost
func (s *Store) ExtendLease(ctx context.Context, attemptID, runnerID string, leaseDuration time.Duration) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET lease_expires_at = now() + make_interval(secs => $3),
			heartbeat_at = now()
		WHERE id = $1 AND lease_owner = $2 AND lease_expires_at > now()`,
		attemptID, runnerID, leaseDuration.Seconds())
	if err != nil {
		return fmt.Errorf("extend lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("extend lease: %w", err)
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) ReleaseLease(ctx context.Context, attemptID, runnerID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND lease_owner = $2`, attemptID, runnerID)
	if err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

func scanRunner(row interface{ Scan(...any) error }) (Runner, error) {
	var r Runner
	var labels []byte
	err := row.Scan(&r.ID, &r.Type, &r.HostnameOrPod, &r.Status, &r.Capacity,
		&labels, &r.LastHeartbeatAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return Runner{}, err
	}
	if err := json.Unmarshal(labels, &r.Labels); err != nil {
		return Runner{}, fmt.Errorf("unmarshal labels: %w", err)
	}
	return r, nil
}
