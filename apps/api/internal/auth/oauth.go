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
	maxResponseSize = 1 << 20
	requestTimeout  = 15 * time.Second
)

type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type InstallationAccount struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"` // User or Organization
}

type OAuthClient struct {
	clientID     string
	clientSecret string
	oauthBaseURL string
	apiBaseURL   string
	httpc        *http.Client

	requests *observability.Counter
	errors   *observability.Counter
}

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

func (c *OAuthClient) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"client_id":    {c.clientID},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return c.oauthBaseURL + "/login/oauth/authorize?" + query.Encode()
}

// github reports spent/forged codes as 200 with an error field, so both shapes reject
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
		// error code is safe to surface; description never logged
		return "", fmt.Errorf("auth: code exchange rejected: %s", resp.Error)
	}
	if resp.AccessToken == "" {
		c.errors.Inc()
		return "", errors.New("auth: code exchange returned no token")
	}
	return resp.AccessToken, nil
}

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
		// bounded drain for connection reuse; never log the body
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		return fmt.Errorf("auth: %s %s: status %d", method, req.URL.Path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(out); err != nil {
		c.errors.Inc()
		return fmt.Errorf("auth: decode %s %s: %w", method, req.URL.Path, err)
	}
	return nil
}
