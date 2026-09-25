package github

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type fakeAPI struct {
	mu             sync.Mutex
	repos          []Repository
	permission     string
	headSHA        string
	comments       []string
	checkRuns      int
	checkRunParams []CheckRunParams
	pullRequest    PullRequestDetail
	pullRequestErr error
	reviews        []Review
	reviewComments []ReviewComment
	issueComments  []IssueComment
}

func (f *fakeAPI) ListInstallationRepositories(context.Context, int64) ([]Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Repository(nil), f.repos...), nil
}

func (f *fakeAPI) CollaboratorPermission(context.Context, int64, string, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.permission, nil
}

func (f *fakeAPI) BranchHeadSHA(context.Context, int64, string, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headSHA, nil
}

func (f *fakeAPI) CreateIssueComment(_ context.Context, _ int64, _, _ string, _ int64, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, body)
	return nil
}

func (f *fakeAPI) CreateCheckRun(_ context.Context, _ int64, _, _ string, p CheckRunParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkRuns++
	f.checkRunParams = append(f.checkRunParams, p)
	return int64(776 + f.checkRuns), nil
}

func (f *fakeAPI) GetPullRequest(_ context.Context, _ int64, _, _ string, number int64) (PullRequestDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pullRequestErr != nil {
		return PullRequestDetail{}, f.pullRequestErr
	}
	pr := f.pullRequest
	pr.Number = number
	return pr, nil
}

func (f *fakeAPI) ListPullRequestReviews(context.Context, int64, string, string, int64) ([]Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Review(nil), f.reviews...), nil
}

// since is advisory on github; the processor must apply the cutoff itself
func (f *fakeAPI) ListPullRequestReviewComments(context.Context, int64, string, string, int64, time.Time) ([]ReviewComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ReviewComment(nil), f.reviewComments...), nil
}

func (f *fakeAPI) ListIssueComments(context.Context, int64, string, string, int64, time.Time) ([]IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]IssueComment(nil), f.issueComments...), nil
}

func (f *fakeAPI) commentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.comments)
}

func (f *fakeAPI) lastComment() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.comments) == 0 {
		return ""
	}
	return f.comments[len(f.comments)-1]
}

func testRepo(id int64, fullName string) Repository {
	var r Repository
	r.ID = id
	r.Name = fullName[len("acme/"):]
	r.FullName = fullName
	r.DefaultBranch = "main"
	r.CloneURL = "https://github.com/" + fullName + ".git"
	r.Owner.Login = "acme"
	return r
}

type fixture struct {
	db    *sql.DB
	store *Store
	tasks *task.Store
	api   *fakeAPI
	proc  *Processor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := dbtest.Open(t)
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	api := &fakeAPI{
		repos:      []Repository{testRepo(501, "acme/service")},
		permission: "write",
		headSHA:    "0123456789012345678901234567890123456789",
	}
	store := NewStore(db)
	tasks := task.NewStore(db)
	return &fixture{
		db:    db,
		store: store,
		tasks: tasks,
		api:   api,
		proc:  NewProcessor(store, tasks, api, logger, observability.NewRegistry()),
	}
}

