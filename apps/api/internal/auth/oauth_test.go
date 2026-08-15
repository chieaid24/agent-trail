package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

func newTestOAuthClient(t *testing.T, handler http.Handler) *OAuthClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewOAuthClient("client-id", "client-secret", server.URL, server.URL,
		observability.NewRegistry())
}

func TestAuthorizeURL(t *testing.T) {
	c := NewOAuthClient("client-id", "secret", "https://github.example", "",
		observability.NewRegistry())
	raw := c.AuthorizeURL("state-1", "https://app.example/callback")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if parsed.Path != "/login/oauth/authorize" {
		t.Errorf("path = %q", parsed.Path)
	}
	q := parsed.Query()
	if q.Get("client_id") != "client-id" || q.Get("state") != "state-1" ||
		q.Get("redirect_uri") != "https://app.example/callback" {
		t.Errorf("query = %v", q)
	}
}

func TestExchangeCode(t *testing.T) {
	var gotBody map[string]string
	c := newTestOAuthClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "user-token"})
	}))
	token, err := c.ExchangeCode(context.Background(), "code-1", "https://app.example/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token != "user-token" {
		t.Errorf("token = %q", token)
	}
	if gotBody["client_id"] != "client-id" || gotBody["client_secret"] != "client-secret" ||
		gotBody["code"] != "code-1" {
		t.Errorf("request body = %v", gotBody)
	}
}

func TestExchangeCodeRejections(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"error field": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
		},
		"empty token": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{})
		},
		"http error": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			c := newTestOAuthClient(t, handler)
			if _, err := c.ExchangeCode(context.Background(), "code", "uri"); err == nil {
				t.Error("ExchangeCode succeeded, want error")
			}
		})
	}
}

func TestUser(t *testing.T) {
	c := newTestOAuthClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Errorf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 1234, "login": "octocat", "name": "Octo Cat",
			"avatar_url": "https://avatars.example/1234",
		})
	}))
	user, err := c.User(context.Background(), "user-token")
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	want := GitHubUser{ID: 1234, Login: "octocat", Name: "Octo Cat",
		AvatarURL: "https://avatars.example/1234"}
	if user != want {
		t.Errorf("user = %+v, want %+v", user, want)
	}
}

func TestUserRejectsMissingIdentity(t *testing.T) {
	c := newTestOAuthClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "No Id"})
	}))
	if _, err := c.User(context.Background(), "user-token"); err == nil {
		t.Error("User succeeded without id/login, want error")
	}
}

func TestInstallationAccountsPaginates(t *testing.T) {
	c := newTestOAuthClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/installations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		count := 100
		if page == 2 {
			count = 1
		}
		var rows []string
		for i := 0; i < count; i++ {
			id := (page-1)*100 + i + 1
			rows = append(rows, fmt.Sprintf(
				`{"id": %d, "account": {"id": %d, "login": "acct-%d", "type": "Organization"}}`,
				id, 1000+id, id))
		}
		_, _ = fmt.Fprintf(w, `{"installations": [%s]}`, strings.Join(rows, ","))
	}))
	accounts, err := c.InstallationAccounts(context.Background(), "user-token")
	if err != nil {
		t.Fatalf("InstallationAccounts: %v", err)
	}
	if len(accounts) != 101 {
		t.Fatalf("len(accounts) = %d, want 101", len(accounts))
	}
	if accounts[100].ID != 1101 || accounts[100].Login != "acct-101" {
		t.Errorf("last account = %+v", accounts[100])
	}
}
