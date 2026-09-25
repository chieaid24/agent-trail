package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const triggerCheckRunID = int64(4242)

// runs attempt 1 to awaiting_review and returns the pushed branch tip
func publishFirstAttempt(t *testing.T, f *publishFixture) (branch, tip string) {
	t.Helper()
	ctx := context.Background()
	if err := f.tasks.RecordTriggerCheckRun(ctx, f.task.ID, triggerCheckRunID); err != nil {
		t.Fatal(err)
	}
	if err := f.exec.Execute(ctx, f.runner.ID, f.claim(t)); err != nil {
		t.Fatal(err)
	}
	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusAwaitingReview || got.WorkingBranch == nil {
		t.Fatalf("attempt 1 ended at %s (branch %v)", got.Status, got.WorkingBranch)
	}
	branch = *got.WorkingBranch
	return branch, gitRun(t, f.origin, "rev-parse", "refs/heads/"+branch)
}

// the revise command's durable effect: attempt 2 queued from base with a queued trigger check run
func requestRevision(t *testing.T, f *publishFixture, base string, checkRunID int64) task.Attempt {
	t.Helper()
	ctx := context.Background()
	_, attempt, err := f.tasks.RequestRevision(ctx, f.task.ID, task.RevisionParams{
		Instructions:     "revise: add the second note",
		BaseCommitSHA:    base,
		RequestedByLogin: "alice",
		TriggerCommentID: 77,
		Feedback: []task.FeedbackItem{
			{Kind: "review_comment", Author: "alice", PostedAt: time.Now().UTC(), Location: "README.md:1"},
			{Kind: "revise_command", Author: "alice", PostedAt: time.Now().UTC()},
		},
		MaxAttempts:    5,
		IdempotencyKey: "revise:77",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.RecordTriggerCheckRun(ctx, f.task.ID, checkRunID); err != nil {
		t.Fatal(err)
	}
	return attempt
}

// a reviewer pushes to the working branch from their own clone
func pushHumanCommit(t *testing.T, origin, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "clone", "-q", "--branch", branch, origin, "clone")
	clone := filepath.Join(dir, "clone")
	if err := os.WriteFile(filepath.Join(clone, "HUMAN.md"), []byte("review fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "add", "-A")
	gitRun(t, clone, "commit", "-q", "-m", "human follow-up")
	gitRun(t, clone, "push", "-q", "origin", branch)
	return gitRun(t, clone, "rev-parse", "HEAD")
}

func TestRevisionContinuesBranchAndPublishesSummary(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	branch, tip := publishFirstAttempt(t, f)
	if got := f.fake.updatesFor(triggerCheckRunID); len(got) != 1 || got[0] != "success" {
		t.Fatalf("attempt 1 trigger check run updates = %v, want [success]", got)
	}
	revision := requestRevision(t, f, tip, 4343)

	c := f.claim(t)
	if c.AttemptID != revision.ID || c.AttemptNumber != 2 || c.Instructions != "revise: add the second note" {
		t.Fatalf("claim = %+v, want revision attempt", c)
	}
	if err := f.exec.Execute(ctx, f.runner.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusAwaitingReview {
		t.Fatalf("task = %s, want awaiting_review", got.Status)
	}
	finalSHA, prNumber := f.attemptRow(t, c.AttemptID)
	if !finalSHA.Valid || finalSHA.String == tip || !prNumber.Valid || prNumber.Int64 != 1 {
		t.Fatalf("attempt 2 row = final %+v pr %+v", finalSHA, prNumber)
	}
	// same branch, one new commit on top of the previous attempt, never rewritten
	if pushed := gitRun(t, f.origin, "rev-parse", "refs/heads/"+branch); pushed != finalSHA.String {
		t.Fatalf("origin branch at %s, want %s", pushed, finalSHA.String)
	}
	if parent := gitRun(t, f.origin, "rev-parse", finalSHA.String+"^"); parent != tip {
		t.Fatalf("revision parent = %s, want previous tip %s", parent, tip)
	}
	notes := gitRun(t, f.origin, "show", finalSHA.String+":"+agent.FixtureFile)
	if strings.Count(notes, "## Fake agent run") != 2 || !strings.Contains(notes, "revise: add the second note") {
		t.Fatalf("branch does not carry both attempts:\n%s", notes)
	}
	if msg := gitRun(t, f.origin, "log", "-1", "--format=%s", finalSHA.String); !strings.Contains(msg, "(attempt 2)") {
		t.Fatalf("revision commit subject = %q", msg)
	}

	// one pull request, body refreshed with the latest evidence and both attempts
	if f.fake.prsCreated != 1 || f.fake.prsUpdated == 0 {
		t.Fatalf("prs created = %d updated = %d", f.fake.prsCreated, f.fake.prsUpdated)
	}
	body := f.fake.prBodies[1]
	for _, want := range []string{
		"Final commit: `" + finalSHA.String + "`",
		"Base commit: `" + tip + "`",
		"## Attempts",
		"| 1 | `" + f.baseSHA + "` | `" + tip + "` | passed |",
		"| 2 | `" + tip + "` | `" + finalSHA.String + "` | passed |",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pr body missing %q:\n%s", want, body)
		}
	}

	// per-attempt evidence, validation, and check runs
	report, err := f.exec.Evidence.GetForAttempt(ctx, c.AttemptID)
	if err != nil || report.AttemptNumber != 2 {
		t.Fatalf("attempt 2 evidence = %+v, err = %v", report, err)
	}
	results, err := f.exec.Validations.ListForAttempt(ctx, c.AttemptID)
	if err != nil || len(results) != 1 || results[0].Name != "smoke" {
		t.Fatalf("attempt 2 validations = %+v, err = %v", results, err)
	}
	var finalChecks []string
	for _, check := range f.fake.checks {
		finalChecks = append(finalChecks, check.ExternalID+"@"+check.HeadSHA)
	}
	if len(f.fake.checks) != 2 || f.fake.checks[1].ExternalID != c.AttemptID ||
		f.fake.checks[1].HeadSHA != finalSHA.String || f.fake.checks[1].Conclusion != "success" {
		t.Fatalf("checks = %v", finalChecks)
	}
	if got := f.fake.updatesFor(4343); len(got) != 1 || got[0] != "success" {
		t.Fatalf("revision trigger check run updates = %v, want [success]", got)
	}
	attempt, err := f.tasks.Attempt(ctx, c.AttemptID)
	if err != nil || attempt.TriggerCheckRunCompletedAt == nil {
		t.Fatalf("trigger check run completion not recorded: %+v, %v", attempt, err)
	}

	// one revision summary on the pull request, no second issue comment
	if len(f.fake.comments) != 2 {
		t.Fatalf("comments = %v, want issue comment + revision summary", f.fake.comments)
	}
	// a recovered owner replaying publish must not post the summary again
	rc, err := f.exec.Repos.RepositoryContextByID(ctx, *f.task.RepositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.exec.postRevisionSummary(ctx, c, rc, attempt, 1, finalSHA.String, evidence.Report{}); err != nil {
		t.Fatal(err)
	}
	if len(f.fake.comments) != 2 {
		t.Fatalf("replayed publish posted another summary: %v", f.fake.comments)
	}
	summary := f.fake.comments[1]
	for _, want := range []string{
		"published revision attempt 2", "Final commit: `" + finalSHA.String + "`",
		"Validation: passed (1 trusted checks)", "Review feedback given: 2 item(s)",
		"inline review comment by @alice", "on README.md:1", "revise command by @alice",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("revision summary missing %q:\n%s", want, summary)
		}
	}

	assertSubsequence(t, attemptTimeline(t, f, c.AttemptID), []string{
		"task.queued", "task.provisioning", "workspace.ready", "agent.started",
		"validation.completed", "evidence.generated", "task.publishing",
		"commit.created", "branch.pushed", "pull_request.updated",
		"github.check_run.created", "github.check_run.updated",
		"github.comment.posted", "cleanup.completed", "task.awaiting_review",
	})
	entries, err := os.ReadDir(filepath.Join(f.wsRoot, "workspaces"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspaces not cleaned: %v, %v", entries, err)
	}
}

func attemptTimeline(t *testing.T, f *publishFixture, attemptID string) []string {
	t.Helper()
	events, err := f.tasks.Events(context.Background(), f.task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range events {
		if e.TaskAttemptID == attemptID {
			types = append(types, e.EventType)
		}
	}
	return types
}

func TestRevisionFailsWhenBranchTipMovedAfterTrigger(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	branch, tip := publishFirstAttempt(t, f)
	requestRevision(t, f, tip, 4343)
	human := pushHumanCommit(t, f.origin, branch)

	c := f.claim(t)
	err := f.exec.Execute(ctx, f.runner.ID, c)
	if !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute err = %v, want ErrAttemptFailed", err)
	}
	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusFailed || got.FailureCode == nil || *got.FailureCode != "base_mismatch" {
		t.Fatalf("task = %s failure = %v, want failed/base_mismatch", got.Status, got.FailureCode)
	}
	if pushed := gitRun(t, f.origin, "rev-parse", "refs/heads/"+branch); pushed != human {
		t.Fatalf("origin branch at %s, want the human commit %s untouched", pushed, human)
	}
	if got := f.fake.updatesFor(4343); len(got) != 1 || got[0] != "failure" {
		t.Fatalf("trigger check run updates = %v, want [failure]", got)
	}
	if f.fake.prsUpdated != 0 || len(f.fake.comments) != 1 {
		t.Fatalf("failed revision touched github: updates=%d comments=%v", f.fake.prsUpdated, f.fake.comments)
	}
	if m := f.exec.Workspaces; m.WorkspaceExists(c.AttemptID) {
		t.Fatal("refused revision left a workspace behind")
	}
}

// pushes a human commit to origin while the agent session runs, so the push is no longer a fast-forward
type racingAdapter struct {
	inner  *agent.Fake
	t      *testing.T
	origin string
	branch string
}

func (r racingAdapter) Name() string                                { return "fake" }
func (r racingAdapter) ValidateConfiguration(context.Context) error { return nil }
func (r racingAdapter) Start(ctx context.Context, req agent.Request) (agent.Session, error) {
	pushHumanCommit(r.t, r.origin, r.branch)
	return r.inner.Start(ctx, req)
}

func TestRevisionNeverForcePushesOverHumanCommits(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	branch, tip := publishFirstAttempt(t, f)
	requestRevision(t, f, tip, 4343)
	f.exec.Adapter = racingAdapter{inner: agent.NewFake(), t: t, origin: f.origin, branch: branch}

	c := f.claim(t)
	err := f.exec.Execute(ctx, f.runner.ID, c)
	if !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute err = %v, want ErrAttemptFailed", err)
	}
	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureCode == nil || *got.FailureCode != "publish_failed" ||
		got.FailureMessage == nil || !strings.HasPrefix(*got.FailureMessage, "push:") {
		t.Fatalf("failure = %v %v, want publish_failed at push", got.FailureCode, got.FailureMessage)
	}
	if subject := gitRun(t, f.origin, "log", "-1", "--format=%s", "refs/heads/"+branch); subject != "human follow-up" {
		t.Fatalf("origin tip subject = %q, want the human commit preserved", subject)
	}
	if got := f.fake.updatesFor(4343); len(got) != 1 || got[0] != "failure" {
		t.Fatalf("trigger check run updates = %v, want [failure]", got)
	}
}

func TestCancelledTaskCompletesTriggerCheckRun(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	if err := f.tasks.RecordTriggerCheckRun(ctx, f.task.ID, triggerCheckRunID); err != nil {
		t.Fatal(err)
	}
	c := f.claim(t)
	if _, err := f.tasks.Cancel(ctx, f.task.ID, "operator cancelled"); err != nil {
		t.Fatal(err)
	}

	if err := f.exec.Execute(ctx, f.runner.ID, c); err == nil {
		t.Fatal("cancelled task executed")
	}
	if got := f.fake.updatesFor(triggerCheckRunID); len(got) != 1 || got[0] != "cancelled" {
		t.Fatalf("trigger check run updates = %v, want [cancelled]", got)
	}
	attempt, err := f.tasks.Attempt(ctx, c.AttemptID)
	if err != nil || attempt.TriggerCheckRunCompletedAt == nil {
		t.Fatalf("completion not recorded: %+v, %v", attempt, err)
	}
	// a second settlement (recovery replay) leaves the completed run alone
	if err := f.exec.settleTriggerCheckRun(ctx, f.exec.Logger, c, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.fake.updatesFor(triggerCheckRunID); len(got) != 1 {
		t.Fatalf("trigger check run updated again: %v", got)
	}
}

func TestRevisionWithoutChangesFailsOnThePullRequest(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	branch, tip := publishFirstAttempt(t, f)
	requestRevision(t, f, tip, 4343)
	f.exec.Adapter = stubAdapter{}

	c := f.claim(t)
	if err := f.exec.Execute(ctx, f.runner.ID, c); !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute err = %v, want ErrAttemptFailed", err)
	}
	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusFailed || got.FailureCode == nil || *got.FailureCode != "no_change" {
		t.Fatalf("task = %s failure = %v, want failed/no_change", got.Status, got.FailureCode)
	}
	if pushed := gitRun(t, f.origin, "rev-parse", "refs/heads/"+branch); pushed != tip {
		t.Fatalf("origin branch moved to %s", pushed)
	}
	last := f.fake.comments[len(f.fake.comments)-1]
	if len(f.fake.comments) != 2 || !strings.Contains(last, "no changes for revision attempt 2") {
		t.Fatalf("comments = %v", f.fake.comments)
	}
	if got := f.fake.updatesFor(4343); len(got) != 1 || got[0] != "neutral" {
		t.Fatalf("trigger check run updates = %v, want [neutral]", got)
	}
	_, prNumber := f.attemptRow(t, c.AttemptID)
	if !prNumber.Valid || prNumber.Int64 != 1 {
		t.Fatalf("no-change revision did not record its pull request: %+v", prNumber)
	}
}

func TestRecoveredRevisionWithoutPushedWorkFailsWorkspaceLost(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	branch, tip := publishFirstAttempt(t, f)
	revision := requestRevision(t, f, tip, 4343)

	// the first owner reached publishing, then died before pushing; its worktree is gone
	first := f.claim(t)
	if first.AttemptID != revision.ID {
		t.Fatalf("claimed %s, want revision attempt %s", first.AttemptID, revision.ID)
	}
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing} {
		if _, err := f.tasks.Transition(ctx, f.task.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	if err := f.store.ReleaseLease(ctx, first.AttemptID, f.runner.ID); err != nil {
		t.Fatal(err)
	}
	f.fake.mu.Lock()
	f.fake.branchHeads[branch] = tip
	f.fake.mu.Unlock()

	second := f.claim(t)
	if second.AttemptID != revision.ID || second.TaskStatus != task.StatusPublishing {
		t.Fatalf("reclaim = %+v", second)
	}
	err := f.exec.Execute(ctx, f.runner.ID, second)
	if !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute err = %v, want ErrAttemptFailed", err)
	}
	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusFailed || got.FailureCode == nil || *got.FailureCode != "workspace_lost" {
		t.Fatalf("task = %s failure = %v, want failed/workspace_lost", got.Status, got.FailureCode)
	}
	if f.fake.prsUpdated != 0 {
		t.Fatalf("lost revision republished the previous tip: updates=%d", f.fake.prsUpdated)
	}
	if got := f.fake.updatesFor(4343); len(got) != 1 || got[0] != "failure" {
		t.Fatalf("trigger check run updates = %v, want [failure]", got)
	}
}
