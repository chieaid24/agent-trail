package conflict

import (
	"context"
	"fmt"
)

const FakeProvider = "fake"

type SemanticRequest struct {
	TaskID         string
	TaskTitle      string
	TaskDiff       string
	OtherTaskID    string
	OtherTaskTitle string
	OtherTaskDiff  string
}

type SemanticVerdict struct {
	Conflicts   bool     `json:"conflicts"`
	Severity    Severity `json:"severity"`
	Explanation string   `json:"explanation"`
	Evidence    []string `json:"evidence"`
}

type SemanticAssessor interface {
	Assess(ctx context.Context, req SemanticRequest) (SemanticVerdict, error)
}

type SemanticOptions struct {
	Enabled  bool
	Provider string
	APIKey   string
	Model    string
}

func NewSemantic(opts SemanticOptions) (SemanticAssessor, error) {
	if !opts.Enabled {
		return nil, nil
	}
	switch opts.Provider {
	case FakeProvider:
		return FakeSemantic{}, nil
	case AnthropicProvider:
		if opts.APIKey == "" {
			return nil, nil
		}
		return NewAnthropicSemantic(AnthropicOptions{APIKey: opts.APIKey, Model: opts.Model})
	default:
		return nil, fmt.Errorf("conflict: unknown semantic provider %q", opts.Provider)
	}
}

func validateVerdict(v SemanticVerdict) error {
	if !v.Conflicts {
		return nil
	}
	switch v.Severity {
	case SeverityLow, SeverityMedium, SeverityHigh:
	default:
		return fmt.Errorf("conflict: semantic severity %q is invalid", v.Severity)
	}
	if v.Explanation == "" {
		return fmt.Errorf("conflict: semantic explanation is required")
	}
	if len(v.Evidence) == 0 {
		return fmt.Errorf("conflict: semantic evidence is required")
	}
	return nil
}
