package github

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// fakeAPI implements API in memory and records the side effects.
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

func (f *fakeAPI) CreateCheckRun(context.Context, int64, string, string, string, string, string) (int64, error) {
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

// recordAndProcess mimics the webhook path synchronously: ledger row, then
// processing.
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
	// No installation events processed: the tables are empty, as they are
	// for an app installed before the webhook endpoint existed.
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

	// The self-heal path upserts with neither permissions nor events; a
	// previous sync's values must survive.
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
