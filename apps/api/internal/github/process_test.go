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

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type fakeAPI struct {
	mu         sync.Mutex
	repos      []Repository
	permission string
	headSHA    string
	comments   []string
	checkRuns  int
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

func (f *fakeAPI) CreateCheckRun(context.Context, int64, string, string, CheckRunParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkRuns++
	return 777, nil
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
