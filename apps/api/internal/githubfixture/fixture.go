// simulates the slice of github the runner touches: rest endpoints, bare origin, signed webhook
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
	"strconv"
	"strings"
	"sync"
	"time"
)

// the platform authenticates as the app, so every comment it posts carries the bot identity
var botUser = map[string]any{"id": 1, "login": "agent-trail[bot]", "type": "Bot"}

var humanUser = map[string]any{"id": 9, "login": "fixture-user", "type": "User"}

type Server struct {
	origin string

	mu             sync.Mutex
	prBody         string
	prOpen         bool
	prHead         string
	checks         []map[string]any
	checkUpdates   map[int64]map[string]any
	comments       []map[string]any
	reviewComments []map[string]any
}

func NewServer(origin string) *Server {
	return &Server{origin: origin, checkUpdates: map[int64]map[string]any{}}
}

func (g *Server) PROpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prOpen
}

func (g *Server) PRBody() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prBody
}

// head branch recorded when the platform opened the pull request
func (g *Server) PRHead() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prHead
}

func (g *Server) CommentCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.comments)
}

// bodies of every comment the platform posted, oldest first
func (g *Server) Comments() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	bodies := make([]string, 0, len(g.comments))
	for _, c := range g.comments {
		bodies = append(bodies, c["body"].(string))
	}
	return bodies
}

// conclusion of the latest update to a check run, "" when never updated
func (g *Server) CheckRunConclusion(id int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if update, ok := g.checkUpdates[id]; ok {
		conclusion, _ := update["conclusion"].(string)
		return conclusion
	}
	return ""
}

func (g *Server) CheckRunCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.checks)
}

