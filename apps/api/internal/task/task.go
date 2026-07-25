package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Task mirrors the tasks table (docs/architecture/data-model.md). JSON tags
// are the API wire shape.
type Task struct {
	ID                string     `json:"id"`
	OrganizationID    *string    `json:"organization_id"`
	RepositoryID      *string    `json:"repository_id"`
	SourceType        string     `json:"source_type"`
	SourceIssueNumber *int64     `json:"source_issue_number"`
	SourceCommentID   *int64     `json:"source_comment_id"`
	Title             string     `json:"title"`
	Instructions      string     `json:"instructions"`
	Status            Status     `json:"status"`
	Phase             Phase      `json:"phase"`
	Priority          int        `json:"priority"`
	BaseBranch        string     `json:"base_branch"`
	BaseCommitSHA     *string    `json:"base_commit_sha"`
	WorkingBranch     *string    `json:"working_branch"`
	AgentProvider     *string    `json:"agent_provider"`
	AgentModel        *string    `json:"agent_model"`
	PolicyID          *string    `json:"policy_id"`
	RequestedByUserID *string    `json:"requested_by_user_id"`
	MaxRuntimeSeconds *int       `json:"max_runtime_seconds"`
	MaxCostUSD        *float64   `json:"max_cost_usd"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at"`
	FailureCode       *string    `json:"failure_code"`
	FailureMessage    *string    `json:"failure_message"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Version           int64      `json:"version"`
}

// Event is one row of the append-only activity timeline. AttemptNumber is
// joined from the owning attempt for API convenience.
type Event struct {
	ID              string          `json:"id"`
	TaskAttemptID   string          `json:"task_attempt_id"`
	AttemptNumber   int             `json:"attempt_number"`
	SequenceNumber  int64           `json:"sequence_number"`
	EventType       string          `json:"event_type"`
	Source          string          `json:"source"`
	Timestamp       time.Time       `json:"timestamp"`
	Payload         json.RawMessage `json:"payload"`
	RedactionStatus string          `json:"redaction_status"`
	CreatedAt       time.Time       `json:"created_at"`
}

// CreateParams are the caller-supplied fields of a new task.
type CreateParams struct {
	Title             string
	Instructions      string
	Priority          int
	BaseBranch        string // defaults to "main"
	MaxRuntimeSeconds *int
	MaxCostUSD        *float64
	// GitHub-sourced tasks (source_type github_issue) carry their origin;
	// API-created tasks leave these zero.
	SourceType        string // defaults to "api"
	SourceIssueNumber *int64
	SourceCommentID   *int64
	OrganizationID    *string
	RepositoryID      *string
}

// ListParams filter and bound List.
type ListParams struct {
	Status Status // zero value = all statuses
	Limit  int    // 0 = default
}

// TransitionParams describe one transition message.
type TransitionParams struct {
	To             Status
	Source         string // event source: api, system, runner, agent
	Reason         string // recorded in the event payload when set
	FailureCode    string // stored on failed/timed_out transitions
	FailureMessage string
	// IdempotencyKey identifies the message: a replay with a key the task
	// has already recorded is a no-op instead of a double transition.
	IdempotencyKey string
	// ExpectedVersion, when non-zero, must match the task's current version
	// (optimistic concurrency).
	ExpectedVersion int64
}

// ErrNotFound is returned when the task id does not exist.
var ErrNotFound = errors.New("task not found")

// ErrActiveTaskExists rejects a second active task for one GitHub issue
// (unique index tasks_one_active_per_issue_idx).
var ErrActiveTaskExists = errors.New("issue already has an active task")

// InvalidTransitionError rejects an edge the state machine does not allow.
type InvalidTransitionError struct {
	From, To Status
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid transition %s -> %s", e.From, e.To)
}

// VersionConflictError rejects a transition whose expected version is stale.
type VersionConflictError struct {
	Expected, Actual int64
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict: expected %d, task is at %d",
		e.Expected, e.Actual)
}
