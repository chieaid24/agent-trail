package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// gitRun runs git for test fixtures with a fixed identity.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// buildPublishOrigin creates a bare origin with one commit on main.
func buildPublishOrigin(t *testing.T) (originPath, baseSHA string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", "-A")
	gitRun(t, src, "commit", "-q", "-m", "initial")
	baseSHA = gitRun(t, src, "rev-parse", "HEAD")
	originPath = filepath.Join(dir, "origin.git")
	gitRun(t, dir, "clone", "-q", "--bare", src, "origin.git")
	return originPath, baseSHA
}

// fakePublish implements PublishGitHub in memory.
type fakePublish struct {
	mu          sync.Mutex
	branchHeads map[string]string
	prsByHead   map[string]*github.PullRequest
	prBodies    map[int64]string
	prsCreated  int
	prsUpdated  int
	checks      []github.CheckRunParams
	updates     []github.CheckRunParams
	comments    []string
	// cancelOnComment, when set, makes the next CreateIssueComment cancel
	// the run and fail once: the "owner died mid-publish" simulation.
	cancelOnComment context.CancelFunc
}

func newFakePublish() *fakePublish {
	return &fakePublish{
		branchHeads: map[string]string{},
		prsByHead:   map[string]*github.PullRequest{},
		prBodies:    map[int64]string{},
	}
}

func (f *fakePublish) InstallationToken(context.Context, int64) (string, error) {
	return "test-token", nil
}

func (f *fakePublish) BranchHeadSHA(_ context.Context, _ int64, _, _, branch string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha, ok := f.branchHeads[branch]
	if !ok {
		return "", &github.APIError{StatusCode: http.StatusNotFound,
			Method: http.MethodGet, Path: "/branches/" + branch}
	}
	return sha, nil
}

func (f *fakePublish) CreateIssueComment(_ context.Context, _ int64, _, _ string, _ int64, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancelOnComment != nil {
		cancel := f.cancelOnComment
		f.cancelOnComment = nil
		cancel()
		return errors.New("owner interrupted mid-publish")
	}
	f.comments = append(f.comments, body)
	return nil
}

func (f *fakePublish) FindPullRequestByHead(_ context.Context, _ int64, _, _, _, branch string) (*github.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prsByHead[branch]
	if !ok {
		return nil, nil
	}
	cp := *pr
	return &cp, nil
}

func (f *fakePublish) CreateDraftPullRequest(_ context.Context, _ int64, _, _ string, p github.PullRequestParams) (github.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prsCreated++
	pr := github.PullRequest{
		Number: int64(f.prsCreated), State: "open", Draft: true,
		HTMLURL: fmt.Sprintf("https://example.test/pr/%d", f.prsCreated),
	}
	f.prsByHead[p.Head] = &pr
	f.prBodies[pr.Number] = p.Body
	return pr, nil
}

func (f *fakePublish) UpdatePullRequestBody(_ context.Context, _ int64, _, _ string, number int64, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prsUpdated++
	f.prBodies[number] = body
	return nil
}

func (f *fakePublish) CreateCheckRun(_ context.Context, _ int64, _, _ string, p github.CheckRunParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, p)
	return int64(len(f.checks)), nil
}

func (f *fakePublish) UpdateCheckRun(_ context.Context, _ int64, _, _ string, _ int64, p github.CheckRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, p)
	return nil
}

func (f *fakePublish) ListCheckRuns(_ context.Context, _ int64, _, _, ref, name string) ([]github.CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var runs []github.CheckRun
	for i, p := range f.checks {
		if p.HeadSHA == ref && p.Name == name {
			runs = append(runs, github.CheckRun{
				ID: int64(i + 1), ExternalID: p.ExternalID,
				Status: p.Status, Conclusion: p.Conclusion,
			})
		}
	}
	return runs, nil
}

// stubAdapter runs a session that changes nothing (the no-change outcome).
type stubAdapter struct{}

func (stubAdapter) Name() string                                { return "stub" }
func (stubAdapter) ValidateConfiguration(context.Context) error { return nil }
func (stubAdapter) Start(context.Context, agent.Request) (agent.Session, error) {
	events := make(chan agent.Event)
	close(events)
	return stubSession{events: events}, nil
}

type stubSession struct{ events chan agent.Event }

func (s stubSession) Events() <-chan agent.Event         { return s.events }
func (s stubSession) Send(context.Context, string) error { return nil }
func (s stubSession) Cancel(context.Context) error       { return nil }
func (s stubSession) Wait(context.Context) (agent.Result, error) {
	return agent.Result{Summary: "nothing to do"}, nil
}

// publishFixture is a claimable GitHub-sourced task wired for publishing
// against a local bare origin and an in-memory GitHub API.
type publishFixture struct {
	db       *sql.DB
	store    *Store
	tasks    *task.Store
	exec     *Executor
	fake     *fakePublish
	origin   string
	baseSHA  string
	task     task.Task
	runner   Runner
	wsRoot   string
	issueNum int64
}