func installationJSON(t *testing.T, action string) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"action": action,
		"installation": map[string]any{
			"id": 999,
			"account": map[string]any{
				"id": 61, "login": "acme", "type": "Organization",
			},
			"permissions": map[string]string{"issues": "write"},
			"events":      []string{"issues", "issue_comment"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type commentOpts struct {
	body        string
	commentID   int64
	userType    string
	action      string
	pullRequest bool
}

func issueCommentJSON(t *testing.T, o commentOpts) []byte {
	t.Helper()
	if o.action == "" {
		o.action = "created"
	}
	if o.commentID == 0 {
		o.commentID = 9001
	}
	if o.userType == "" {
		o.userType = "User"
	}
	issue := map[string]any{
		"number": 15,
		"title":  "Fix the flaky login test",
		"body":   "It fails on CI about once a day.",
	}
	if o.pullRequest {
		issue["pull_request"] = map[string]any{"url": "https://example.test"}
	}
	payload, err := json.Marshal(map[string]any{
		"action": o.action,
		"comment": map[string]any{
			"id":   o.commentID,
			"body": o.body,
			"user": map[string]any{"id": 7, "login": "alice", "type": o.userType},
		},
		"issue": issue,
		"repository": map[string]any{
			"id": 501,
			"owner": map[string]any{
				"id": 61, "login": "acme", "type": "Organization",
			},
		},
		"installation": map[string]any{"id": 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (f *fixture) recordAndProcess(t *testing.T, deliveryID, eventType string, payload []byte) {
	t.Helper()
	ctx := context.Background()
	inserted, err := f.store.RecordDelivery(ctx, deliveryID, eventType, "", 999, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatalf("delivery %s already recorded", deliveryID)
	}
	f.proc.process(ctx, Delivery{ID: deliveryID, EventType: eventType}, payload)
}

func (f *fixture) deliveryStatus(t *testing.T, deliveryID string) string {
	t.Helper()
	var status string
	err := f.db.QueryRow(`
		SELECT processing_status FROM github_webhook_deliveries
		WHERE github_delivery_id = $1`, deliveryID).Scan(&status)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *fixture) taskCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInstallationCreatedSyncsOrganizationAndRepositories(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-install", "installation", installationJSON(t, "created"))

	if got := f.deliveryStatus(t, "d-install"); got != "processed" {
		t.Fatalf("delivery status = %q", got)
	}
	repo, err := f.store.RepositoryByGitHubID(context.Background(), 501)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.IsEnabled || repo.FullName != "acme/service" || repo.DefaultBranch != "main" {
		t.Fatalf("repo = %+v", repo)
	}
	var orgLogin string
	err = f.db.QueryRow(`SELECT github_account_login FROM organizations`).Scan(&orgLogin)
	if err != nil || orgLogin != "acme" {
		t.Fatalf("organization login = %q err = %v", orgLogin, err)
	}
}

func TestRepositoryRemovalDisablesRow(t *testing.T) {
	f := newFixture(t)
	f.api.repos = []Repository{
		testRepo(501, "acme/service"), testRepo(502, "acme/tools"),
	}
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))

	f.api.mu.Lock()
	f.api.repos = []Repository{testRepo(501, "acme/service")}
	f.api.mu.Unlock()
	f.recordAndProcess(t, "d-2", "installation_repositories",
		installationJSON(t, "removed"))

	removed, err := f.store.RepositoryByGitHubID(context.Background(), 502)
	if err != nil {
		t.Fatal(err)
	}
	if removed.IsEnabled {
		t.Fatal("removed repository still enabled")
	}
	kept, err := f.store.RepositoryByGitHubID(context.Background(), 501)
	if err != nil || !kept.IsEnabled {
		t.Fatalf("kept repository disabled: %+v err=%v", kept, err)
	}
}

func TestInstallationDeletedDisablesRepositories(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-2", "installation", installationJSON(t, "deleted"))

	var installations int
	if err := f.db.QueryRow(`SELECT count(*) FROM github_installations`).Scan(&installations); err != nil {
		t.Fatal(err)
	}
	if installations != 0 {
		t.Fatalf("installations = %d, want 0", installations)
	}
	repo, err := f.store.RepositoryByGitHubID(context.Background(), 501)
	if err != nil {
		t.Fatal(err)
	}
	if repo.IsEnabled {
		t.Fatal("repository still enabled after uninstall")
	}
}

func TestRunCommandCreatesExactlyOneTask(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-2", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))

	if got := f.deliveryStatus(t, "d-2"); got != "processed" {
		t.Fatalf("delivery status = %q", got)
	}
	if n := f.taskCount(t); n != 1 {
		t.Fatalf("tasks = %d, want 1", n)
	}

	var created task.Task
	tasks, err := f.tasks.List(context.Background(), task.ListParams{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("list: %v (%d tasks)", err, len(tasks))
	}
	created = tasks[0]
	if created.SourceType != "github_issue" ||
		created.SourceIssueNumber == nil || *created.SourceIssueNumber != 15 ||
		created.SourceCommentID == nil || *created.SourceCommentID != 9001 ||
		created.RepositoryID == nil || created.OrganizationID == nil {
		t.Fatalf("task source fields wrong: %+v", created)
	}
	if created.Title != "Fix the flaky login test" || created.BaseBranch != "main" {
		t.Fatalf("task content wrong: title=%q base=%q", created.Title, created.BaseBranch)
	}

	if f.api.checkRuns != 1 {
		t.Fatalf("check runs = %d, want 1", f.api.checkRuns)
	}
	if f.api.commentCount() != 1 {
		t.Fatalf("comments = %d, want 1", f.api.commentCount())
	}

	events, err := f.tasks.Events(context.Background(), created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var haveCheckRun, haveAck bool
	for _, e := range events {
		switch e.EventType {
		case "github.check_run.created":
			haveCheckRun = true
		case "github.comment.posted":
			haveAck = true
		}
	}
	if !haveCheckRun || !haveAck {
		t.Fatalf("side-effect events missing: check_run=%v ack=%v", haveCheckRun, haveAck)
	}
	attempts, err := f.tasks.Attempts(context.Background(), created.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts = %+v, err = %v", attempts, err)
	}
	if attempts[0].TriggerCheckRunID == nil || *attempts[0].TriggerCheckRunID != 777 ||
		attempts[0].RequestedByLogin == nil || *attempts[0].RequestedByLogin != "alice" ||
		attempts[0].TriggerCommentID == nil || *attempts[0].TriggerCommentID != 9001 {
		t.Fatalf("attempt 1 = %+v", attempts[0])
	}
	if got := f.api.checkRunParams[0]; got.HeadSHA != f.api.headSHA || got.Status != "queued" {
		t.Fatalf("trigger check run = %+v", got)
	}
}

func TestSecondRunCommandOnSameIssueRejected(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-2", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run", commentID: 1}))
	f.recordAndProcess(t, "d-3", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run", commentID: 2}))

	if n := f.taskCount(t); n != 1 {
		t.Fatalf("tasks = %d, want 1", n)
	}
	if got := f.lastCommentContains(t, "already has an active task"); !got {
		t.Fatalf("no active-task reply; last comment: %q", f.api.lastComment())
	}
}

func (f *fixture) lastCommentContains(t *testing.T, want string) bool {
	t.Helper()
	return bytes.Contains([]byte(f.api.lastComment()), []byte(want))
}

func TestRunCommandWithoutWriteAccessRejected(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	f.api.mu.Lock()
	f.api.permission = "read"
	f.api.mu.Unlock()
	f.recordAndProcess(t, "d-2", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))

	if n := f.taskCount(t); n != 0 {
		t.Fatalf("tasks = %d, want 0", n)
	}
	if !f.lastCommentContains(t, "write access") {
		t.Fatalf("no authorization reply; last comment: %q", f.api.lastComment())
	}
}

func TestUnknownCommandGetsUsageReply(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-2", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail deploy"}))

	if n := f.taskCount(t); n != 0 {
		t.Fatalf("tasks = %d, want 0", n)
	}
	if !f.lastCommentContains(t, "Unknown command") {
		t.Fatalf("no usage reply; last comment: %q", f.api.lastComment())
	}
}