// a reviewer leaves an inline comment on the open pull request
func (g *Server) AddReviewComment(path string, line int, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reviewComments = append(g.reviewComments, map[string]any{
		"id": 1000 + len(g.reviewComments), "body": body, "path": path, "line": line,
		"diff_hunk": "@@ -1 +1 @@", "user": humanUser,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	})
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
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/fixture/branches/"):
		sha, err := gitIn(g.origin, "rev-parse", "refs/heads/"+strings.TrimPrefix(path, "/repos/acme/fixture/branches/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"commit": map[string]any{"sha": sha}})
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/fixture/collaborators/"):
		writeJSON(w, map[string]any{"permission": "admin"})
	case method == http.MethodPost && strings.HasPrefix(path, "/repos/acme/fixture/issues/") && strings.HasSuffix(path, "/comments"):
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := 100 + len(g.comments)
		g.comments = append(g.comments, map[string]any{
			"id": id, "body": body.Body, "user": botUser, "issue": issueNumber(path),
			"created_at": time.Now().UTC().Format(time.RFC3339),
		})
		writeJSON(w, map[string]any{"id": id})
	case method == http.MethodGet && strings.HasPrefix(path, "/repos/acme/fixture/issues/") && strings.HasSuffix(path, "/comments"):
		number := issueNumber(path)
		list := []map[string]any{}
		for _, c := range g.comments {
			if c["issue"] == number {
				list = append(list, c)
			}
		}
		writeJSON(w, list)
	case method == http.MethodGet && path == "/repos/acme/fixture/pulls/1/reviews":
		writeJSON(w, []map[string]any{})
	case method == http.MethodGet && path == "/repos/acme/fixture/pulls/1/comments":
		writeJSON(w, g.reviewComments)
	case method == http.MethodGet && path == "/repos/acme/fixture/pulls/1":
		if !g.prOpen {
			http.NotFound(w, r)
			return
		}
		sha, err := gitIn(g.origin, "rev-parse", "refs/heads/"+g.prHead)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"number": 1, "state": "open", "merged": false,
			"html_url": "https://github.example/acme/fixture/pull/1",
			"head": map[string]any{
				"ref": g.prHead, "sha": sha,
				"repo": map[string]any{"id": fixtureRepositoryID},
			},
		})
	case method == http.MethodGet && path == "/repos/acme/fixture/pulls":
		if g.prOpen {
			writeJSON(w, []map[string]any{{
				"number": 1, "state": "open", "draft": true,
				"html_url": "https://github.example/acme/fixture/pull/1",
			}})
			return
		}
		writeJSON(w, []map[string]any{})
	case method == http.MethodPost && path == "/repos/acme/fixture/pulls":
		var body struct {
			Body string `json:"body"`
			Head string `json:"head"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.prOpen = true
		g.prBody = body.Body
		g.prHead = body.Head
		writeJSON(w, map[string]any{
			"number": 1, "state": "open", "draft": true,
			"html_url": "https://github.example/acme/fixture/pull/1",
		})
	case method == http.MethodPatch && strings.HasPrefix(path, "/repos/acme/fixture/pulls/"):
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
	case method == http.MethodPost && path == "/repos/acme/fixture/check-runs":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["id"] = len(g.checks) + 1
		g.checks = append(g.checks, body)
		writeJSON(w, map[string]any{"id": len(g.checks)})
	case method == http.MethodPatch && strings.HasPrefix(path, "/repos/acme/fixture/check-runs/"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		id, _ := strconv.ParseInt(strings.TrimPrefix(path, "/repos/acme/fixture/check-runs/"), 10, 64)
		g.checkUpdates[id] = body
		writeJSON(w, map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

// repository id the fixture serves; webhooks name the same id so the pull request head maps to it
const fixtureRepositoryID = 424243

// "/repos/acme/fixture/issues/7/comments" -> 7
func issueNumber(path string) int {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		return 0
	}
	n, _ := strconv.Atoi(parts[4])
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func BuildOrigin(dir string) (origin, baseSHA string, err error) {
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		return "", "", err
	}
	readme := "# Fixture repository\n\nThe fake agent records its run here.\n"
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
	// fixture-only: http-backend refuses anonymous pushes otherwise
	if _, err := gitIn(origin, "config", "http.receivepack", "true"); err != nil {
		return "", "", err
	}
	return origin, baseSHA, nil
}

func RunCommandRequest(secret []byte, installationID, repositoryID, issueNumber int) (*http.Request, error) {
	payload := map[string]any{
		"action": "created",
		"comment": map[string]any{
			"id":   1,
			"body": "/agent-trail run",
			"user": humanUser,
		},
		"issue": map[string]any{
			"number": issueNumber,
			"title":  "Record the run in the fixture file",
			"body":   "Scripted issue driving the full vertical slice.",
		},
		"repository":   fixtureRepository(installationID, repositoryID),
		"installation": map[string]any{"id": installationID},
	}
	return signedRequest(secret, "issue_comment", payload)
}

// revise comment on the pull request the fixture opened; body doubles as the reviewer's ask
func ReviseCommandRequest(secret []byte, installationID, repositoryID, pullNumber int, body string) (*http.Request, error) {
	payload := map[string]any{
		"action": "created",
		"comment": map[string]any{
			"id":   2,
			"body": body,
			"user": humanUser,
		},
		"issue": map[string]any{
			"number":       pullNumber,
			"title":        "Record the run in the fixture file",
			"body":         "",
			"pull_request": map[string]any{"url": "https://github.example/acme/fixture/pull/1"},
		},
		"repository":   fixtureRepository(installationID, repositoryID),
		"installation": map[string]any{"id": installationID},
	}
	return signedRequest(secret, "issue_comment", payload)
}

// closed delivery for a pull request whose head branch was pushed to the fixture repository
func PullRequestClosedRequest(secret []byte, installationID, repositoryID, number int, headRef string, merged bool) (*http.Request, error) {
	payload := map[string]any{
		"action": "closed",
		"number": number,
		"pull_request": map[string]any{
			"number": number,
			"state":  "closed",
			"merged": merged,
			"head": map[string]any{
				"ref":  headRef,
				"repo": map[string]any{"id": repositoryID},
			},
			"base": map[string]any{"ref": "main"},
		},
		"repository":   fixtureRepository(installationID, repositoryID),
		"installation": map[string]any{"id": installationID},
	}
	return signedRequest(secret, "pull_request", payload)
}

func fixtureRepository(installationID, repositoryID int) map[string]any {
	return map[string]any{
		"id":        repositoryID,
		"full_name": "acme/fixture",
		"owner": map[string]any{
			"id": installationID, "login": "acme", "type": "Organization",
		},
	}
}

func signedRequest(secret []byte, event string, payload map[string]any) (*http.Request, error) {
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
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("fixture-%d", time.Now().UnixNano()))
	req.Header.Set("X-Hub-Signature-256", sig)
	return req, nil
}

func EphemeralKey() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), nil
}

// allowlisted env, never os.Environ: a parent git hook's GIT_DIR would redirect these commands
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
