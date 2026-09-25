package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// every state change goes through applyTransition under the task row lock
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const taskColumns = `id, organization_id, repository_id, source_type,
	source_issue_number, source_comment_id, title, instructions, status,
	phase, priority, base_branch, base_commit_sha, working_branch,
	agent_provider, agent_model, policy_id, requested_by_user_id,
	max_runtime_seconds, max_cost_usd, started_at, completed_at,
	cancel_requested_at, failure_code, failure_message, status_reason,
	created_at, updated_at, version`

// bound of tasks.status_reason; the event payload keeps the full reason
const maxStatusReasonLen = 1000

var uuidRe = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// inserts task + first attempt, queues immediately; returns status queued at version 2
func (s *Store) Create(ctx context.Context, p CreateParams) (Task, error) {
	if p.BaseBranch == "" {
		p.BaseBranch = "main"
	}
	if p.SourceType == "" {
		p.SourceType = "api"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO tasks (title, instructions, priority, base_branch,
			max_runtime_seconds, max_cost_usd, source_type,
			source_issue_number, source_comment_id, organization_id,
			repository_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+taskColumns,
		p.Title, p.Instructions, p.Priority, p.BaseBranch,
		p.MaxRuntimeSeconds, p.MaxCostUSD, p.SourceType,
		p.SourceIssueNumber, p.SourceCommentID, p.OrganizationID,
		p.RepositoryID)
	created, err := scanTask(row)
	if isUniqueViolation(err, "tasks_one_active_per_issue_idx") {
		return Task{}, ErrActiveTaskExists
	}
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}

	var attemptID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO task_attempts
			(task_id, attempt_number, requested_by_login, trigger_comment_id)
		VALUES ($1, 1, NULLIF($2, ''), $3) RETURNING id`,
		created.ID, p.RequestedByLogin, p.SourceCommentID).Scan(&attemptID)
	if err != nil {
		return Task{}, fmt.Errorf("insert attempt: %w", err)
	}

	payload, err := json.Marshal(map[string]string{"title": created.Title})
	if err != nil {
		return Task{}, fmt.Errorf("marshal payload: %w", err)
	}
	if err := insertEvent(ctx, tx, attemptID, EventTypeCreated, "api", payload, ""); err != nil {
		return Task{}, err
	}

	queued, err := applyTransition(ctx, tx, created, TransitionParams{
		To:     StatusQueued,
		Source: "system",
	})
	if err != nil {
		return Task{}, err
	}

	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit: %w", err)
	}
	return queued, nil
}

func (s *Store) Get(ctx context.Context, id string) (Task, error) {
	if !IsUUID(id) {
		return Task{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

// first writer wins: branch and base stay stable across owners
func (s *Store) EnsureGitContext(ctx context.Context, id, baseCommitSHA, workingBranch string) (base, branch string, err error) {
	if !IsUUID(id) {
		return "", "", ErrNotFound
	}
	err = s.db.QueryRowContext(ctx, `
		UPDATE tasks
		SET base_commit_sha = COALESCE(base_commit_sha, $2),
			working_branch = COALESCE(working_branch, $3),
			version = version + 1, updated_at = now()
		WHERE id = $1
		RETURNING base_commit_sha, working_branch`,
		id, baseCommitSHA, workingBranch).Scan(&base, &branch)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("ensure git context: %w", err)
	}
	return base, branch, nil
}

func (s *Store) List(ctx context.Context, p ListParams) ([]Task, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := `SELECT ` + taskColumns + ` FROM tasks`
	args := []any{}
	if p.Status != "" {
		if !p.Status.Valid() {
			return nil, fmt.Errorf("unknown status %q", p.Status)
		}
		query += ` WHERE status = $1`
		args = append(args, string(p.Status))
	}
	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d`, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

func (s *Store) Transition(ctx context.Context, id string, p TransitionParams) (Task, error) {
	if !IsUUID(id) {
		return Task{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := lockTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	next, err := applyTransition(ctx, tx, cur, p)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit: %w", err)
	}
	return next, nil
}

// awaiting_review -> revision_requested -> queued under one row lock; the limit and replay
// checks share that lock so two revise commands cannot both pass
func (s *Store) RequestRevision(ctx context.Context, id string, p RevisionParams) (Task, Attempt, error) {
	if !IsUUID(id) {
		return Task{}, Attempt{}, ErrNotFound
	}
	if p.Instructions == "" || !shaRe.MatchString(p.BaseCommitSHA) ||
		p.RequestedByLogin == "" || p.TriggerCommentID <= 0 || p.MaxAttempts < 1 ||
		p.IdempotencyKey == "" {
		return Task{}, Attempt{}, errors.New("revision params incomplete")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, Attempt{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := lockTask(ctx, tx, id)
	if err != nil {
		return Task{}, Attempt{}, err
	}
	// replay outranks every other guard: a repeated command must never draw a limit reply
	var seen bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM activity_events e
			JOIN task_attempts a ON a.id = e.task_attempt_id
			WHERE a.task_id = $1 AND e.idempotency_key = $2)`,
		id, p.IdempotencyKey).Scan(&seen)
	if err != nil {
		return Task{}, Attempt{}, fmt.Errorf("revision replay check: %w", err)
	}
	if seen {
		return Task{}, Attempt{}, ErrRevisionReplayed
	}
	var attempts int
	err = tx.QueryRowContext(ctx,
		`SELECT count(*) FROM task_attempts WHERE task_id = $1`, id).Scan(&attempts)
	if err != nil {
		return Task{}, Attempt{}, fmt.Errorf("count attempts: %w", err)
	}
	if attempts >= p.MaxAttempts {
		return Task{}, Attempt{}, fmt.Errorf("%w: %d of %d attempts used", ErrRevisionLimit, attempts, p.MaxAttempts)
	}
	requested, err := applyTransition(ctx, tx, cur, TransitionParams{
		To:             StatusRevisionRequested,
		Source:         "system",
		Reason:         p.Reason,
		IdempotencyKey: p.IdempotencyKey,
	})
	if err != nil {
		return Task{}, Attempt{}, err
	}
	if requested.Status != StatusRevisionRequested {
		return Task{}, Attempt{}, ErrRevisionReplayed
	}
	queued, err := applyTransition(ctx, tx, requested, TransitionParams{
		To:       StatusQueued,
		Source:   "system",
		Reason:   p.Reason,
		revision: &p,
	})
	if err != nil {
		return Task{}, Attempt{}, err
	}
	next, err := scanAttempt(tx.QueryRowContext(ctx, `
		SELECT `+attemptColumns+` FROM task_attempts
		WHERE task_id = $1 AND status = 'active'`, id))
	if err != nil {
		return Task{}, Attempt{}, fmt.Errorf("load revision attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, Attempt{}, fmt.Errorf("commit: %w", err)
	}
	return queued, next, nil
}

// cancel of already-cancelled task is idempotent no-op; other terminal states reject
func (s *Store) Cancel(ctx context.Context, id, reason string) (Task, error) {
	if !IsUUID(id) {
		return Task{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := lockTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if cur.Status == StatusCancelled {
		return cur, nil
	}
	next, err := applyTransition(ctx, tx, cur, TransitionParams{
		To:     StatusCancelled,
		Source: "api",
		Reason: reason,
	})
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit: %w", err)
	}
	return next, nil
}

func (s *Store) Events(ctx context.Context, id string, limit int) ([]Event, error) {
	if !IsUUID(id) {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}

	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check task: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT e.id, e.task_attempt_id, a.attempt_number, e.sequence_number,
			e.event_type, e.source, e."timestamp", e.payload_json,
			e.redaction_status, e.created_at
		FROM activity_events e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1
		ORDER BY a.attempt_number, e.sequence_number
		LIMIT %d`, limit), id)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.TaskAttemptID, &e.AttemptNumber,
			&e.SequenceNumber, &e.EventType, &e.Source, &e.Timestamp,
			&payload, &e.RedactionStatus, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

func (s *Store) EventsAfter(ctx context.Context, id string, afterAttempt int, afterSequence int64, limit int) ([]Event, error) {
	if !IsUUID(id) {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}

	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check task: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT e.id, e.task_attempt_id, a.attempt_number, e.sequence_number,
			e.event_type, e.source, e."timestamp", e.payload_json,
			e.redaction_status, e.created_at
		FROM activity_events e
		JOIN task_attempts a ON a.id = e.task_attempt_id
		WHERE a.task_id = $1
			AND (a.attempt_number, e.sequence_number) > ($2, $3)
		ORDER BY a.attempt_number, e.sequence_number
		LIMIT %d`, limit), id, afterAttempt, afterSequence)
	if err != nil {
		return nil, fmt.Errorf("list events after cursor: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.TaskAttemptID, &e.AttemptNumber,
			&e.SequenceNumber, &e.EventType, &e.Source, &e.Timestamp,
			&payload, &e.RedactionStatus, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events after cursor: %w", err)
	}
	return events, nil
}

// takes task row lock so sequence number cannot race a transition
func (s *Store) AppendAttemptEvent(ctx context.Context, attemptID, eventType, source string, payload any) error {
	if !IsUUID(attemptID) {
		return ErrAttemptNotFound
	}
	if !validSource(source) {
		return fmt.Errorf("unknown event source %q", source)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if string(raw) == "null" {
		raw = []byte(`{}`)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var taskID string
	err = tx.QueryRowContext(ctx,
		`SELECT task_id FROM task_attempts WHERE id = $1`, attemptID).
		Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptNotFound
	}
	if err != nil {
		return fmt.Errorf("load attempt: %w", err)
	}
	if _, err := lockTask(ctx, tx, taskID); err != nil {
		return err
	}
	if err := insertEvent(ctx, tx, attemptID, eventType, source, raw, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *Store) AppendEvent(ctx context.Context, taskID, eventType, source string, payload map[string]string) error {
	if !IsUUID(taskID) {
		return ErrNotFound
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := lockTask(ctx, tx, taskID); err != nil {
		return err
	}
	var attemptID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM task_attempts
		WHERE task_id = $1 AND status = 'active'`, taskID).Scan(&attemptID)
	if err != nil {
		return fmt.Errorf("load active attempt: %w", err)
	}
	if err := insertEvent(ctx, tx, attemptID, eventType, source, body, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func validSource(s string) bool {
	switch s {
	case "api", "system", "runner", "agent":
		return true
	}
	return false
}

func (s *Store) ActiveTaskForIssue(ctx context.Context, repositoryID string, issueNumber int64) (Task, bool, error) {
	if !IsUUID(repositoryID) {
		return Task{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE repository_id = $1 AND source_issue_number = $2
			AND source_type = 'github_issue' AND phase <> 'terminal'`,
		repositoryID, issueNumber)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, fmt.Errorf("active task for issue: %w", err)
	}
	return t, true, nil
}

// newest non-terminal task outranks a finished predecessor that reused the branch
func (s *Store) TaskForBranch(ctx context.Context, repositoryID, workingBranch string) (Task, bool, error) {
	if !IsUUID(repositoryID) || workingBranch == "" {
		return Task{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE repository_id = $1 AND working_branch = $2
		ORDER BY phase = 'terminal', created_at DESC, id DESC
		LIMIT 1`, repositoryID, workingBranch)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, fmt.Errorf("task for branch: %w", err)
	}
	return t, true, nil
}

const attemptColumns = `id, task_id, attempt_number, status, base_commit_sha,
	final_commit_sha, pull_request_number, instructions, requested_by_login,
	trigger_comment_id, trigger_check_run_id, trigger_check_run_completed_at,
	feedback_json, failure_code, failure_message, started_at, completed_at,
	created_at`

func (s *Store) Attempt(ctx context.Context, attemptID string) (Attempt, error) {
	if !IsUUID(attemptID) {
		return Attempt{}, ErrAttemptNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+attemptColumns+` FROM task_attempts WHERE id = $1`, attemptID)
	a, err := scanAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrAttemptNotFound
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("get attempt: %w", err)
	}
	return a, nil
}

func (s *Store) Attempts(ctx context.Context, taskID string) ([]Attempt, error) {
	if !IsUUID(taskID) {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+attemptColumns+` FROM task_attempts
		WHERE task_id = $1 ORDER BY attempt_number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	defer rows.Close()
	attempts := []Attempt{}
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	if len(attempts) == 0 {
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, taskID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check task: %w", err)
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return attempts, nil
}

// first writer wins so a replayed side effect keeps the original check run
func (s *Store) RecordTriggerCheckRun(ctx context.Context, taskID string, checkRunID int64) error {
	if !IsUUID(taskID) {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET trigger_check_run_id = COALESCE(trigger_check_run_id, $2)
		WHERE task_id = $1 AND status = 'active'`, taskID, checkRunID)
	if err != nil {
		return fmt.Errorf("record trigger check run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("record trigger check run: %w", err)
	}
	if n == 0 {
		return ErrAttemptNotFound
	}
	return nil
}

func (s *Store) MarkTriggerCheckRunCompleted(ctx context.Context, attemptID string) error {
	if !IsUUID(attemptID) {
		return ErrAttemptNotFound
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_attempts
		SET trigger_check_run_completed_at = COALESCE(trigger_check_run_completed_at, now())
		WHERE id = $1 AND trigger_check_run_id IS NOT NULL`, attemptID)
	if err != nil {
		return fmt.Errorf("mark trigger check run completed: %w", err)
	}
	return nil
}

func scanAttempt(row interface{ Scan(...any) error }) (Attempt, error) {
	var a Attempt
	var (
		base, final, instructions, requestedBy, failureCode, failureMsg sql.NullString
		prNumber, commentID, checkRunID                                 sql.NullInt64
		checkRunCompletedAt, startedAt, completedAt                     sql.NullTime
		feedback                                                        []byte
	)
	err := row.Scan(&a.ID, &a.TaskID, &a.Number, &a.Status, &base, &final,
		&prNumber, &instructions, &requestedBy, &commentID, &checkRunID,
		&checkRunCompletedAt, &feedback, &failureCode, &failureMsg,
		&startedAt, &completedAt, &a.CreatedAt)
	if err != nil {
		return Attempt{}, err
	}
	a.BaseCommitSHA = nullStr(base)
	a.FinalCommitSHA = nullStr(final)
	a.PullRequestNumber = nullInt64(prNumber)
	a.Instructions = nullStr(instructions)
	a.RequestedByLogin = nullStr(requestedBy)
	a.TriggerCommentID = nullInt64(commentID)
	a.TriggerCheckRunID = nullInt64(checkRunID)
	a.TriggerCheckRunCompletedAt = nullTime(checkRunCompletedAt)
	a.FailureCode = nullStr(failureCode)
	a.FailureMessage = nullStr(failureMsg)
	a.StartedAt = nullTime(startedAt)
	a.CompletedAt = nullTime(completedAt)
	a.Feedback = []FeedbackItem{}
	if len(feedback) > 0 {
		if err := json.Unmarshal(feedback, &a.Feedback); err != nil {
			return Attempt{}, fmt.Errorf("decode feedback: %w", err)
		}
	}
	return a, nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func lockTask(ctx context.Context, tx *sql.Tx, id string) (Task, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1 FOR UPDATE`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("lock task: %w", err)
	}
	return t, nil
}

// tx must hold cur's row lock; replayed idempotency key returns cur untouched
func applyTransition(ctx context.Context, tx *sql.Tx, cur Task, p TransitionParams) (Task, error) {
	source := p.Source
	if source == "" {
		source = "system"
	}
	if !validSource(source) {
		return Task{}, fmt.Errorf("unknown event source %q", source)
	}

	if p.IdempotencyKey != "" {
		var seen bool
		err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM activity_events e
				JOIN task_attempts a ON a.id = e.task_attempt_id
				WHERE a.task_id = $1 AND e.idempotency_key = $2)`,
			cur.ID, p.IdempotencyKey).Scan(&seen)
		if err != nil {
			return Task{}, fmt.Errorf("idempotency check: %w", err)
		}
		if seen {
			return cur, nil
		}
	}

	if p.ExpectedVersion != 0 && p.ExpectedVersion != cur.Version {
		return Task{}, &VersionConflictError{
			Expected: p.ExpectedVersion, Actual: cur.Version,
		}
	}
	if !CanTransition(cur.Status, p.To) {
		return Task{}, &InvalidTransitionError{From: cur.Status, To: p.To}
	}

	var attemptID string
	var attemptNumber int
	err := tx.QueryRowContext(ctx, `
		SELECT id, attempt_number FROM task_attempts
		WHERE task_id = $1 AND status = 'active' FOR UPDATE`,
		cur.ID).Scan(&attemptID, &attemptNumber)
	if err != nil {
		return Task{}, fmt.Errorf("load active attempt: %w", err)
	}

	eventAttemptID := attemptID
	switch {
	case p.To.Terminal():
		_, err = tx.ExecContext(ctx, `
			UPDATE task_attempts
			SET status = $2, completed_at = now(),
				failure_code = NULLIF($3, ''),
				failure_message = NULLIF($4, '')
			WHERE id = $1`,
			attemptID, string(p.To), p.FailureCode, p.FailureMessage)
		if err != nil {
			return Task{}, fmt.Errorf("finish attempt: %w", err)
		}
	case cur.Status == StatusRevisionRequested && p.To == StatusQueued:
		_, err = tx.ExecContext(ctx, `
			UPDATE task_attempts
			SET status = 'superseded', completed_at = now()
			WHERE id = $1`, attemptID)
		if err != nil {
			return Task{}, fmt.Errorf("supersede attempt: %w", err)
		}
		eventAttemptID, err = insertRevisionAttempt(ctx, tx, cur.ID, attemptNumber+1, p.revision)
		if err != nil {
			return Task{}, err
		}
	case p.To == StatusProvisioning:
		_, err = tx.ExecContext(ctx, `
			UPDATE task_attempts
			SET started_at = COALESCE(started_at, now())
			WHERE id = $1`, attemptID)
		if err != nil {
			return Task{}, fmt.Errorf("start attempt: %w", err)
		}
	}

	row := tx.QueryRowContext(ctx, `
		UPDATE tasks SET
			status = $2, phase = $3, version = version + 1,
			updated_at = now(),
			started_at = CASE WHEN $4
				THEN COALESCE(started_at, now()) ELSE started_at END,
			completed_at = CASE WHEN $5 THEN now() ELSE completed_at END,
			cancel_requested_at = CASE WHEN $6
				THEN COALESCE(cancel_requested_at, now())
				ELSE cancel_requested_at END,
			failure_code = COALESCE(NULLIF($7, ''), failure_code),
			failure_message = COALESCE(NULLIF($8, ''), failure_message),
			status_reason = NULLIF($9, '')
		WHERE id = $1
		RETURNING `+taskColumns,
		cur.ID, string(p.To), string(p.To.Phase()),
		p.To == StatusProvisioning, p.To.Terminal(),
		p.To == StatusCancelled, p.FailureCode, p.FailureMessage,
		truncateRunes(p.Reason, maxStatusReasonLen))
	next, err := scanTask(row)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}

	payloadMap := map[string]string{
		"from": string(cur.Status),
		"to":   string(p.To),
	}
	if p.Reason != "" {
		payloadMap["reason"] = p.Reason
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return Task{}, fmt.Errorf("marshal payload: %w", err)
	}
	if err := insertEvent(ctx, tx, eventAttemptID,
		TransitionEventType(p.To), source, payload, p.IdempotencyKey); err != nil {
		return Task{}, err
	}
	return next, nil
}

// nil revision (public Transition path) inserts a bare attempt that inherits the task's instructions
func insertRevisionAttempt(ctx context.Context, tx *sql.Tx, taskID string, number int, r *RevisionParams) (string, error) {
	var id string
	if r == nil {
		err := tx.QueryRowContext(ctx, `
			INSERT INTO task_attempts (task_id, attempt_number)
			VALUES ($1, $2) RETURNING id`, taskID, number).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("insert attempt: %w", err)
		}
		return id, nil
	}
	feedback, err := json.Marshal(r.Feedback)
	if err != nil {
		return "", fmt.Errorf("marshal feedback: %w", err)
	}
	if r.Feedback == nil {
		feedback = []byte(`[]`)
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO task_attempts
			(task_id, attempt_number, instructions, base_commit_sha,
			 requested_by_login, trigger_comment_id, feedback_json)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		taskID, number, r.Instructions, r.BaseCommitSHA,
		r.RequestedByLogin, r.TriggerCommentID, feedback).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert attempt: %w", err)
	}
	return id, nil
}

// callers hold task row lock so max+1 sequence cannot race
func insertEvent(ctx context.Context, tx *sql.Tx, attemptID, eventType, source string, payload []byte, idempotencyKey string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO activity_events
			(task_attempt_id, sequence_number, event_type, source,
			 payload_json, idempotency_key)
		VALUES ($1,
			(SELECT COALESCE(MAX(sequence_number), 0) + 1
			 FROM activity_events WHERE task_attempt_id = $1),
			$2, $3, $4, NULLIF($5, ''))`,
		attemptID, eventType, source, payload, idempotencyKey)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var (
		orgID, repoID, baseSHA, workBranch, provider, model sql.NullString
		policyID, requestedBy, failureCode, failureMsg      sql.NullString
		statusReason                                        sql.NullString
		issueNumber, commentID                              sql.NullInt64
		maxRuntime                                          sql.NullInt64
		maxCost                                             sql.NullFloat64
		startedAt, completedAt, cancelAt                    sql.NullTime
		status, phase                                       string
	)
	err := row.Scan(&t.ID, &orgID, &repoID, &t.SourceType, &issueNumber,
		&commentID, &t.Title, &t.Instructions, &status, &phase, &t.Priority,
		&t.BaseBranch, &baseSHA, &workBranch, &provider, &model, &policyID,
		&requestedBy, &maxRuntime, &maxCost, &startedAt, &completedAt,
		&cancelAt, &failureCode, &failureMsg, &statusReason, &t.CreatedAt,
		&t.UpdatedAt, &t.Version)
	if err != nil {
		return Task{}, err
	}
	t.Status = Status(status)
	t.Phase = Phase(phase)
	t.OrganizationID = nullStr(orgID)
	t.RepositoryID = nullStr(repoID)
	t.SourceIssueNumber = nullInt64(issueNumber)
	t.SourceCommentID = nullInt64(commentID)
	t.BaseCommitSHA = nullStr(baseSHA)
	t.WorkingBranch = nullStr(workBranch)
	t.AgentProvider = nullStr(provider)
	t.AgentModel = nullStr(model)
	t.PolicyID = nullStr(policyID)
	t.RequestedByUserID = nullStr(requestedBy)
	if maxRuntime.Valid {
		v := int(maxRuntime.Int64)
		t.MaxRuntimeSeconds = &v
	}
	if maxCost.Valid {
		t.MaxCostUSD = &maxCost.Float64
	}
	t.StartedAt = nullTime(startedAt)
	t.CompletedAt = nullTime(completedAt)
	t.CancelRequestedAt = nullTime(cancelAt)
	t.FailureCode = nullStr(failureCode)
	t.FailureMessage = nullStr(failureMsg)
	t.StatusReason = nullStr(statusReason)
	return t, nil
}

func nullStr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func nullTime(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