func TestPullRequestCommentRejected(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-2", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run", pullRequest: true}))

	if n := f.taskCount(t); n != 0 {
		t.Fatalf("tasks = %d, want 0", n)
	}
	if !f.lastCommentContains(t, "not pull requests") {
		t.Fatalf("no PR reply; last comment: %q", f.api.lastComment())
	}
}

func TestDisabledRepositoryRejected(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))
	if _, err := f.db.Exec(`UPDATE repositories SET is_enabled = false`); err != nil {
		t.Fatal(err)
	}
	f.recordAndProcess(t, "d-2", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))

	if n := f.taskCount(t); n != 0 {
		t.Fatalf("tasks = %d, want 0", n)
	}
	if !f.lastCommentContains(t, "not enabled") {
		t.Fatalf("no disabled reply; last comment: %q", f.api.lastComment())
	}
}

func TestCommandSelfHealsUnsyncedRepository(t *testing.T) {
	f := newFixture(t)
	// empty tables = app installed before the webhook endpoint existed
	f.recordAndProcess(t, "d-1", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))

	if n := f.taskCount(t); n != 1 {
		t.Fatalf("tasks = %d, want 1", n)
	}
	repo, err := f.store.RepositoryByGitHubID(context.Background(), 501)
	if err != nil || !repo.IsEnabled {
		t.Fatalf("repository not self-healed: %+v err=%v", repo, err)
	}
}

func TestNonCommandTrafficIgnored(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-1", "installation", installationJSON(t, "created"))

	cases := []struct {
		name    string
		event   string
		payload []byte
	}{
		{"ping", "ping", []byte(`{"zen":"Keep it simple."}`)},
		{"plain comment", "issue_comment",
			issueCommentJSON(t, commentOpts{body: "looks good to me"})},
		{"edited action", "issue_comment",
			issueCommentJSON(t, commentOpts{body: "/agent-trail run", action: "edited"})},
		{"bot comment", "issue_comment",
			issueCommentJSON(t, commentOpts{body: "/agent-trail run", userType: "Bot"})},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("d-ignored-%d", i)
			f.recordAndProcess(t, id, tc.event, tc.payload)
			if got := f.deliveryStatus(t, id); got != "ignored" {
				t.Fatalf("delivery status = %q, want ignored", got)
			}
		})
	}
	if n := f.taskCount(t); n != 0 {
		t.Fatalf("tasks = %d, want 0", n)
	}
}

func TestMalformedPayloadMarksDeliveryFailed(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-bad", "issue_comment", []byte(`{"action":`))
	if got := f.deliveryStatus(t, "d-bad"); got != "failed" {
		t.Fatalf("delivery status = %q, want failed", got)
	}
}

func TestInstallationSuspendAndUnsuspend(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-sus-install", "installation", installationJSON(t, "created"))

	suspendedAt := func() sql.NullTime {
		var at sql.NullTime
		err := f.db.QueryRow(`
			SELECT suspended_at FROM github_installations
			WHERE github_installation_id = 999`).Scan(&at)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}

	f.recordAndProcess(t, "d-sus", "installation", installationJSON(t, "suspend"))
	if !suspendedAt().Valid {
		t.Fatal("suspended_at not set after suspend")
	}
	f.recordAndProcess(t, "d-unsus", "installation", installationJSON(t, "unsuspend"))
	if suspendedAt().Valid {
		t.Fatal("suspended_at still set after unsuspend")
	}
}

