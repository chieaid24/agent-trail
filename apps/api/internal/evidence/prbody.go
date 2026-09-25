package evidence

import (
	"fmt"
	"strconv"
	"strings"
)

// finalcommit measured at publish time; report is generated before the commit exists.
// the report always belongs to the latest attempt; history lists every attempt below it
func PRBody(r Report, finalCommit string, history []AttemptHistory) string {
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

	if len(history) > 0 {
		b.WriteString("\n## Attempts\n\n")
		b.WriteString("| Attempt | Base commit | Final commit | Validation | Reported cost |\n")
		b.WriteString("|---:|---|---|---|---:|\n")
		for _, h := range history {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", h.Number,
				shaCell(h.BaseCommit), shaCell(h.FinalCommit),
				textCell(h.Validation), costCell(h.CostUSD))
		}
	}
	return b.String()
}

func shaCell(sha string) string {
	if sha == "" {
		return "-"
	}
	return "`" + sha + "`"
}

func textCell(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// provider-reported, never billing; shortest exact decimal so small costs stay visible
func costCell(usd *float64) string {
	if usd == nil {
		return "-"
	}
	return "$" + strconv.FormatFloat(*usd, 'f', -1, 64)
}
