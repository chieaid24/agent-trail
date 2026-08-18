// Command demo runs the complete issue-to-PR vertical slice in one process
// (VISION.md): a signed GitHub webhook creates a task, the fake agent edits
// an isolated git worktree, trusted validation and evidence run, and
// publishing commits, pushes, and opens one evidence-backed draft pull
// request. GitHub itself is simulated by internal/githubfixture - a local
// API server and a local bare repository - so the demo needs only PostgreSQL
// (DATABASE_URL) and git; every other component - webhook verification, the
// task store, the runner, the GitHub client - is the production code path.
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
	demoInstallationID = 424242
	demoRepositoryID   = 424243
	demoIssueNumber    = 7
	webhookSecret      = "demo-webhook-secret"
)

func main() {
	if err := run(os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run(databaseURL string) error {
	if databaseURL == "" {
		return errors.New("demo requires DATABASE_URL (run: make infra migrate)")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("demo requires git on PATH")
	}
	ctx := context.Background()
	logger := observability.NewLogger(io.Discard, "demo", slog.LevelError)
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
	dir, err := os.MkdirTemp("", "agent-trail-demo-repo-")
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

	keyPEM, err := githubfixture.ThrowawayKey()
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

	repo, err := ghStore.RepositoryByGitHubID(ctx, demoRepositoryID)
	if err != nil {
		return err
	}
	// A previous demo run leaves its task in awaiting_review; cancel it so
	// the one-active-task-per-issue rule lets this run create a fresh one.
	if stale, active, err := tasks.ActiveTaskForIssue(ctx, repo.ID, demoIssueNumber); err != nil {
		return err
	} else if active {
		if _, err := tasks.Cancel(ctx, stale.ID, "superseded by a new demo run"); err != nil {
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
	created, active, err := tasks.ActiveTaskForIssue(ctx, repo.ID, demoIssueNumber)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("webhook did not create a task")
	}
	fmt.Println("   task:", created.ID)

	step("Running the task: fake agent, trusted validation, evidence, publishing")
	workspaceRoot, err := os.MkdirTemp("", "agent-trail-demo-ws-")
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
		Type: "process", HostnameOrPod: "demo",
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
	step("Result")
	fmt.Println("   task status:", final.Status)
	if final.WorkingBranch != nil {
		fmt.Println("   branch pushed:", *final.WorkingBranch)
	}
	prBody := gh.PRBody()
	if prBody == "" {
		return errors.New("no draft pull request was created")
	}
	fmt.Printf("   draft PR #1 opened, %d issue comment(s) posted\n", gh.CommentCount())

	step("Timeline")
	events, err := tasks.Events(ctx, created.ID, 0)
	if err != nil {
		return err
	}
	for _, ev := range events {
		fmt.Printf("   %-28s %s\n", ev.EventType, ev.Source)
	}

	step("Draft pull request body (evidence-backed)")
	fmt.Println(indent(prBody, "   | "))

	if final.Status != task.StatusAwaitingReview {
		return fmt.Errorf("task ended at %s, want awaiting_review", final.Status)
	}
	fmt.Println("\nDemo complete: the task now awaits human review on the draft PR.")
	return nil
}

// claimTask claims until it owns the demo task (a dev worker may be polling
// the same database; those claims are for other tasks).
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
	return nil, errors.New("could not claim the demo task (another worker may have taken it)")
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

// seedRepository registers the demo installation and repository the way an
// installation webhook would.
func seedRepository(ctx context.Context, s *github.Store, origin string) error {
	err := s.UpsertInstallation(ctx, github.InstallationParams{
		GitHubInstallationID: demoInstallationID,
		AccountID:            demoInstallationID,
		AccountLogin:         "acme",
		AccountType:          "Organization",
	})
	if err != nil {
		return err
	}
	repo := github.Repository{
		ID: demoRepositoryID, Name: "demo", FullName: "acme/demo",
		DefaultBranch: "main", CloneURL: origin,
	}
	repo.Owner.Login = "acme"
	return s.SyncRepositories(ctx, demoInstallationID, []github.Repository{repo})
}

// deliverRunCommand posts a signed /agent-trail run issue comment to the
// webhook handler, exactly as GitHub would.
func deliverRunCommand(webhook http.Handler) error {
	req, err := githubfixture.RunCommandRequest([]byte(webhookSecret),
		demoInstallationID, demoRepositoryID, demoIssueNumber)
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	webhook.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		return fmt.Errorf("webhook rejected the delivery: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

type demoServer struct {
	URL    string
	server *http.Server
}

func (d *demoServer) Close() {
	_ = d.server.Close()
}

// serve exposes the fixture on a loopback port for the GitHub client.
func serve(h http.Handler) (*demoServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &demoServer{URL: "http://" + ln.Addr().String(), server: srv}, nil
}