func TestSelfHealUpsertPreservesPermissions(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-perm-install", "installation", installationJSON(t, "created"))

	// self-heal upserts with neither permissions nor events; previous sync's values must survive
	err := f.store.UpsertInstallation(context.Background(), InstallationParams{
		GitHubInstallationID: 999, AccountID: 61,
		AccountLogin: "acme", AccountType: "Organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	var permissions, events string
	err = f.db.QueryRow(`
		SELECT permissions_json::text, events_json::text
		FROM github_installations
		WHERE github_installation_id = 999`).Scan(&permissions, &events)
	if err != nil {
		t.Fatal(err)
	}
	if permissions != `{"issues": "write"}` {
		t.Fatalf("permissions_json = %s", permissions)
	}
	if events != `["issues", "issue_comment"]` {
		t.Fatalf("events_json = %s", events)
	}
}

const reviewBranch = "agent-trail/fix-the-flaky-login-test"

type prOpts struct {
	action     string
	number     int64
	merged     bool
	headRef    string
	headRepoID int64 // 0 = this repository
	noHeadRepo bool
	repoID     int64 // 0 = the synced fixture repository
}

func pullRequestJSON(t *testing.T, o prOpts) []byte {
	t.Helper()
	if o.action == "" {
		o.action = "closed"
	}
	if o.number == 0 {
		o.number = 12
	}
	if o.headRef == "" {
		o.headRef = reviewBranch
	}
	if o.repoID == 0 {
		o.repoID = 501
	}
	if o.headRepoID == 0 {
		o.headRepoID = o.repoID
	}
	head := map[string]any{"ref": o.headRef, "sha": strings.Repeat("b", 40)}
	if !o.noHeadRepo {
		head["repo"] = map[string]any{"id": o.headRepoID}
	}
	payload, err := json.Marshal(map[string]any{
		"action": o.action,
		"number": o.number,
		"pull_request": map[string]any{
			"number": o.number,
			"state":  "closed",
			"merged": o.merged,
			"head":   head,
			"base":   map[string]any{"ref": "main"},
		},
		"repository": map[string]any{
			"id": o.repoID, "full_name": "acme/service",
			"owner": map[string]any{
				"id": 61, "login": "acme", "type": "Organization",
			},
		},
		"installation": map[string]any{"id": 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// creates the issue task on reviewBranch and walks it along the happy path to status
func (f *fixture) taskAt(t *testing.T, status task.Status) task.Task {
	t.Helper()
	ctx := context.Background()
	f.recordAndProcess(t, "d-install", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-run", "issue_comment",
		issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))
	tasks, err := f.tasks.List(ctx, task.ListParams{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("list: %v (%d tasks)", err, len(tasks))
	}
	cur := tasks[0]
	if _, _, err := f.tasks.EnsureGitContext(ctx, cur.ID, strings.Repeat("a", 40), reviewBranch); err != nil {
		t.Fatal(err)
	}
	path := []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing,
		task.StatusAwaitingReview, task.StatusRevisionRequested}
	for _, to := range path {
		if cur.Status == status {
			break
		}
		if cur, err = f.tasks.Transition(ctx, cur.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	if cur.Status != status {
		t.Fatalf("task at %s, want %s", cur.Status, status)
	}
	return cur
}

func (f *fixture) taskStatus(t *testing.T, id string) task.Status {
	t.Helper()
	cur, err := f.tasks.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return cur.Status
}

type transitionEvent struct {
	Type, Source, From, To, Reason string
}

func (f *fixture) transitionEvents(t *testing.T, id string) []transitionEvent {
	t.Helper()
	events, err := f.tasks.Events(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []transitionEvent
	for _, e := range events {
		if !strings.HasPrefix(e.EventType, "task.") || e.EventType == task.EventTypeCreated {
			continue
		}
		var payload struct{ From, To, Reason string }
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, transitionEvent{
			Type: e.EventType, Source: e.Source,
			From: payload.From, To: payload.To, Reason: payload.Reason,
		})
	}
	return out
}

func (f *fixture) lastTransition(t *testing.T, id string) transitionEvent {
	t.Helper()
	events := f.transitionEvents(t, id)
	if len(events) == 0 {
		t.Fatal("no transition events")
	}
	return events[len(events)-1]
}

func TestPullRequestMergedCompletesTask(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)

	f.recordAndProcess(t, "d-pr", "pull_request", pullRequestJSON(t, prOpts{merged: true}))

	if got := f.deliveryStatus(t, "d-pr"); got != "processed" {
		t.Fatalf("delivery status = %q, want processed", got)
	}
	final, err := f.tasks.Get(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusCompleted || final.CompletedAt == nil {
		t.Fatalf("task = %s completed_at=%v, want completed", final.Status, final.CompletedAt)
	}
	want := transitionEvent{
		Type: "task.completed", Source: "system",
		From: "awaiting_review", To: "completed", Reason: "pull request #12 merged",
	}
	if got := f.lastTransition(t, tk.ID); got != want {
		t.Fatalf("last transition = %+v, want %+v", got, want)
	}
}

func TestPullRequestClosedWithoutMergeCancelsTask(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)

	f.recordAndProcess(t, "d-pr", "pull_request", pullRequestJSON(t, prOpts{merged: false}))

	if got := f.deliveryStatus(t, "d-pr"); got != "processed" {
		t.Fatalf("delivery status = %q, want processed", got)
	}
	if got := f.taskStatus(t, tk.ID); got != task.StatusCancelled {
		t.Fatalf("task = %s, want cancelled", got)
	}
	want := transitionEvent{
		Type: "task.cancelled", Source: "system",
		From: "awaiting_review", To: "cancelled",
		Reason: "pull request #12 closed without merge",
	}
	if got := f.lastTransition(t, tk.ID); got != want {
		t.Fatalf("last transition = %+v, want %+v", got, want)
	}
}

func TestPullRequestMergedCompletesRevisionRequestedTask(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusRevisionRequested)

	f.recordAndProcess(t, "d-pr", "pull_request", pullRequestJSON(t, prOpts{merged: true}))

	if got := f.deliveryStatus(t, "d-pr"); got != "processed" {
		t.Fatalf("delivery status = %q, want processed", got)
	}
	if got := f.lastTransition(t, tk.ID); got.From != "revision_requested" || got.To != "completed" {
		t.Fatalf("last transition = %+v, want revision_requested -> completed", got)
	}
}

func TestPullRequestClosedIgnoredWhenNoTaskMatches(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)

	cases := []struct {
		name string
		opts prOpts
	}{
		{"opened action", prOpts{action: "opened"}},
		{"reopened action", prOpts{action: "reopened", merged: false}},
		{"non agent-trail branch", prOpts{merged: true, headRef: "feature/login"}},
		{"unknown agent-trail branch", prOpts{merged: true, headRef: "agent-trail/other-task"}},
		{"fork head", prOpts{merged: true, headRepoID: 777}},
		{"deleted fork head", prOpts{merged: true, noHeadRepo: true}},
		{"unknown repository", prOpts{merged: true, repoID: 8888}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("d-pr-%d", i)
			f.recordAndProcess(t, id, "pull_request", pullRequestJSON(t, tc.opts))
			if got := f.deliveryStatus(t, id); got != "ignored" {
				t.Fatalf("delivery status = %q, want ignored", got)
			}
			if got := f.taskStatus(t, tk.ID); got != task.StatusAwaitingReview {
				t.Fatalf("task = %s, want awaiting_review untouched", got)
			}
		})
	}
}

func TestPullRequestClosedBeforeReviewLeavesTaskRunning(t *testing.T) {
	for _, status := range []task.Status{task.StatusQueued, task.StatusExecuting} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture(t)
			tk := f.taskAt(t, status)

			f.recordAndProcess(t, "d-pr", "pull_request", pullRequestJSON(t, prOpts{merged: true}))

			if got := f.deliveryStatus(t, "d-pr"); got != "ignored" {
				t.Fatalf("delivery status = %q, want ignored", got)
			}
			if got := f.taskStatus(t, tk.ID); got != status {
				t.Fatalf("task = %s, want %s untouched", got, status)
			}
		})
	}
}

