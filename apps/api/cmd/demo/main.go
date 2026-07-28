// Command demo runs the complete issue-to-PR vertical slice in one process
// (VISION.md): a signed GitHub webhook creates a task, the fake agent edits
// an isolated git worktree, trusted validation and evidence run, and
// publishing commits, pushes, and opens one evidence-backed draft pull
// request. GitHub itself is simulated by a local API server and a local
// bare repository, so the demo needs only PostgreSQL (DATABASE_URL) and
// git; every other component - webhook verification, the task store, the
// runner, the GitHub client - is the production code path.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
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
	origin, baseSHA, cleanupRepo, err := buildOrigin()
	if err != nil {
		return err
	}
	defer cleanupRepo()
	fmt.Println("   origin:", origin)
	fmt.Println("   main at:", baseSHA)

	step("Starting a simulated GitHub API")
	gh := newFakeGitHub(origin)
	server, err := gh.serve()
	if err != nil {
		return err
	}
	defer server.Close()

	keyPEM, err := throwawayKey()
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
	gh.mu.Lock()
	prBody := gh.prBody
	comments := len(gh.comments)
	gh.mu.Unlock()
	if prBody == "" {
		return errors.New("no draft pull request was created")
	}
	fmt.Printf("   draft PR #1 opened, %d issue comment(s) posted\n", comments)

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
	payload := map[string]any{
		"action": "created",
		"comment": map[string]any{
			"id":   1,
			"body": "/agent-trail run",
			"user": map[string]any{"id": 9, "login": "demo-user", "type": "User"},
		},
		"issue": map[string]any{
			"number": demoIssueNumber,
			"title":  "Demo: record the run in the fixture file",
			"body":   "Scripted demo issue driving the full vertical slice.",
		},
		"repository": map[string]any{
			"id": demoRepositoryID,
			"owner": map[string]any{
				"id": demoInstallationID, "login": "acme", "type": "Organization",
			},
		},
		"installation": map[string]any{"id": demoInstallationID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("demo-%d", time.Now().UnixNano()))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	webhook.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		return fmt.Errorf("webhook rejected the delivery: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

// fakeGitHub simulates the handful of REST endpoints publishing calls.
type fakeGitHub struct {
	origin string

	mu       sync.Mutex
	prBody   string
	prOpen   bool
	checks   []map[string]any
	comments []string
}

func newFakeGitHub(origin string) *fakeGitHub {
	return &fakeGitHub{origin: origin}
}

type demoServer struct {
	URL    string
	server *http.Server
}

func (d *demoServer) Close() {
	_ = d.server.Close()
}

func (g *fakeGitHub) serve() (*demoServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: g, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &demoServer{URL: "http://" + ln.Addr().String(), server: srv}, nil
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	path, method := r.URL.Path, r.Method
	switch {
	case method == http.MethodPost && strings.HasPrefix(path, "/app/installations/"):
		writeJSON(w, map[string]any{
			"token":      "demo-token",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/demo/branches/"):
		sha, err := gitOut(g.origin, "rev-parse", "refs/heads/"+strings.TrimPrefix(path, "/repos/acme/demo/branches/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"commit": map[string]any{"sha": sha}})
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/demo/collaborators/"):
		writeJSON(w, map[string]any{"permission": "admin"})
	case method == http.MethodPost && strings.HasPrefix(path, "/repos/acme/demo/issues/"):
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.comments = append(g.comments, body.Body)
		writeJSON(w, map[string]any{"id": len(g.comments)})
	case method == http.MethodGet && path == "/repos/acme/demo/pulls":
		if g.prOpen {
			writeJSON(w, []map[string]any{{
				"number": 1, "state": "open", "draft": true,
				"html_url": "https://github.example/acme/demo/pull/1",
			}})
			return
		}
		writeJSON(w, []map[string]any{})
	case method == http.MethodPost && path == "/repos/acme/demo/pulls":
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.prOpen = true
		g.prBody = body.Body
		writeJSON(w, map[string]any{
			"number": 1, "state": "open", "draft": true,
			"html_url": "https://github.example/acme/demo/pull/1",
		})
	case method == http.MethodPatch && strings.HasPrefix(path, "/repos/acme/demo/pulls/"):
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Body != "" {
			g.prBody = body.Body
		}
		writeJSON(w, map[string]any{})
	case method == http.MethodGet && strings.HasSuffix(path, "/check-runs"):
		writeJSON(w, map[string]any{"check_runs": g.checks})
	case method == http.MethodPost && path == "/repos/acme/demo/check-runs":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["id"] = len(g.checks) + 1
		g.checks = append(g.checks, body)
		writeJSON(w, map[string]any{"id": len(g.checks)})
	case method == http.MethodPatch && strings.HasPrefix(path, "/repos/acme/demo/check-runs/"):
		writeJSON(w, map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// buildOrigin creates the sample repository: a bare origin with one commit.
func buildOrigin() (origin, baseSHA string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "agent-trail-demo-repo-")
	if err != nil {
		return "", "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		cleanup()
		return "", "", nil, err
	}
	readme := "# Demo repository\n\nThe fake agent records its run here.\n"
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte(readme), 0o644); err != nil {
		cleanup()
		return "", "", nil, err
	}
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial"},
	}
	for _, args := range steps {
		if _, err := gitIn(src, args...); err != nil {
			cleanup()
			return "", "", nil, err
		}
	}
	baseSHA, err = gitIn(src, "rev-parse", "HEAD")
	if err != nil {
		cleanup()
		return "", "", nil, err
	}
	origin = filepath.Join(dir, "origin.git")
	if _, err := gitIn(dir, "clone", "-q", "--bare", src, "origin.git"); err != nil {
		cleanup()
		return "", "", nil, err
	}
	return origin, baseSHA, cleanup, nil
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agent Trail Demo", "GIT_AUTHOR_EMAIL=demo@example.invalid",
		"GIT_COMMITTER_NAME=Agent Trail Demo", "GIT_COMMITTER_EMAIL=demo@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOut(dir string, args ...string) (string, error) {
	return gitIn(dir, args...)
}

// throwawayKey generates a single-run RSA key for the app JWT.
func throwawayKey() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), nil
}
