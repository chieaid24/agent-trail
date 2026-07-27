package evidence

import (
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

func intPtr(v int) *int { return &v }

func int64Ptr(v int64) *int64 { return &v }

func testParams() Params {
	model := "test-model"
	issue := int64(42)
	return Params{
		Task: task.Task{
			ID:                "11111111-1111-1111-1111-111111111111",
			Title:             "Add refresh-token rotation",
			SourceIssueNumber: &issue,
			AgentModel:        &model,
		},
		AgentProvider:   "fake",
		DurationSeconds: int64Ptr(12),
		Plan:            []string{"Inspect auth service", "Add rotation"},
		FilesChanged:    []string{"auth.go"},
		Trusted: []validation.StoredResult{
			{Name: "unit", Category: "unit_test", Status: validation.StatusFailed,
				ExitCode: intPtr(1), DurationMS: 340, Summary: "3 failed",
				TrustedExecution: true},
		},
		AgentReported: []CheckResult{
			{Name: "make test", Status: "passed", TrustedExecution: false,
				ExitCode: intPtr(0)},
		},
	}
}

// TestGenerateKeepsTrustedAndClaimedApart: the report never promotes an
// agent claim to a trusted result, and a trusted failure stays failed.
func TestGenerateKeepsTrustedAndClaimedApart(t *testing.T) {
	r := Generate(testParams())
	if r.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d", r.SchemaVersion)
	}
	if r.Task.SourceIssue == nil || *r.Task.SourceIssue != 42 {
		t.Fatalf("source_issue = %v", r.Task.SourceIssue)
	}
	if r.Execution.AgentProvider != "fake" || r.Execution.AgentModel != "test-model" {
		t.Fatalf("execution = %+v", r.Execution)
	}
	if len(r.Validation) != 2 {
		t.Fatalf("validation entries = %d, want 2", len(r.Validation))
	}
	trusted, claimed := r.Validation[0], r.Validation[1]
	if !trusted.TrustedExecution || trusted.Status != "failed" ||
		trusted.ExitCode == nil || *trusted.ExitCode != 1 {
		t.Fatalf("trusted entry = %+v", trusted)
	}
	if claimed.TrustedExecution {
		t.Fatalf("claimed entry gained trusted_execution: %+v", claimed)
	}
	if len(r.Unverified) == 0 {
		t.Fatal("agent claims present but nothing marked unverified")
	}
	if r.Changes.FilesChanged != 1 {
		t.Fatalf("changes = %+v", r.Changes)
	}
}

func TestGenerateRecordsValidationNote(t *testing.T) {
	p := Params{Task: task.Task{ID: "x", Title: "t"},
		ValidationNote: "no validation file at .agent-trail/validation.yaml"}
	r := Generate(p)
	if len(r.Validation) != 0 {
		t.Fatalf("validation = %+v, want empty", r.Validation)
	}
	found := false
	for _, u := range r.Unverified {
		if strings.Contains(u, "no validation file") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unverified = %v, want the note surfaced", r.Unverified)
	}
}

// TestMarkdownSeparatesTrustedFromClaimed: trusted results are visibly
// distinct - only platform-run checks sit under "Verified by Agent Trail".
func TestMarkdownSeparatesTrustedFromClaimed(t *testing.T) {
	md := Markdown(Generate(testParams()))

	verified := strings.Index(md, "## Verified by Agent Trail")
	claimed := strings.Index(md, "## Agent-reported (not independently verified)")
	if verified == -1 || claimed == -1 {
		t.Fatalf("markdown missing sections:\n%s", md)
	}
	verifiedSection := md[verified:claimed]
	if !strings.Contains(verifiedSection, "| unit | unit_test | failed | 1 | 340ms |") {
		t.Fatalf("verified section lacks the failed row:\n%s", verifiedSection)
	}
	if strings.Contains(verifiedSection, "make test") {
		t.Fatalf("claimed check leaked into the verified section:\n%s", verifiedSection)
	}
	if !strings.Contains(md[claimed:], "make test") {
		t.Fatalf("claimed section lacks the claim:\n%s", md[claimed:])
	}

	for _, want := range []string{"## Plan", "## Changes", "## Unverified", "`auth.go`"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestMarkdownWithoutTrustedChecks(t *testing.T) {
	md := Markdown(Generate(Params{Task: task.Task{ID: "x", Title: "t"},
		ValidationNote: "the workspace was lost"}))
	if !strings.Contains(md, "No trusted checks ran.") {
		t.Fatalf("markdown = %s", md)
	}
	if !strings.Contains(md, "the workspace was lost") {
		t.Fatalf("markdown does not surface the note:\n%s", md)
	}
}
