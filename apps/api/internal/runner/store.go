// Package runner implements the runner registry, task-attempt leasing, and
// the loop that drives a claimed attempt through the fake agent flow.
// Spec: docs/architecture/runner.md. Claiming is FOR UPDATE SKIP LOCKED
// against task_attempts (ADR-0003): only one runner ever owns an attempt,
// every claim carries an expiring lease, and a lost runner's attempt becomes
// claimable again once its lease expires.
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

// ErrLeaseLost is returned when the caller no longer holds a live lease on
// the attempt (expired, released, or claimed by another runner).
var ErrLeaseLost = errors.New("lease lost")

// ErrRunnerNotFound is returned when the runner id does not exist.
var ErrRunnerNotFound = errors.New("runner not found")

// Runner mirrors the runners table (docs/architecture/data-model.md).
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

// RegisterParams describe a runner registering itself.
type RegisterParams struct {
	Type          string // process, docker, kubernetes
	HostnameOrPod string
	Capacity      int               // defaults to 1
	Labels        map[string]string // optional
}

// Claim is one leased task attempt. TaskStatus is the task's status at claim
// time: "queued" for a fresh attempt, a later status when recovering an
// attempt whose previous owner lost its lease.
type Claim struct {
	AttemptID      string
	AttemptNumber  int
	TaskID         string
	TaskStatus     task.Status
	Title          string
	Instructions   string
	LeaseExpiresAt time.Time
}

// claimableStatuses are the task statuses whose active attempt a runner may
// own: queued (fresh) plus every status the runner itself drives, so an
// expired lease anywhere mid-flight is recoverable.
const claimableStatuses = `('queued', 'provisioning', 'planning',
	'executing', 'validating', 'publishing', 'awaiting_review')`

// Store is the PostgreSQL-backed runner registry and lease arbiter.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const runnerColumns = `id, runner_type, hostname_or_pod, status, capacity,
	labels_json, last_heartbeat_at, created_at, updated_at`

// Register inserts a new online runner and returns it.
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

// Heartbeat records liveness and revives a runner marked lost. Offline is
// deliberate (shutdown), so a heartbeat does not resurrect it.
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

// MarkOffline records a deliberate shutdown.
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

// MarkLost flips online runners whose heartbeat is older than threshold to
// lost and returns them. The UPDATE is atomic, so concurrent reapers each
// see a runner transition at most once. Leases are left untouched: expiry,
// not runner status, is what makes an attempt claimable again, so a lost
// runner never immediately causes duplicate execution.
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

// LeasedAttemptIDs returns the active attempts currently leased by a runner,
// for reporting a loss on their timelines.
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

// Claim leases the next claimable attempt for runnerID and returns it, or
// nil when nothing is claimable. Selection is highest task priority first,
// then oldest. FOR UPDATE OF task_attempts SKIP LOCKED makes concurrent
// claims race-free: the row a winner holds is invisible to everyone else,
// and the lease it writes keeps it invisible after commit.
func (s *Store) Claim(ctx context.Context, runnerID string, leaseDuration time.Duration) (*Claim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var c Claim
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT a.id, a.attempt_number, t.id, t.status, t.title, t.instructions
		FROM task_attempts a
		JOIN tasks t ON t.id = a.task_id
		WHERE a.status = 'active'
		  AND (a.lease_expires_at IS NULL OR a.lease_expires_at < now())
		  AND t.status IN `+claimableStatuses+`
		ORDER BY t.priority DESC, t.created_at
		FOR UPDATE OF a SKIP LOCKED
		LIMIT 1`).
		Scan(&c.AttemptID, &c.AttemptNumber, &c.TaskID, &status,
			&c.Title, &c.Instructions)
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

// ExtendLease pushes the expiry of a lease the runner still holds. A lease
// that already expired is not resurrected -- another runner may have claimed
// the attempt -- so the caller must stop working on ErrLeaseLost.
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

// ReleaseLease clears a lease the runner holds (done or giving up). The
// attempt may be in any status by now; only ownership is checked.
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

// scanRunner scans one runners row in runnerColumns order.
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
