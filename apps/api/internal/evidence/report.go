// only measured facts enter a report; unmeasured fields are omitted, never invented
package evidence

import (
	"fmt"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

const SchemaVersion = 1

type Report struct {
	SchemaVersion int           `json:"schema_version"`
	Task          TaskInfo      `json:"task"`
	Execution     Execution     `json:"execution"`
	Plan          []string      `json:"plan,omitempty"`
	Changes       Changes       `json:"changes"`
	Validation    []CheckResult `json:"validation"`
	Risks         []string      `json:"risks,omitempty"`
	Unverified    []string      `json:"unverified,omitempty"`
}

type TaskInfo struct {
	ID          string `json:"id"`
	SourceIssue *int64 `json:"source_issue,omitempty"`
	Title       string `json:"title"`
	RequestedBy string `json:"requested_by,omitempty"`
}

type Execution struct {
	AgentProvider   string `json:"agent_provider,omitempty"`
	AgentModel      string `json:"agent_model,omitempty"`
	BaseCommit      string `json:"base_commit,omitempty"`
	FinalCommit     string `json:"final_commit,omitempty"`
	DurationSeconds *int64 `json:"duration_seconds,omitempty"`
}

type Changes struct {
	FilesChanged int      `json:"files_changed"`
	Files        []string `json:"files,omitempty"`
}

// trustedexecution=true means the platform measured it; false means the agent merely claimed it
type CheckResult struct {
	Name             string `json:"name"`
	Category         string `json:"category,omitempty"`
	Status           string `json:"status"`
	TrustedExecution bool   `json:"trusted_execution"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
	Summary          string `json:"summary,omitempty"`
}

type Params struct {
	Task            task.Task
	AgentProvider   string
	DurationSeconds *int64
	Plan            []string
	FilesChanged    []string
	Trusted         []validation.StoredResult
	AgentReported   []CheckResult
	ValidationNote  string
	Unverified      []string
}

func Generate(p Params) Report {
	r := Report{
		SchemaVersion: SchemaVersion,
		Task: TaskInfo{
			ID:          p.Task.ID,
			SourceIssue: p.Task.SourceIssueNumber,
			Title:       p.Task.Title,
		},
		Execution: Execution{
			AgentProvider:   p.AgentProvider,
			DurationSeconds: p.DurationSeconds,
		},
		Plan: p.Plan,
		Changes: Changes{
			FilesChanged: len(p.FilesChanged),
			Files:        p.FilesChanged,
		},
		Validation: []CheckResult{},
	}
	if p.Task.RequestedByUserID != nil {
		r.Task.RequestedBy = *p.Task.RequestedByUserID
	}
	if p.Task.AgentModel != nil {
		r.Execution.AgentModel = *p.Task.AgentModel
	}
	if p.Task.BaseCommitSHA != nil {
		r.Execution.BaseCommit = *p.Task.BaseCommitSHA
	}

	for _, t := range p.Trusted {
		r.Validation = append(r.Validation, CheckResult{
			Name:             t.Name,
			Category:         t.Category,
			Status:           string(t.Status),
			TrustedExecution: t.TrustedExecution,
			ExitCode:         t.ExitCode,
			DurationMS:       t.DurationMS,
			Summary:          t.Summary,
		})
	}
	r.Validation = append(r.Validation, p.AgentReported...)

	if p.ValidationNote != "" {
		r.Unverified = append(r.Unverified,
			"trusted validation did not run: "+p.ValidationNote)
	}
	if len(p.AgentReported) > 0 {
		r.Unverified = append(r.Unverified, fmt.Sprintf(
			"%d agent-reported check(s) were not independently executed",
			len(p.AgentReported)))
	}
	r.Unverified = append(r.Unverified, p.Unverified...)
	return r
}
