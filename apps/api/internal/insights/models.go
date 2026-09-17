// per-task execution read model built from task records and persisted spans; nothing is inferred from the clock
package insights

import "time"

// span names and event types that define each duration
const (
	spanAttempt      = "runner.attempt"
	spanProvisioning = "runner.provisioning"
	spanAgentSession = "agent.session"
	spanValidation   = "validation.run"

	eventPublishing     = "task.publishing"
	eventAwaitingReview = "task.awaiting_review"
	eventCostUpdate     = "agent.cost_update"
)

type ValidationSummary struct {
	Total    int `json:"total"`
	Passed   int `json:"passed"`
	Failed   int `json:"failed"`
	TimedOut int `json:"timed_out"`
	Error    int `json:"error"`
	Trusted  int `json:"trusted"`
}

type CostSummary struct {
	TotalUSD    float64 `json:"total_usd"`
	UpdateCount int     `json:"update_count"`
}

// nil durations mean a boundary is missing, never zero
type AttemptInsight struct {
	AttemptID      string             `json:"attempt_id"`
	AttemptNumber  int                `json:"attempt_number"`
	Status         string             `json:"status"`
	StartedAt      *time.Time         `json:"started_at"`
	CompletedAt    *time.Time         `json:"completed_at"`
	FailureCode    *string            `json:"failure_code"`
	QueueWaitMS    *int64             `json:"queue_wait_ms"`
	ProvisioningMS *int64             `json:"provisioning_ms"`
	AgentSessionMS *int64             `json:"agent_session_ms"`
	ValidationMS   *int64             `json:"validation_ms"`
	PublishingMS   *int64             `json:"publishing_ms"`
	TotalRuntimeMS *int64             `json:"total_runtime_ms"`
	EventCount     int                `json:"event_count"`
	Validation     *ValidationSummary `json:"validation"`
	Cost           *CostSummary       `json:"cost"`
}

// runtime is nil unless every attempt has one; cost sums only reporting attempts
type OverallInsight struct {
	AttemptCount   int                `json:"attempt_count"`
	TotalRuntimeMS *int64             `json:"total_runtime_ms"`
	EventCount     int                `json:"event_count"`
	Validation     *ValidationSummary `json:"validation"`
	Cost           *CostSummary       `json:"cost"`
}

type TaskInsights struct {
	TaskID     string           `json:"task_id"`
	TaskStatus string           `json:"task_status"`
	Overall    OverallInsight   `json:"overall"`
	Attempts   []AttemptInsight `json:"attempts"`
}
