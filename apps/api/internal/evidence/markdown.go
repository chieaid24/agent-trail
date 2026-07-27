package evidence

import (
	"fmt"
	"strings"
)

// Markdown renders the report as the human-readable evidence summary
// (docs/architecture/evidence.md). Trusted results and agent claims are
// kept visibly apart: only platform-executed checks appear under
// "Verified by Agent Trail".
func Markdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Evidence: %s\n\n", r.Task.Title)

	fmt.Fprintf(&b, "Task `%s`", r.Task.ID)
	if r.Execution.AgentProvider != "" {
		fmt.Fprintf(&b, ", agent provider `%s`", r.Execution.AgentProvider)
	}
	if r.Execution.DurationSeconds != nil {
		fmt.Fprintf(&b, ", ran %ds", *r.Execution.DurationSeconds)
	}
	b.WriteString(".\n")

	trusted := make([]CheckResult, 0, len(r.Validation))
	claimed := make([]CheckResult, 0)
	for _, v := range r.Validation {
		if v.TrustedExecution {
			trusted = append(trusted, v)
		} else {
			claimed = append(claimed, v)
		}
	}

	b.WriteString("\n## Verified by Agent Trail\n\n")
	if len(trusted) == 0 {
		b.WriteString("No trusted checks ran.\n")
	} else {
		b.WriteString("Checks the platform executed in the workspace after editing ended.\n\n")
		b.WriteString("| Check | Category | Result | Exit code | Duration |\n")
		b.WriteString("|---|---|---|---:|---:|\n")
		for _, v := range trusted {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %dms |\n",
				v.Name, v.Category, v.Status, exitCodeCell(v.ExitCode), v.DurationMS)
		}
	}

	if len(claimed) > 0 {
		b.WriteString("\n## Agent-reported (not independently verified)\n\n")
		b.WriteString("| Check | Claimed result | Claimed exit code |\n")
		b.WriteString("|---|---|---:|\n")
		for _, v := range claimed {
			fmt.Fprintf(&b, "| %s | %s | %s |\n",
				v.Name, v.Status, exitCodeCell(v.ExitCode))
		}
	}

	if len(r.Plan) > 0 {
		b.WriteString("\n## Plan\n\n")
		for i, step := range r.Plan {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
	}

	b.WriteString("\n## Changes\n\n")
	if len(r.Changes.Files) == 0 {
		fmt.Fprintf(&b, "%d files changed.\n", r.Changes.FilesChanged)
	} else {
		for _, f := range r.Changes.Files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
	}

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
	return b.String()
}

func exitCodeCell(code *int) string {
	if code == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *code)
}