func newPublishFixture(t *testing.T) *publishFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	db, s, ts := testStores(t)
	ctx := context.Background()

	origin, baseSHA := buildPublishOrigin(t)
	gh := github.NewStore(db)
	err := gh.UpsertInstallation(ctx, github.InstallationParams{
		GitHubInstallationID: 999, AccountID: 61,
		AccountLogin: "acme", AccountType: "Organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := github.Repository{ID: 501, Name: "service",
		FullName: "acme/service", DefaultBranch: "main", CloneURL: origin}
	repo.Owner.Login = "acme"
	if err := gh.SyncRepositories(ctx, 999, []github.Repository{repo}); err != nil {
		t.Fatal(err)
	}
	stored, err := gh.RepositoryByGitHubID(ctx, 501)
	if err != nil {
		t.Fatal(err)
	}

	issue := int64(42)
	tk, err := ts.Create(ctx, task.CreateParams{
		Title:             "publishing test task",
		Instructions:      "do the thing",
		BaseBranch:        "main",
		SourceType:        "github_issue",
		SourceIssueNumber: &issue,
		OrganizationID:    &stored.OrganizationID,
		RepositoryID:      &stored.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wsRoot := t.TempDir()
	manager, err := gitworkspace.New(wsRoot, logger, observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakePublish()
	fake.branchHeads["main"] = baseSHA

	e := testExecutor(db, s, ts)
	e.Workspaces = manager
	e.GitHub = fake
	e.Repos = gh

	return &publishFixture{
		db: db, store: s, tasks: ts, exec: e, fake: fake,
		origin: origin, baseSHA: baseSHA, task: tk,
		runner: mustRegister(t, s), wsRoot: wsRoot, issueNum: issue,
	}
}

func (f *publishFixture) claim(t *testing.T) *Claim {
	t.Helper()
	c, err := f.store.Claim(context.Background(), f.runner.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	return c
}

func (f *publishFixture) attemptRow(t *testing.T, attemptID string) (finalSHA sql.NullString, prNumber sql.NullInt64) {
	t.Helper()
	err := f.db.QueryRow(`
		SELECT final_commit_sha, pull_request_number
		FROM task_attempts WHERE id = $1`, attemptID).Scan(&finalSHA, &prNumber)
	if err != nil {
		t.Fatal(err)
	}
	return finalSHA, prNumber
}

// TestPublishOpensOneDraftPR is the milestone acceptance happy path: the
// fake agent's changes land as one commit on an agent-trail/ branch in the
// origin, one draft PR whose body carries the verified-evidence table, one
// completed check run, one issue comment, task at awaiting_review.
func TestPublishOpensOneDraftPR(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	c := f.claim(t)

	if err := f.exec.Execute(ctx, f.runner.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusAwaitingReview {
		t.Fatalf("task status = %s, want awaiting_review", got.Status)
	}
	if got.WorkingBranch == nil || !strings.HasPrefix(*got.WorkingBranch, "agent-trail/issue-42-") {
		t.Fatalf("working branch = %v", got.WorkingBranch)
	}
	if got.BaseCommitSHA == nil || *got.BaseCommitSHA != f.baseSHA {
		t.Fatalf("base sha = %v, want %s", got.BaseCommitSHA, f.baseSHA)
	}

	finalSHA, prNumber := f.attemptRow(t, c.AttemptID)
	if !finalSHA.Valid || finalSHA.String == f.baseSHA {
		t.Fatalf("final commit = %+v", finalSHA)
	}
	if !prNumber.Valid || prNumber.Int64 != 1 {
		t.Fatalf("pull request number = %+v", prNumber)
	}

	// The branch is on the origin at the final commit.
	pushed := gitRun(t, f.origin, "rev-parse", "refs/heads/"+*got.WorkingBranch)
	if pushed != finalSHA.String {
		t.Fatalf("origin branch at %s, want %s", pushed, finalSHA.String)
	}

	// One draft PR, evidence-backed body.
	if f.fake.prsCreated != 1 {
		t.Fatalf("prs created = %d, want 1", f.fake.prsCreated)
	}
	body := f.fake.prBodies[1]
	for _, want := range []string{
		"Closes #42", "## Verified by Agent Trail", "| smoke |",
		"Final commit: `" + finalSHA.String + "`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pr body missing %q:\n%s", want, body)
		}
	}

	// One completed check run keyed to the attempt on the final commit.
	if len(f.fake.checks) != 1 {
		t.Fatalf("checks created = %d, want 1", len(f.fake.checks))
	}
	check := f.fake.checks[0]
	if check.ExternalID != c.AttemptID || check.HeadSHA != finalSHA.String ||
		check.Status != "completed" || check.Conclusion != "success" {
		t.Fatalf("check = %+v", check)
	}

	if len(f.fake.comments) != 1 || !strings.Contains(f.fake.comments[0], "draft pull request #1") {
		t.Fatalf("comments = %v", f.fake.comments)
	}

	assertSubsequence(t, timelineTypes(t, f.tasks, f.task.ID), []string{
		"task.publishing", "commit.created", "branch.pushed",
		"pull_request.created", "github.check_run.created",
		"github.comment.posted", "task.awaiting_review", "cleanup.completed",
	})
	for _, ev := range timelineTypes(t, f.tasks, f.task.ID) {
		if ev == "task.completed" {
			t.Fatal("published task must stay awaiting_review, not complete")
		}
	}

	// The settled attempt released its worktree.
	entries, err := os.ReadDir(filepath.Join(f.wsRoot, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspaces not cleaned: %v", entries)
	}
}

// TestPublishRetryCreatesNoSecondPR is the idempotency acceptance: an owner
// dies mid-publish (after commit, push, PR, and check, before the issue
// comment and the transition), and the recovering owner reattaches the
// surviving worktree and replays every step without creating a second
// commit, PR, or check.
func TestPublishRetryCreatesNoSecondPR(t *testing.T) {
	f := newPublishFixture(t)
	c := f.claim(t)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.fake.cancelOnComment = cancel
	if err := f.exec.Execute(runCtx, f.runner.ID, c); err == nil {
		t.Fatal("interrupted run reported success")
	}

	mid, err := f.tasks.Get(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mid.Status != task.StatusPublishing {
		t.Fatalf("interrupted status = %s, want publishing", mid.Status)
	}
	if f.fake.prsCreated != 1 {
		t.Fatalf("prs created before crash = %d, want 1", f.fake.prsCreated)
	}
	firstFinal, _ := f.attemptRow(t, c.AttemptID)

	ctx := context.Background()
	c2 := f.claim(t)
	if c2.AttemptID != c.AttemptID || c2.TaskStatus != task.StatusPublishing {
		t.Fatalf("reclaim = %+v", c2)
	}
	if err := f.exec.Execute(ctx, f.runner.ID, c2); err != nil {
		t.Fatal(err)
	}

	if f.fake.prsCreated != 1 {
		t.Fatalf("prs created after retry = %d, want 1", f.fake.prsCreated)
	}
	if f.fake.prsUpdated == 0 {
		t.Fatal("retry did not refresh the existing PR body")
	}
	if len(f.fake.checks) != 1 || len(f.fake.updates) == 0 {
		t.Fatalf("checks created = %d, updated = %d; want 1, >0",
			len(f.fake.checks), len(f.fake.updates))
	}
	if len(f.fake.comments) != 1 {
		t.Fatalf("comments = %v, want exactly one", f.fake.comments)
	}

	after, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != task.StatusAwaitingReview {
		t.Fatalf("task status = %s, want awaiting_review", after.Status)
	}
	// The retry reused the first owner's commit: one commit past base.
	secondFinal, prNumber := f.attemptRow(t, c.AttemptID)
	if !secondFinal.Valid || secondFinal.String != firstFinal.String {
		t.Fatalf("final commit changed on retry: %+v -> %+v", firstFinal, secondFinal)
	}
	if !prNumber.Valid || prNumber.Int64 != 1 {
		t.Fatalf("pull request number = %+v, want 1", prNumber)
	}
	pushed := gitRun(t, f.origin, "rev-parse", "refs/heads/"+*after.WorkingBranch)
	if pushed != secondFinal.String {
		t.Fatalf("origin branch at %s, want %s", pushed, secondFinal.String)
	}
}

// TestPublishNoChangeCreatesNoPR is the empty-diff acceptance: a session
// that changes nothing opens no PR, resolves the check neutral on the base
// commit, explains itself on the issue, and fails the task as no_change.
func TestPublishNoChangeCreatesNoPR(t *testing.T) {
	f := newPublishFixture(t)
	ctx := context.Background()
	f.exec.Adapter = stubAdapter{}
	c := f.claim(t)

	err := f.exec.Execute(ctx, f.runner.ID, c)
	if !errors.Is(err, ErrAttemptFailed) {
		t.Fatalf("Execute err = %v, want ErrAttemptFailed", err)
	}

	got, err := f.tasks.Get(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusFailed {
		t.Fatalf("task status = %s, want failed", got.Status)
	}
	if got.FailureCode == nil || *got.FailureCode != "no_change" {
		t.Fatalf("failure code = %v, want no_change", got.FailureCode)
	}
	if got.FailureMessage == nil || !strings.Contains(*got.FailureMessage, "without modifying") {
		t.Fatalf("failure message = %v", got.FailureMessage)
	}

	if f.fake.prsCreated != 0 {
		t.Fatalf("prs created = %d, want 0", f.fake.prsCreated)
	}
	if len(f.fake.checks) != 1 || f.fake.checks[0].Conclusion != "neutral" ||
		f.fake.checks[0].HeadSHA != f.baseSHA {
		t.Fatalf("checks = %+v", f.fake.checks)
	}
	if len(f.fake.comments) != 1 || !strings.Contains(f.fake.comments[0], "no changes") {
		t.Fatalf("comments = %v", f.fake.comments)
	}

	// Nothing was pushed: the origin still has only main.
	refs := gitRun(t, f.origin, "for-each-ref", "--format=%(refname)", "refs/heads")
	if refs != "refs/heads/main" {
		t.Fatalf("origin refs = %q, want only main", refs)
	}
}