func TestPullRequestClosedRedeliveryIsNoOp(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)
	payload := pullRequestJSON(t, prOpts{merged: true})

	f.recordAndProcess(t, "d-pr-1", "pull_request", payload)
	f.recordAndProcess(t, "d-pr-2", "pull_request", payload)

	if got := f.deliveryStatus(t, "d-pr-2"); got != "ignored" {
		t.Fatalf("redelivery status = %q, want ignored", got)
	}
	completions := 0
	for _, e := range f.transitionEvents(t, tk.ID) {
		if e.Type == "task.completed" {
			completions++
		}
	}
	if completions != 1 {
		t.Fatalf("task.completed events = %d, want exactly 1", completions)
	}
}

func TestPullRequestClosedConcurrentDeliveriesCompleteOnce(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)
	payload := pullRequestJSON(t, prOpts{merged: true})
	ctx := context.Background()

	const workers = 6
	ids := make([]string, workers)
	for i := range ids {
		ids[i] = fmt.Sprintf("d-pr-concurrent-%d", i)
		if _, err := f.store.RecordDelivery(ctx, ids[i], "pull_request", "closed", 999, 501); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.proc.process(ctx, Delivery{ID: id, EventType: "pull_request"}, payload)
		}()
	}
	wg.Wait()

	for _, id := range ids {
		if got := f.deliveryStatus(t, id); got == "failed" {
			t.Fatalf("delivery %s failed on a concurrent replay", id)
		}
	}
	if got := f.taskStatus(t, tk.ID); got != task.StatusCompleted {
		t.Fatalf("task = %s, want completed", got)
	}
	completions := 0
	for _, e := range f.transitionEvents(t, tk.ID) {
		if e.Type == "task.completed" {
			completions++
		}
	}
	if completions != 1 {
		t.Fatalf("task.completed events = %d, want exactly 1", completions)
	}
}

