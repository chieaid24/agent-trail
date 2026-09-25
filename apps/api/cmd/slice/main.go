package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/githubfixture"
	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/runner"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

const (
	fixtureInstallationID = 424242
	fixtureRepositoryID   = 424243
	fixtureIssueNumber    = 7
	webhookSecret         = "local-webhook-secret"
)

func main() {
	if err := run(os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, "slice failed:", err)
		os.Exit(1)
	}
}

func run(databaseURL string) error {
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required (run: make infra migrate)")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is required on PATH")
	}
	ctx := context.Background()
	logger := observability.NewLogger(io.Discard, "slice", slog.LevelError)
	metrics := observability.NewRegistry()

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database not reachable (run: make infra migrate): %w", err)
	}

	step("Preparing a sample repository (local bare origin with one commit)")
	dir, err := os.MkdirTemp("", "agent-trail-slice-repo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	origin, baseSHA, err := githubfixture.BuildOrigin(dir)
	if err != nil {
		return err
	}
	fmt.Println("   origin:", origin)
	fmt.Println("   main at:", baseSHA)

	step("Starting a simulated GitHub API")
	gh := githubfixture.NewServer(origin)
	server, err := serve(gh)
	if err != nil {
		return err
	}
	defer server.Close()

	keyPEM, err := githubfixture.EphemeralKey()
	if err != nil {
		return err
	}
	client, err := github.NewClient("1", keyPEM, server.URL, metrics)
	if err != nil {
		return err
	}

	ghStore := github.NewStore(db)
	tasks := task.NewStore(db)
	if err := seedRepository(ctx, ghStore, origin); err != nil {
		return err
	}

	repo, err := ghStore.RepositoryByGitHubID(ctx, fixtureRepositoryID)
	if err != nil {
		return err
	}
	// cancel a previous run's task so one-active-per-issue allows a fresh one
	if stale, active, err := tasks.ActiveTaskForIssue(ctx, repo.ID, fixtureIssueNumber); err != nil {
		return err
	} else if active {
		if _, err := tasks.Cancel(ctx, stale.ID, "superseded by a new slice run"); err != nil {
			return err
		}
	}

	step("Delivering a signed issue_comment webhook: /agent-trail run")
	processor := github.NewProcessor(ghStore, tasks, client, logger, metrics)
	webhook := github.NewWebhook([]byte(webhookSecret), ghStore, processor, logger, metrics)
	if err := deliverRunCommand(webhook); err != nil {
		return err
	}
	processor.Wait()
	created, active, err := tasks.ActiveTaskForIssue(ctx, repo.ID, fixtureIssueNumber)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("webhook did not create a task")
	}
	fmt.Println("   task:", created.ID)

	step("Running the task: fake agent, trusted validation, evidence, publishing")
	workspaceRoot, err := os.MkdirTemp("", "agent-trail-slice-ws-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspaceRoot)
	workspaces, err := gitworkspace.New(workspaceRoot, logger, metrics)
	if err != nil {
		return err
	}
	store := runner.NewStore(db)
	reg, err := store.Register(ctx, runner.RegisterParams{
		Type: "process", HostnameOrPod: "local",
	})
	if err != nil {
		return err
	}
	worker := &runner.Executor{
		Tasks:         tasks,
		Store:         store,
		Validations:   validation.NewStore(db),
		Evidence:      evidence.NewStore(db),
		Adapter:       agent.NewFake(),
		Logger:        logger,
		Workspaces:    workspaces,
		GitHub:        client,
		Repos:         ghStore,
		LeaseDuration: time.Minute,
	}
	claim, err := claimTask(ctx, store, reg.ID, created.ID)
	if err != nil {
		return err
	}
	if err := worker.Execute(ctx, reg.ID, claim); err != nil {
		return err
	}

	final, err := tasks.Get(ctx, created.ID)
	if err != nil {
		return err
	}
	step("Result of attempt 1")
	fmt.Println("   task status:", final.Status)
	if final.WorkingBranch == nil {
		return errors.New("task has no working branch")
	}
	fmt.Println("   branch pushed:", *final.WorkingBranch)
	if gh.PRBody() == "" {
		return errors.New("no draft pull request was created")
	}
	fmt.Printf("   draft PR #1 opened, %d comment(s) posted\n", gh.CommentCount())
	if final.Status != task.StatusAwaitingReview {
		return fmt.Errorf("task ended at %s, want awaiting_review", final.Status)
	}
	// check run 1 is the trigger run the run command created; publish must have completed it
	if got := gh.CheckRunConclusion(1); got != "success" {
		return fmt.Errorf("trigger check run conclusion = %q, want success", got)
	}

	step("Reviewer leaves an inline comment and comments /agent-trail revise on the draft PR")
	gh.AddReviewComment(agent.FixtureFile, 1, "Please also note which files changed.")
	if err := deliverReviseCommand(webhook); err != nil {
		return err
	}
	processor.Wait()
	revised, err := tasks.Get(ctx, created.ID)
	if err != nil {
		return err
	}
	attempts, err := tasks.Attempts(ctx, created.ID)
	if err != nil {
		return err
	}
	if revised.Status != task.StatusQueued || len(attempts) != 2 || attempts[1].BaseCommitSHA == nil {
		return fmt.Errorf("revise command left the task %s with %d attempt(s): %s",
			revised.Status, len(attempts), lastLine(gh.Comments()))
	}
	fmt.Println("   attempt 2 queued from pull request head", *attempts[1].BaseCommitSHA)
	// the inline review comment plus the revise command itself
	if len(attempts[1].Feedback) != 2 {
		return fmt.Errorf("attempt 2 carries %d feedback item(s), want 2", len(attempts[1].Feedback))
	}
	fmt.Printf("   %d feedback item(s) composed into the attempt instructions\n", len(attempts[1].Feedback))

	step("Running the revision: same branch, new attempt, re-validated")
	claim2, err := claimTask(ctx, store, reg.ID, created.ID)
	if err != nil {
		return err
	}
	if claim2.AttemptNumber != 2 {
		return fmt.Errorf("claimed attempt %d, want 2", claim2.AttemptNumber)
	}
	if err := worker.Execute(ctx, reg.ID, claim2); err != nil {
		return err
	}
	afterRevision, err := tasks.Get(ctx, created.ID)
	if err != nil {
		return err
	}
	if afterRevision.Status != task.StatusAwaitingReview {
		return fmt.Errorf("revision ended at %s, want awaiting_review", afterRevision.Status)
	}
	prBody := gh.PRBody()
	for _, want := range []string{"## Attempts", "| 1 | `", "| 2 | `"} {
		if !strings.Contains(prBody, want) {
			return fmt.Errorf("pull request body lacks %q after the revision", want)
		}
	}
	summary := lastLine(gh.Comments())
	if !strings.Contains(summary, "published revision attempt 2") {
		return fmt.Errorf("last comment is not the revision summary: %q", summary)
	}
	if got := gh.CheckRunConclusion(3); got != "success" {
		return fmt.Errorf("revision trigger check run conclusion = %q, want success", got)
	}
	fmt.Printf("   revision published: %d check run(s), %d comment(s)\n", gh.CheckRunCount(), gh.CommentCount())

	step("Merging the pull request completes the task")
	if err := deliverPullRequestMerged(webhook, *final.WorkingBranch); err != nil {
		return err
	}
	processor.Wait()
	merged, err := tasks.Get(ctx, created.ID)
	if err != nil {
		return err
	}
	if merged.Status != task.StatusCompleted {
		return fmt.Errorf("task ended at %s after merge, want completed", merged.Status)
	}
	fmt.Println("   task status:", merged.Status)

	step("Timeline")
	events, err := tasks.Events(ctx, created.ID, 0)
	if err != nil {
		return err
	}
	for _, ev := range events {
		fmt.Printf("   attempt %d  %-28s %s\n", ev.AttemptNumber, ev.EventType, ev.Source)
	}

	step("Pull request body (evidence-backed, with attempts history)")
	fmt.Println(indent(prBody, "   | "))

	step("Revision summary comment")
	fmt.Println(indent(summary, "   | "))

	fmt.Println("\nSlice complete: run, revise, and merge all landed on one branch and one pull request.")
	return nil
}

