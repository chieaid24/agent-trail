// Package githubfixture simulates the slice of GitHub the runner touches:
// the handful of REST endpoints publishing calls, a bare git origin, and a
// signed /agent-trail run webhook delivery. cmd/demo embeds it in-process;
// cmd/fixture-github serves it over the network so a runner isolated in a
// Kubernetes Job can exercise the full task pipeline without GitHub.
package githubfixture

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Server simulates the GitHub REST endpoints publishing calls, backed by a
// bare origin repository on disk.
type Server struct {
	origin string

	mu       sync.Mutex
	prBody   string
	prOpen   bool
	checks   []map[string]any
	comments []string
}

// NewServer returns a fixture for the bare repository at origin.
func NewServer(origin string) *Server {
	return &Server{origin: origin}
}

// PROpen reports whether a draft pull request was opened.
func (g *Server) PROpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prOpen
}

// PRBody returns the current pull request body.
func (g *Server) PRBody() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prBody
}

// CommentCount returns how many issue comments were posted.
func (g *Server) CommentCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.comments)
}

func (g *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	path, method := r.URL.Path, r.Method
	switch {
	case method == http.MethodPost && strings.HasPrefix(path, "/app/installations/"):
		writeJSON(w, map[string]any{
			"token":      "fixture-token",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/demo/branches/"):
		sha, err := gitIn(g.origin, "rev-parse", "refs/heads/"+strings.TrimPrefix(path, "/repos/acme/demo/branches/"))
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

// BuildOrigin creates the sample repository under dir: a bare origin with
// one commit on main, anonymous push enabled for the fixture's smart-HTTP
// serving. Returns the origin path and the base commit.
func BuildOrigin(dir string) (origin, baseSHA string, err error) {
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		return "", "", err
	}
	readme := "# Demo repository\n\nThe fake agent records its run here.\n"
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte(readme), 0o644); err != nil {
		return "", "", err
	}
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial"},
	}
	for _, args := range steps {
		if _, err := gitIn(src, args...); err != nil {
			return "", "", err
		}
	}
	baseSHA, err = gitIn(src, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	origin = filepath.Join(dir, "origin.git")
	if _, err := gitIn(dir, "clone", "-q", "--bare", src, "origin.git"); err != nil {
		return "", "", err
	}
	// Fixture-only: git http-backend refuses anonymous pushes otherwise.
	if _, err := gitIn(origin, "config", "http.receivepack", "true"); err != nil {
		return "", "", err
	}
	return origin, baseSHA, nil
}

// RunCommandRequest builds a signed /agent-trail run issue_comment webhook
// request, exactly as GitHub would deliver it.
func RunCommandRequest(secret []byte, installationID, repositoryID, issueNumber int) (*http.Request, error) {
	payload := map[string]any{
		"action": "created",
		"comment": map[string]any{
			"id":   1,
			"body": "/agent-trail run",
			"user": map[string]any{"id": 9, "login": "demo-user", "type": "User"},
		},
		"issue": map[string]any{
			"number": issueNumber,
			"title":  "Demo: record the run in the fixture file",
			"body":   "Scripted demo issue driving the full vertical slice.",
		},
		"repository": map[string]any{
			"id": repositoryID,
			"owner": map[string]any{
				"id": installationID, "login": "acme", "type": "Organization",
			},
		},
		"installation": map[string]any{"id": installationID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("fixture-%d", time.Now().UnixNano()))
	req.Header.Set("X-Hub-Signature-256", sig)
	return req, nil
}

// ThrowawayKey generates a single-run RSA key for the app JWT.
func ThrowawayKey() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), nil
}

// gitIn runs git in dir with an allowlisted environment, never os.Environ():
// under a git hook the parent exports GIT_DIR/GIT_INDEX_FILE, and inheriting
// them makes these commands operate on the invoking repository instead.
func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C",
		"GIT_AUTHOR_NAME=Agent Trail Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Agent Trail Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}