func TestPullRequestClosedWithoutNumberFails(t *testing.T) {
	f := newFixture(t)
	f.recordAndProcess(t, "d-pr-bad", "pull_request",
		[]byte(`{"action":"closed","pull_request":{"merged":true},"repository":{"id":501}}`))
	if got := f.deliveryStatus(t, "d-pr-bad"); got != "failed" {
		t.Fatalf("delivery status = %q, want failed", got)
	}
}

const reviewHeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// agent trail pull request #15 on reviewBranch, head pushed to this repository
func (f *fixture) setPullRequest(headRef string, headRepoID int64) {
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	f.api.pullRequest = PullRequestDetail{
		State: "open", HTMLURL: "https://example.test/pr/15",
		Head: PullRequestHead{Ref: headRef, SHA: reviewHeadSHA, RepoID: headRepoID},
	}
}

func (f *fixture) attempts(t *testing.T, id string) []task.Attempt {
	t.Helper()
	attempts, err := f.tasks.Attempts(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return attempts
}

func reviseComment(commentID int64) commentOpts {
	return commentOpts{
		body:      "/agent-trail revise\nplease also rename the helper",
		commentID: commentID, pullRequest: true,
	}
}

// walks the active attempt along the happy path back to awaiting_review
func (f *fixture) finishAttempt(t *testing.T, id string) {
	t.Helper()
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing, task.StatusAwaitingReview} {
		if _, err := f.tasks.Transition(context.Background(), id, task.TransitionParams{To: to}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
}

func TestReviseCreatesNextAttemptFromPullRequestHead(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)
	f.setPullRequest(reviewBranch, 501)

	// a first revision fixes the cutoff: feedback before its trigger was already given to it
	f.recordAndProcess(t, "d-revise-1", "issue_comment", issueCommentJSON(t, reviseComment(9002)))
	if got := f.taskStatus(t, tk.ID); got != task.StatusQueued {
		t.Fatalf("first revision left the task %s: %q", got, f.api.lastComment())
	}
	f.finishAttempt(t, tk.ID)
	cutoff := f.attempts(t, tk.ID)[1].CreatedAt
	before, after := cutoff.Add(-time.Hour), cutoff.Add(time.Minute)
	seven := 7
	f.api.mu.Lock()
	f.api.reviews = []Review{
		{ID: 1, Body: "old round", State: "COMMENTED", User: Author{Login: "bob", Type: "User"}, SubmittedAt: before},
		{ID: 2, Body: "Please split the helper.", State: "CHANGES_REQUESTED", User: Author{Login: "bob", Type: "User"}, SubmittedAt: after.Add(2 * time.Minute)},
		{ID: 3, Body: "", State: "APPROVED", User: Author{Login: "carol", Type: "User"}, SubmittedAt: after.Add(3 * time.Minute)},
	}
	f.api.reviewComments = []ReviewComment{
		{ID: 4, Body: "rename this", Path: "internal/a.go", Line: &seven, DiffHunk: "@@ -1 +1 @@\n-x\n+y", User: Author{Login: "alice", Type: "User"}, CreatedAt: after.Add(time.Minute)},
		{ID: 5, Body: "bot noise", Path: "internal/b.go", Line: &seven, User: Author{Login: "linter[bot]", Type: "Bot"}, CreatedAt: after.Add(time.Minute)},
	}
	f.api.issueComments = []IssueComment{
		{ID: 6, Body: "also update the docs", User: Author{Login: "alice", Type: "User"}, CreatedAt: after},
		{ID: 7, Body: "/agent-trail run", User: Author{Login: "dave", Type: "User"}, CreatedAt: after.Add(90 * time.Second)},
		{ID: 9003, Body: "/agent-trail revise", User: Author{Login: "alice", Type: "User"}, CreatedAt: after.Add(4 * time.Minute)},
	}
	f.api.mu.Unlock()

	f.recordAndProcess(t, "d-revise-2", "issue_comment", issueCommentJSON(t, reviseComment(9003)))

	if got := f.deliveryStatus(t, "d-revise-2"); got != "processed" {
		t.Fatalf("delivery status = %q", got)
	}
	if got := f.taskStatus(t, tk.ID); got != task.StatusQueued {
		t.Fatalf("task = %s, want queued", got)
	}
	attempts := f.attempts(t, tk.ID)
	if len(attempts) != 3 || attempts[1].Status != "superseded" || attempts[2].Status != "active" {
		t.Fatalf("attempts = %+v", attempts)
	}
	third := attempts[2]
	if third.BaseCommitSHA == nil || *third.BaseCommitSHA != reviewHeadSHA ||
		third.RequestedByLogin == nil || *third.RequestedByLogin != "alice" ||
		third.TriggerCommentID == nil || *third.TriggerCommentID != 9003 ||
		third.TriggerCheckRunID == nil || *third.TriggerCheckRunID != 779 {
		t.Fatalf("attempt 3 = %+v", third)
	}
	kinds := []string{}
	for _, item := range third.Feedback {
		kinds = append(kinds, string(item.Kind)+":"+item.Author+":"+item.Location)
	}
	want := []string{"comment:alice:", "review_comment:alice:internal/a.go:7",
		"review:bob:changes_requested", "revise_command:alice:"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("feedback = %v, want %v", kinds, want)
	}
	instructions := *third.Instructions
	for _, want := range []string{
		"Fix the flaky login test", "It fails on CI about once a day.",
		"already contains the previous attempts' work",
		"[comment by @alice at", "also update the docs",
		"[inline review comment by @alice at", "on internal/a.go:7]", "```diff\n@@ -1 +1 @@", "rename this",
		"[review by @bob at", "Please split the helper.",
		"Revise command by @alice:", "please also rename the helper",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q:\n%s", want, instructions)
		}
	}
	for _, absent := range []string{"old round", "bot noise", "@dave"} {
		if strings.Contains(instructions, absent) {
			t.Fatalf("instructions carry excluded feedback %q:\n%s", absent, instructions)
		}
	}
	if strings.Index(instructions, "also update the docs") > strings.Index(instructions, "rename this") ||
		strings.Index(instructions, "rename this") > strings.Index(instructions, "Please split the helper.") {
		t.Fatalf("feedback not chronological:\n%s", instructions)
	}

	// trigger check run on the pull request head, ack on the pull request
	params := f.api.checkRunParams[len(f.api.checkRunParams)-1]
	if params.HeadSHA != reviewHeadSHA || params.Status != "queued" || params.ExternalID != tk.ID {
		t.Fatalf("trigger check run = %+v", params)
	}
	if !f.lastCommentContains(t, "revision attempt 3") || !f.lastCommentContains(t, "4 review feedback item(s)") {
		t.Fatalf("ack = %q", f.api.lastComment())
	}
	events := f.transitionEvents(t, tk.ID)
	last := events[len(events)-2:]
	if last[0].To != "revision_requested" || last[1].To != "queued" ||
		!strings.Contains(last[1].Reason, "revision requested by @alice in pull request #15") {
		t.Fatalf("transitions = %+v", last)
	}
}

