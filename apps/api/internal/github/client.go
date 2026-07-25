package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

const (
	apiVersion      = "2022-11-28"
	userAgent       = "agent-trail"
	maxResponseSize = 5 << 20 // 5 MiB response cap
	requestTimeout  = 15 * time.Second
	// tokenExpiryMargin refreshes installation tokens well before their
	// one-hour expiry so in-flight requests never race it.
	tokenExpiryMargin = 5 * time.Minute
)

// Client calls the GitHub REST API as a GitHub App: it signs app JWTs,
// exchanges and caches installation tokens, and wraps the few endpoints the
// integration needs. No SDK: the surface is small and the standard library
// keeps the dependency tree flat (ADR-0006).
type Client struct {
	appID   string
	key     *rsa.PrivateKey
	baseURL string
	httpc   *http.Client

	requests *observability.Counter // agent_trail_github_api_requests_total
	errors   *observability.Counter // agent_trail_github_api_errors_total

	mu     sync.Mutex
	tokens map[int64]installationToken

	// now is stubbed in tests.
	now func() time.Time
}

type installationToken struct {
	token     string
	expiresAt time.Time
}

// APIError is a non-2xx GitHub API response.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %s %s: status %d", e.Method, e.Path, e.StatusCode)
}

// NewClient builds a Client from the app id and PEM-encoded private key.
// baseURL overrides the API root in tests; empty means api.github.com.
func NewClient(appID string, keyPEM []byte, baseURL string, metrics *observability.Registry) (*Client, error) {
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{
		appID:   appID,
		key:     key,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpc:   &http.Client{Timeout: requestTimeout},
		requests: metrics.Counter("agent_trail_github_api_requests_total",
			"GitHub API requests issued."),
		errors: metrics.Counter("agent_trail_github_api_errors_total",
			"GitHub API requests that failed."),
		tokens: map[int64]installationToken{},
		now:    time.Now,
	}, nil
}

func parsePrivateKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("github: private key: no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: private key: not an RSA key")
	}
	return key, nil
}

// appJWT returns a short-lived RS256 JWT identifying the app itself.
func (c *Client) appJWT() (string, error) {
	b64 := base64.RawURLEncoding
	header := b64.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	now := c.now().Unix()
	claims, err := json.Marshal(map[string]any{
		"iat": now - 60, // clock-drift allowance
		"exp": now + 9*60,
		"iss": c.appID,
	})
	if err != nil {
		return "", fmt.Errorf("github: marshal jwt claims: %w", err)
	}
	signing := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("github: sign jwt: %w", err)
	}
	return signing + "." + b64.EncodeToString(sig), nil
}

// InstallationToken returns a cached or freshly minted token for the
// installation.
func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	cached, ok := c.tokens[installationID]
	c.mu.Unlock()
	if ok && c.now().Before(cached.expiresAt.Add(-tokenExpiryMargin)) {
		return cached.token, nil
	}

	jwt, err := c.appJWT()
	if err != nil {
		return "", err
	}
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if err := c.do(ctx, http.MethodPost, path, "Bearer "+jwt, nil, &resp); err != nil {
		return "", err
	}

	c.mu.Lock()
	c.tokens[installationID] = installationToken{
		token: resp.Token, expiresAt: resp.ExpiresAt,
	}
	c.mu.Unlock()
	return resp.Token, nil
}

// Repository is the slice of the GitHub repository object the sync stores.
type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
	Private       bool   `json:"private"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// ListInstallationRepositories returns every repository the installation
// can access.
func (c *Client) ListInstallationRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	const perPage = 100
	var all []Repository
	for page := 1; ; page++ {
		var resp struct {
			Repositories []Repository `json:"repositories"`
		}
		path := fmt.Sprintf("/installation/repositories?per_page=%d&page=%d",
			perPage, page)
		if err := c.do(ctx, http.MethodGet, path, "Bearer "+token, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Repositories...)
		if len(resp.Repositories) < perPage {
			return all, nil
		}
	}
}

// CollaboratorPermission returns the user's effective permission on the
// repository: admin, write, read, or none.
func (c *Client) CollaboratorPermission(ctx context.Context, installationID int64, owner, repo, username string) (string, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	var resp struct {
		Permission string `json:"permission"`
	}
	path := fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(username))
	if err := c.do(ctx, http.MethodGet, path, "Bearer "+token, nil, &resp); err != nil {
		return "", err
	}
	return resp.Permission, nil
}

// BranchHeadSHA returns the head commit SHA of the branch.
func (c *Client) BranchHeadSHA(ctx context.Context, installationID int64, owner, repo, branch string) (string, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	var resp struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	path := fmt.Sprintf("/repos/%s/%s/branches/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	if err := c.do(ctx, http.MethodGet, path, "Bearer "+token, nil, &resp); err != nil {
		return "", err
	}
	return resp.Commit.SHA, nil
}

// CreateIssueComment posts a comment on the issue.
func (c *Client) CreateIssueComment(ctx context.Context, installationID int64, owner, repo string, issueNumber int64, body string) error {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), issueNumber)
	return c.do(ctx, http.MethodPost, path, "Bearer "+token,
		map[string]any{"body": body}, nil)
}

// CreateCheckRun creates a check run on the commit and returns its id.
func (c *Client) CreateCheckRun(ctx context.Context, installationID int64, owner, repo, name, headSHA, status string) (int64, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return 0, err
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	path := fmt.Sprintf("/repos/%s/%s/check-runs",
		url.PathEscape(owner), url.PathEscape(repo))
	err = c.do(ctx, http.MethodPost, path, "Bearer "+token, map[string]any{
		"name": name, "head_sha": headSHA, "status": status,
	}, &resp)
	if err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// do issues one API request. A non-2xx status returns *APIError; the
// response body is decoded into out when out is non-nil.
func (c *Client) do(ctx context.Context, method, path, authorization string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.requests.Inc()
	resp, err := c.httpc.Do(req)
	if err != nil {
		c.errors.Inc()
		return fmt.Errorf("github: %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		c.errors.Inc()
		// Drain (bounded) so the connection is reused; never log the body.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		return &APIError{
			StatusCode: resp.StatusCode, Method: method, Path: req.URL.Path,
		}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(out); err != nil {
		c.errors.Inc()
		return fmt.Errorf("github: decode %s %s: %w", method, req.URL.Path, err)
	}
	return nil
}
