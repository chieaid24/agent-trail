package dashboard

import (
	"errors"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/reposettings"
)

// http layer maps these to 404
var (
	ErrOrganizationNotFound = resourceNotFound("organization not found")
	ErrRepositoryNotFound   = resourceNotFound("repository not found")
	ErrRunnerNotFound       = resourceNotFound("runner not found")
)

type resourceNotFound string

func (e resourceNotFound) Error() string { return string(e) }

// http layer maps this to 400
var ErrInvalidSettings = errors.New("invalid repository settings")

type Organization struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Slug                   string    `json:"slug"`
	GitHubAccountLogin     string    `json:"github_account_login"`
	GitHubAccountType      string    `json:"github_account_type"`
	RepositoryCount        int       `json:"repository_count"`
	EnabledRepositoryCount int       `json:"enabled_repository_count"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type RepositorySettings = reposettings.Settings

// nil fields keep their stored value
type RepositorySettingsPatch struct {
	MaxAttempts *int
}

type Repository struct {
	ID                 string             `json:"id"`
	OrganizationID     string             `json:"organization_id"`
	GitHubRepositoryID int64              `json:"github_repository_id"`
	Owner              string             `json:"owner"`
	Name               string             `json:"name"`
	FullName           string             `json:"full_name"`
	DefaultBranch      string             `json:"default_branch"`
	IsPrivate          bool               `json:"is_private"`
	IsEnabled          bool               `json:"is_enabled"`
	Settings           RepositorySettings `json:"settings"`
	ActiveTaskCount    int                `json:"active_task_count"`
	RecentTaskCount    int                `json:"recent_task_count"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

type TaskSummary struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	Status            string     `json:"status"`
	Phase             string     `json:"phase"`
	SourceIssueNumber *int64     `json:"source_issue_number"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	FailureMessage    *string    `json:"failure_message"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type RepositoryMetrics struct {
	TotalTasks          int      `json:"total_tasks"`
	ActiveTasks         int      `json:"active_tasks"`
	CompletedTasks      int      `json:"completed_tasks"`
	FailedTasks         int      `json:"failed_tasks"`
	CompletionRate      *float64 `json:"completion_rate"`
	MedianRuntimeMillis *int64   `json:"median_runtime_millis"`
}

type RepositoryDetail struct {
	Repository
	Metrics     RepositoryMetrics `json:"metrics"`
	ActiveTasks []TaskSummary     `json:"active_tasks"`
	RecentTasks []TaskSummary     `json:"recent_tasks"`
}

type ResourceUsage struct {
	CPUPercent    *float64 `json:"cpu_percent"`
	MemoryPercent *float64 `json:"memory_percent"`
	DiskPercent   *float64 `json:"disk_percent"`
}

type Runner struct {
	ID              string            `json:"id"`
	Type            string            `json:"runner_type"`
	HostnameOrPod   string            `json:"hostname_or_pod"`
	Status          string            `json:"status"`
	Capacity        int               `json:"capacity"`
	ActiveTaskCount int               `json:"active_task_count"`
	Labels          map[string]string `json:"labels"`
	Resources       ResourceUsage     `json:"resources"`
	LastHeartbeatAt time.Time         `json:"last_heartbeat_at"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type RunnerDetail struct {
	Runner
	CurrentTasks   []TaskSummary `json:"current_tasks"`
	RecentFailures []TaskSummary `json:"recent_failures"`
}
