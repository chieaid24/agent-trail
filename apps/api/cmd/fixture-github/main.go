// Command fixture-github serves the GitHub simulation for the Kubernetes
// runner verification (scripts/verify-k8s-runner.sh): the fake GitHub REST
// API from internal/githubfixture, the sample bare repository over git
// smart HTTP, and a /verify endpoint reporting the seeded task's outcome.
// On startup it seeds the installation and repository and delivers the
// signed /agent-trail run webhook, so a worker Job polling the same
// database finds one queued task. Test-only: anonymous git push, throwaway
// credentials, no TLS.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/githubfixture"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const (
	installationID = 424242
	repositoryID   = 424243
	issueNumber    = 7
	webhookSecret  = "fixture-webhook-secret"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fixture-github exited", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("fixture-github requires DATABASE_URL")
	}
	publicURL := os.Getenv("FIXTURE_PUBLIC_URL")
	if publicURL == "" {
		return errors.New("fixture-github requires FIXTURE_PUBLIC_URL (base URL runner pods reach this fixture at)")
	}
	addr := os.Getenv("FIXTURE_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	logger := observability.NewLogger(os.Stdout, "fixture-github", slog.LevelInfo)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := pingUntil(ctx, db, 30*time.Second); err != nil {
		return fmt.Errorf("database not reachable: %w", err)
	}

	dir, err := os.MkdirTemp("", "agent-trail-fixture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	origin, baseSHA, err := githubfixture.BuildOrigin(dir)
	if err != nil {
		return err
	}

	f, err := newFixture(db, origin, publicURL)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	if err := f.seed(ctx); err != nil {
		_ = server.Close()
		return err
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "fixture ready",
		slog.String("event", "fixture_ready"),
		slog.String("addr", ln.Addr().String()),
		slog.String("clone_url", f.cloneURL),
		slog.String("base_commit", baseSHA),
		slog.String("task_id", f.taskID.Load().(string)),
	)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutCtx)
	case err := <-serveErr:
		return err
	}
}

// pingUntil retries the first connection: the fixture may start while the
// database pod is still coming up.
func pingUntil(ctx context.Context, db *sql.DB, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		err := db.PingContext(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// fixture routes the fake GitHub API, the git smart-HTTP repository, and
// the verification endpoints on one listener.
type fixture struct {
	db       *sql.DB
	gh       *githubfixture.Server
	git      http.Handler
	tasks    *task.Store
	ghStore  *github.Store
	client   *github.Client
	cloneURL string

	ready  atomic.Bool
	taskID atomic.Value
}

func newFixture(db *sql.DB, origin, publicURL string) (*fixture, error) {
	metrics := observability.NewRegistry()
	git, err := githubfixture.GitHandler(origin, "/git/")
	if err != nil {
		return nil, err
	}
	keyPEM, err := githubfixture.ThrowawayKey()
	if err != nil {
		return nil, err
	}
	gh := githubfixture.NewServer(origin)
	// The processor's permission check calls the fake API on this same
	// process; loop back directly rather than through the service address,
	// which has no ready endpoints until seeding completes.
	self := httptest.NewServer(gh)
	client, err := github.NewClient("1", keyPEM, self.URL, metrics)
	if err != nil {
		return nil, err
	}
	return &fixture{
		db:       db,
		gh:       gh,
		git:      git,
		tasks:    task.NewStore(db),
		ghStore:  github.NewStore(db),
		client:   client,
		cloneURL: publicURL + "/git/origin.git",
	}, nil
}

// seed registers the installation and repository and delivers the signed
// /agent-trail run webhook, leaving exactly one queued task.
func (f *fixture) seed(ctx context.Context) error {
	logger := observability.NewLogger(os.Stdout, "fixture-github", slog.LevelWarn)
	metrics := observability.NewRegistry()

	err := f.ghStore.UpsertInstallation(ctx, github.InstallationParams{
		GitHubInstallationID: installationID,
		AccountID:            installationID,
		AccountLogin:         "acme",
		AccountType:          "Organization",
	})
	if err != nil {
		return err
	}
	repo := github.Repository{
		ID: repositoryID, Name: "demo", FullName: "acme/demo",
		DefaultBranch: "main", CloneURL: f.cloneURL,
	}
	repo.Owner.Login = "acme"
	if err := f.ghStore.SyncRepositories(ctx, installationID, []github.Repository{repo}); err != nil {
		return err
	}

	stored, err := f.ghStore.RepositoryByGitHubID(ctx, repositoryID)
	if err != nil {
		return err
	}
	if stale, active, err := f.tasks.ActiveTaskForIssue(ctx, stored.ID, issueNumber); err != nil {
		return err
	} else if active {
		if _, err := f.tasks.Cancel(ctx, stale.ID, "superseded by a new fixture run"); err != nil {
			return err
		}
	}

	processor := github.NewProcessor(f.ghStore, f.tasks, f.client, logger, metrics)
	webhook := github.NewWebhook([]byte(webhookSecret), f.ghStore, processor, logger, metrics)
	req, err := githubfixture.RunCommandRequest([]byte(webhookSecret),
		installationID, repositoryID, issueNumber)
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	webhook.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusAccepted {
		return fmt.Errorf("webhook rejected the delivery: %d %s", rec.Code, rec.Body.String())
	}
	processor.Wait()

	created, active, err := f.tasks.ActiveTaskForIssue(ctx, stored.ID, issueNumber)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("webhook did not create a task")
	}
	f.taskID.Store(created.ID)
	f.ready.Store(true)
	return nil
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		if !f.ready.Load() {
			http.Error(w, "seeding", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	case r.URL.Path == "/verify":
		f.verify(w, r)
	case r.URL.Path == "/git" || len(r.URL.Path) > 4 && r.URL.Path[:5] == "/git/":
		f.git.ServeHTTP(w, r)
	default:
		f.gh.ServeHTTP(w, r)
	}
}

// verify reports the seeded task's outcome for the verification script.
func (f *fixture) verify(w http.ResponseWriter, r *http.Request) {
	if !f.ready.Load() {
		http.Error(w, "seeding", http.StatusServiceUnavailable)
		return
	}
	id, _ := f.taskID.Load().(string)
	tk, err := f.tasks.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"task_id":     tk.ID,
		"task_status": string(tk.Status),
		"pr_open":     f.gh.PROpen(),
		"comments":    f.gh.CommentCount(),
	}
	if tk.WorkingBranch != nil {
		resp["working_branch"] = *tk.WorkingBranch
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
