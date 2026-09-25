package evidence

import (
	"strings"
	"testing"
)

func TestPRBodyRendersTemplate(t *testing.T) {
	issue := int64(42)
	exit := 0
	r := Report{
		SchemaVersion: SchemaVersion,
		Task: TaskInfo{
			ID: "task-1", SourceIssue: &issue, Title: "Add rotation",
		},
		Execution: Execution{
			AgentProvider: "fake",
			BaseCommit:    "1111111111111111111111111111111111111111",
		},
		Plan:    []string{"Inspect service", "Add tests"},
		Changes: Changes{FilesChanged: 2},
		Validation: []CheckResult{
			{Name: "unit-tests", Category: "unit_test", Status: "passed",
				TrustedExecution: true, ExitCode: &exit, DurationMS: 20},
			{Name: "agent claim", Status: "passed"},
		},
		Risks: []string{"sessions may reset"},
	}
	body := PRBody(r, "2222222222222222222222222222222222222222", nil)

	for _, want := range []string{
		"## Agent Trail task",
		"Closes #42",
		"## Summary",
		"## Verified by Agent Trail",
		"| unit-tests | unit_test | passed | 0 | 20ms |",
		"## Agent-reported (not independently verified)",
		"## Risks",
		"## Execution metadata",
		"- Base commit: `1111111111111111111111111111111111111111`",
		"- Final commit: `2222222222222222222222222222222222222222`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(strings.Split(body, "## Verified by Agent Trail")[1],
		"| agent claim | passed") &&
		!strings.Contains(body, "## Agent-reported") {
		t.Fatal("agent claim leaked into the verified table")
	}
}

func TestPRBodyWithoutIssueOmitsCloses(t *testing.T) {
	body := PRBody(Report{Task: TaskInfo{ID: "task-2", Title: "x"}}, "", nil)
	if strings.Contains(body, "Closes #") {
		t.Fatalf("body has Closes line without a source issue:\n%s", body)
	}
	if strings.Contains(body, "## Attempts") {
		t.Fatalf("body has an attempts section without history:\n%s", body)
	}
}

func TestPRBodyListsAttemptsHistoryBelowEvidence(t *testing.T) {
	cost := 0.0043
	history := []AttemptHistory{
		{Number: 1, Status: "superseded", BaseCommit: "1111111111111111111111111111111111111111",
			FinalCommit: "2222222222222222222222222222222222222222", Validation: "passed", CostUSD: &cost},
		{Number: 2, Status: "active", BaseCommit: "2222222222222222222222222222222222222222"},
	}
	body := PRBody(Report{Task: TaskInfo{ID: "task-3", Title: "x"}}, "", history)
	for _, want := range []string{
		"## Attempts",
		"| Attempt | Base commit | Final commit | Validation | Reported cost |",
		"| 1 | `1111111111111111111111111111111111111111` | `2222222222222222222222222222222222222222` | passed | $0.0043 |",
		"| 2 | `2222222222222222222222222222222222222222` | - | - | - |",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "## Attempts") < strings.Index(body, "## Execution metadata") {
		t.Fatalf("attempts history must follow the evidence report:\n%s", body)
	}
}
