// Package auth implements the dashboard session layer: GitHub OAuth user
// authorization, persisted users and memberships, and database-backed
// browser sessions (docs/adr/0013-dashboard-sessions.md).
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

const (
	maxResponseSize = 1 << 20 // 1 MiB response cap
	requestTimeout  = 15 * time.Second
)

// GitHubUser is the slice of the GitHub user object a login stores.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// InstallationAccount is one GitHub account whose app installation the
// authorized user can access; logins map it to an organization row.
type InstallationAccount struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"` // User or Organization
}

// OAuthClient drives the GitHub App web application flow: it builds the
// authorize URL, exchanges the callback code, and reads the authorized
// user. No SDK: the surface is three endpoints and the standard library
// keeps the dependency tree flat (ADR-0006).
type OAuthClient struct {
	clientID     string
	clientSecret string
	oauthBaseURL string // https://github.com; overridden in tests
	apiBaseURL   string // https://api.github.com; overridden in tests
	httpc        *http.Client

	requests *observability.Counter // agent_trail_auth_github_requests_total
	errors   *observability.Counter // agent_trail_auth_github_errors_total
}

// NewOAuthClient builds an OAuthClient. Empty base URLs mean the public
// GitHub hosts.
func NewOAuthClient(clientID, clientSecret, oauthBaseURL, apiBaseURL string, metrics *observability.Registry) *OAuthClient {
	if oauthBaseURL == "" {
		oauthBaseURL = "https://github.com"
	}
	if apiBaseURL == "" {
		apiBaseURL = "https://api.github.com"
	}
	return &OAuthClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		oauthBaseURL: strings.TrimSuffix(oauthBaseURL, "/"),
		apiBaseURL:   strings.TrimSuffix(apiBaseURL, "/"),
		httpc:        &http.Client{Timeout: requestTimeout},
		requests: metrics.Counter("agent_trail_auth_github_requests_total",
			"GitHub requests issued by the auth flow."),
		errors: metrics.Counter("agent_trail_auth_github_errors_total",
			"GitHub requests by the auth flow that failed."),
	}
}

// AuthorizeURL returns the GitHub authorize URL the login redirects to.
func (c *OAuthClient) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"client_id":    {c.clientID},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return c.oauthBaseURL + "/login/oauth/authorize?" + query.Encode()
}

// ExchangeCode swaps the callback code for a user access token. GitHub
// reports a spent or forged code as a 200 with an error field, so both
// shapes reject.
func (c *OAuthClient) ExchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	var resp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	err := c.do(ctx, http.MethodPost, c.oauthBaseURL+"/login/oauth/access_token",
		"", map[string]string{
			"client_id":     c.clientID,
			"client_secret": c.clientSecret,
			"code":          code,
			"redirect_uri":  redirectURI,
		}, &resp)
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		c.errors.Inc()
		// The error code ("bad_verification_code") is safe to surface; the
		// description is not logged to keep responses out of logs.
		return "", fmt.Errorf("auth: code exchange rejected: %s", resp.Error)
	}
	if resp.AccessToken == "" {
		c.errors.Inc()
		return "", errors.New("auth: code exchange returned no token")
	}
	return resp.AccessToken, nil
}

// User returns the authorized user.
func (c *OAuthClient) User(ctx context.Context, token string) (GitHubUser, error) {
	var user GitHubUser
	err := c.do(ctx, http.MethodGet, c.apiBaseURL+"/user", token, nil, &user)
	if err != nil {
		return GitHubUser{}, err
	}
	if user.ID <= 0 || user.Login == "" {
		return GitHubUser{}, errors.New("auth: user response missing id or login")
	}
	return user, nil
}

// InstallationAccounts returns the accounts whose installations of this
// app the user can access (GET /user/installations, paginated).
func (c *OAuthClient) InstallationAccounts(ctx context.Context, token string) ([]InstallationAccount, error) {
	const perPage = 100
	var all []InstallationAccount
	for page := 1; ; page++ {
		var resp struct {
			Installations []struct {
				Account InstallationAccount `json:"account"`
			} `json:"installations"`
		}
		endpoint := fmt.Sprintf("%s/user/installations?per_page=%d&page=%d",
			c.apiBaseURL, perPage, page)
		if err := c.do(ctx, http.MethodGet, endpoint, token, nil, &resp); err != nil {
			return nil, err
		}
		for _, installation := range resp.Installations {
			all = append(all, installation.Account)
		}
		if len(resp.Installations) < perPage {
			return all, nil
		}
	}
}

// do issues one request. token authenticates user-to-server calls; the
// code exchange authenticates with the client secret in the body instead.
func (c *OAuthClient) do(ctx context.Context, method, endpoint, token string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("auth: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return fmt.Errorf("auth: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-trail")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.requests.Inc()
	resp, err := c.httpc.Do(req)
	if err != nil {
		c.errors.Inc()
		return fmt.Errorf("auth: %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		c.errors.Inc()
		// Drain (bounded) so the connection is reused; never log the body.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		return fmt.Errorf("auth: %s %s: status %d", method, req.URL.Path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(out); err != nil {
		c.errors.Inc()
		return fmt.Errorf("auth: decode %s %s: %w", method, req.URL.Path, err)
	}
	return nil
}
