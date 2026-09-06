package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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

type CreateParams struct {
	Title             string
	Instructions      string
	Priority          int
	BaseBranch        string
	MaxRuntimeSeconds *int
	MaxCostUSD        *float64
	SourceType        string
	SourceIssueNumber *int64
	SourceCommentID   *int64
	OrganizationID    *string
	RepositoryID      *string
}

type ListParams struct {
	Status Status // zero value = all statuses
	Limit  int    // 0 = default
}

type TransitionParams struct {
	To             Status
	Source         string // api, system, runner, agent
	Reason         string
	FailureCode    string
	FailureMessage string
	// replay with an already-recorded key is a no-op, not a double transition
	IdempotencyKey  string
	ExpectedVersion int64
}

var ErrNotFound = errors.New("task not found")

var ErrAttemptNotFound = errors.New("task attempt not found")

var ErrActiveTaskExists = errors.New("issue already has an active task")

type InvalidTransitionError struct {
	From, To Status
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid transition %s -> %s", e.From, e.To)
}

type VersionConflictError struct {
	Expected, Actual int64
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict: expected %d, task is at %d",
		e.Expected, e.Actual)
}