func lastLine(comments []string) string {
	if len(comments) == 0 {
		return ""
	}
	return comments[len(comments)-1]
}

// claims until it owns the slice task; a local worker may claim other tasks meanwhile
func claimTask(ctx context.Context, store *runner.Store, runnerID, taskID string) (*runner.Claim, error) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		c, err := store.Claim(ctx, runnerID, time.Minute)
		if err != nil {
			return nil, err
		}
		if c != nil && c.TaskID == taskID {
			return c, nil
		}
		if c != nil {
			if err := store.ReleaseLease(ctx, c.AttemptID, runnerID); err != nil {
				return nil, err
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, errors.New("could not claim the slice task (another worker may have taken it)")
}

func step(title string) {
	fmt.Println("\n==>", title)
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func seedRepository(ctx context.Context, s *github.Store, origin string) error {
	err := s.UpsertInstallation(ctx, github.InstallationParams{
		GitHubInstallationID: fixtureInstallationID,
		AccountID:            fixtureInstallationID,
		AccountLogin:         "acme",
		AccountType:          "Organization",
	})
	if err != nil {
		return err
	}
	repo := github.Repository{
		ID: fixtureRepositoryID, Name: "fixture", FullName: "acme/fixture",
		DefaultBranch: "main", CloneURL: origin,
	}
	repo.Owner.Login = "acme"
	return s.SyncRepositories(ctx, fixtureInstallationID, []github.Repository{repo})
}

func deliverRunCommand(webhook http.Handler) error {
	req, err := githubfixture.RunCommandRequest([]byte(webhookSecret),
		fixtureInstallationID, fixtureRepositoryID, fixtureIssueNumber)
	if err != nil {
		return err
	}
	return deliver(webhook, req)
}

func deliverReviseCommand(webhook http.Handler) error {
	req, err := githubfixture.ReviseCommandRequest([]byte(webhookSecret),
		fixtureInstallationID, fixtureRepositoryID, 1,
		"/agent-trail revise\nAlso mention the validation file in the notes.")
	if err != nil {
		return err
	}
	return deliver(webhook, req)
}

func deliverPullRequestMerged(webhook http.Handler, headRef string) error {
	req, err := githubfixture.PullRequestClosedRequest([]byte(webhookSecret),
		fixtureInstallationID, fixtureRepositoryID, 1, headRef, true)
	if err != nil {
		return err
	}
	return deliver(webhook, req)
}

func deliver(webhook http.Handler, req *http.Request) error {
	rec := httptest.NewRecorder()
	webhook.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		return fmt.Errorf("webhook rejected the delivery: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

type fixtureServer struct {
	URL    string
	server *http.Server
}

func (d *fixtureServer) Close() {
	_ = d.server.Close()
}

func serve(h http.Handler) (*fixtureServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &fixtureServer{URL: "http://" + ln.Addr().String(), server: srv}, nil
}