func TestReviseGuardsReplyOnceAndCreateNothing(t *testing.T) {
	cases := []struct {
		name    string
		status  task.Status
		prepare func(f *fixture)
		comment commentOpts
		reply   string
		ignored bool
	}{
		{"revise on an issue", task.StatusAwaitingReview, nil,
			commentOpts{body: "/agent-trail revise", commentID: 1}, "works on Agent Trail pull requests, not issues", false},
		{"run on a pull request", task.StatusAwaitingReview, nil,
			commentOpts{body: "/agent-trail run", commentID: 2, pullRequest: true}, "works on issues, not pull requests", false},
		{"disabled repository", task.StatusAwaitingReview, func(f *fixture) {
			if _, err := f.db.Exec(`UPDATE repositories SET is_enabled = false`); err != nil {
				t.Fatal(err)
			}
		}, reviseComment(3), "not enabled", false},
		{"commenter without write access", task.StatusAwaitingReview, func(f *fixture) {
			f.api.mu.Lock()
			f.api.permission = "read"
			f.api.mu.Unlock()
		}, reviseComment(4), "write access", false},
		{"running attempt", task.StatusExecuting, nil, reviseComment(5), "still running", false},
		{"queued attempt", task.StatusQueued, nil, reviseComment(6), "still running", false},
		{"terminal task", task.StatusCompleted, nil, reviseComment(7), "cannot be revised", false},
		{"revision limit reached", task.StatusAwaitingReview, func(f *fixture) {
			if _, err := f.db.Exec(`UPDATE repositories SET settings_json = '{"max_attempts": 1}'`); err != nil {
				t.Fatal(err)
			}
		}, reviseComment(8), "revision limit of 1 attempts", false},
		{"pull request on another branch", task.StatusAwaitingReview, func(f *fixture) {
			f.setPullRequest("feature/login", 501)
		}, reviseComment(9), "not managed by Agent Trail", false},
		{"pull request from a fork", task.StatusAwaitingReview, func(f *fixture) {
			f.setPullRequest(reviewBranch, 777)
		}, reviseComment(10), "not managed by Agent Trail", false},
		{"unknown agent trail branch", task.StatusAwaitingReview, func(f *fixture) {
			f.setPullRequest("agent-trail/other-task", 501)
		}, reviseComment(11), "not managed by Agent Trail", false},
		{"bot author", task.StatusAwaitingReview, nil,
			commentOpts{body: "/agent-trail revise", commentID: 12, pullRequest: true, userType: "Bot"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			var tk task.Task
			if tc.status == task.StatusCompleted {
				tk = f.taskAt(t, task.StatusAwaitingReview)
				if _, err := f.tasks.Transition(context.Background(), tk.ID, task.TransitionParams{To: task.StatusCompleted}); err != nil {
					t.Fatal(err)
				}
			} else {
				tk = f.taskAt(t, tc.status)
			}
			f.setPullRequest(reviewBranch, 501)
			if tc.prepare != nil {
				tc.prepare(f)
			}
			before := f.api.commentCount()
			f.recordAndProcess(t, "d-guard", "issue_comment", issueCommentJSON(t, tc.comment))

			wantStatus := "processed"
			if tc.ignored {
				wantStatus = "ignored"
			}
			if got := f.deliveryStatus(t, "d-guard"); got != wantStatus {
				t.Fatalf("delivery status = %q, want %s", got, wantStatus)
			}
			replies := f.api.commentCount() - before
			if tc.ignored && replies != 0 {
				t.Fatalf("bot command drew %d replies", replies)
			}
			if !tc.ignored && (replies != 1 || !f.lastCommentContains(t, tc.reply)) {
				t.Fatalf("replies = %d, last = %q, want one containing %q", replies, f.api.lastComment(), tc.reply)
			}
			if got := len(f.attempts(t, tk.ID)); got != 1 {
				t.Fatalf("attempts = %d, want 1", got)
			}
			cur, err := f.tasks.Get(context.Background(), tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.status == task.StatusCompleted && cur.Status != task.StatusCompleted ||
				tc.status != task.StatusCompleted && cur.Status != tc.status {
				t.Fatalf("task = %s, want untouched %s", cur.Status, tc.status)
			}
		})
	}
}

func TestReviseRedeliveryIsNoOp(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)
	f.setPullRequest(reviewBranch, 501)
	payload := issueCommentJSON(t, reviseComment(42))

	f.recordAndProcess(t, "d-revise-1", "issue_comment", payload)
	f.recordAndProcess(t, "d-revise-2", "issue_comment", payload)

	if got := f.deliveryStatus(t, "d-revise-2"); got != "ignored" {
		t.Fatalf("redelivery status = %q, want ignored", got)
	}
	if got := len(f.attempts(t, tk.ID)); got != 2 {
		t.Fatalf("attempts = %d, want exactly 2", got)
	}
	if got := f.api.checkRuns; got != 2 {
		t.Fatalf("check runs = %d, want run + one revise", got)
	}
	// the revision finished and the task is reviewable again: the old comment must not start another
	for _, to := range []task.Status{task.StatusProvisioning, task.StatusPlanning,
		task.StatusExecuting, task.StatusValidating, task.StatusPublishing, task.StatusAwaitingReview} {
		if _, err := f.tasks.Transition(context.Background(), tk.ID, task.TransitionParams{To: to}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	f.recordAndProcess(t, "d-revise-3", "issue_comment", payload)
	if got := f.deliveryStatus(t, "d-revise-3"); got != "ignored" {
		t.Fatalf("late redelivery status = %q, want ignored", got)
	}
	if got := len(f.attempts(t, tk.ID)); got != 2 {
		t.Fatalf("attempts after late redelivery = %d, want 2", got)
	}
	acks := 0
	for _, c := range f.api.comments {
		if strings.Contains(c, "revision attempt") {
			acks++
		}
	}
	if acks != 1 {
		t.Fatalf("revision acks = %d, want 1", acks)
	}
}

func TestReviseInstructionsTruncateOnRuneBoundary(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)
	f.setPullRequest(reviewBranch, 501)
	f.api.mu.Lock()
	f.api.issueComments = []IssueComment{{
		ID: 1, Body: strings.Repeat("\u00e9", instructionLimit),
		User: Author{Login: "alice", Type: "User"}, CreatedAt: time.Now().Add(time.Minute),
	}}
	f.api.mu.Unlock()

	f.recordAndProcess(t, "d-long", "issue_comment", issueCommentJSON(t, reviseComment(43)))

	if got := f.deliveryStatus(t, "d-long"); got != "processed" {
		t.Fatalf("delivery status = %q", got)
	}
	attempts := f.attempts(t, tk.ID)
	if len(attempts) != 2 || attempts[1].Instructions == nil {
		t.Fatalf("attempts = %+v", attempts)
	}
	instructions := *attempts[1].Instructions
	if len(instructions) > instructionLimit || !utf8.ValidString(instructions) {
		t.Fatalf("instructions len=%d valid=%v", len(instructions), utf8.ValidString(instructions))
	}
	if !strings.Contains(instructions, "[comment by @alice at") {
		t.Fatalf("truncated instructions lost the feedback label:\n%.300s", instructions)
	}
}

func TestFirstRevisionTakesEveryHumanComment(t *testing.T) {
	f := newFixture(t)
	tk := f.taskAt(t, task.StatusAwaitingReview)
	f.setPullRequest(reviewBranch, 501)
	f.api.mu.Lock()
	f.api.reviews = []Review{{ID: 1, Body: "early review", State: "COMMENTED",
		User: Author{Login: "bob", Type: "User"}, SubmittedAt: time.Now().Add(-48 * time.Hour)}}
	f.api.mu.Unlock()

	f.recordAndProcess(t, "d-nocutoff", "issue_comment", issueCommentJSON(t, reviseComment(44)))

	attempts := f.attempts(t, tk.ID)
	if len(attempts) != 2 || !strings.Contains(*attempts[1].Instructions, "early review") {
		t.Fatalf("attempts = %+v", attempts)
	}
}
