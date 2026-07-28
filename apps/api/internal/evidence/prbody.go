package evidence

import (
	"fmt"
	"strings"
)

// PRBody renders the draft pull request body from the stored report
// (docs/architecture/evidence.md "Pull-Request Body"). finalCommit is the
// published commit, measured at publish time (the report is generated before
// the commit exists). Only measured facts appear; no prompts, no secrets.
func PRBody(r Report, finalCommit string) string {
	var b strings.Builder
	b.WriteString("## Agent Trail task\n\n")
	if r.Task.SourceIssue != nil {
		fmt.Fprintf(&b, "Closes #%d\n\n", *r.Task.SourceIssue)
	}
	fmt.Fprintf(&b, "Task `%s`.\n", r.Task.ID)

	b.WriteString("\n## Summary\n\n")
	b.WriteString(r.Task.Title + "\n")

	if len(r.Plan) > 0 {
		b.WriteString("\n## Implementation\n\n")
		for _, step := range r.Plan {
			fmt.Fprintf(&b, "- %s\n", step)
		}
	}

	writeValidationSections(&b, r)

	if len(r.Risks) > 0 {
		b.WriteString("\n## Risks\n\n")
		for _, risk := range r.Risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}
	if len(r.Unverified) > 0 {
		b.WriteString("\n## Unverified\n\n")
		for _, u := range r.Unverified {
			fmt.Fprintf(&b, "- %s\n", u)
		}
	}

	b.WriteString("\n## Execution metadata\n\n")
	if r.Execution.BaseCommit != "" {
		fmt.Fprintf(&b, "- Base commit: `%s`\n", r.Execution.BaseCommit)
	}
	if finalCommit != "" {
		fmt.Fprintf(&b, "- Final commit: `%s`\n", finalCommit)
	}
	if r.Execution.AgentProvider != "" {
		fmt.Fprintf(&b, "- Agent provider: %s\n", r.Execution.AgentProvider)
	}
	if r.Execution.AgentModel != "" {
		fmt.Fprintf(&b, "- Agent model: %s\n", r.Execution.AgentModel)
	}
	if r.Execution.DurationSeconds != nil {
		fmt.Fprintf(&b, "- Duration: %ds\n", *r.Execution.DurationSeconds)
	}
	fmt.Fprintf(&b, "- Changes: %d files\n", r.Changes.FilesChanged)
	return b.String()
}
