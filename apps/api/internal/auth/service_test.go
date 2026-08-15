package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeGitHub serves the three endpoints CompleteLogin touches.
func fakeGitHub(t *testing.T, accounts string, failInstallations bool) *OAuthClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "user-token"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "login": "octocat", "name": "Octo Cat",
			"avatar_url": "https://avatars.example/42",
		})
	})
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		if failInstallations {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprintf(w, `{"installations": [%s]}`, accounts)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return NewOAuthClient("client-id", "client-secret", server.URL, server.URL,
		observability.NewRegistry())
}

func TestCompleteLogin(t *testing.T) {
	db := dbtest.Open(t)
	orgID := seedOrganization(t, db, 100, "acme")
	oauth := fakeGitHub(t,
		`{"id": 1, "account": {"id": 100, "login": "acme", "type": "Organization"}}`,
		false)
	service := NewService(oauth, NewStore(db), testLogger(),
		"https://github.example/apps/agent-trail/installations/new")

	user, token, err := service.CompleteLogin(context.Background(), "code-1",
		"https://app.example/callback")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if user.GitHubLogin != "octocat" || token == "" {
		t.Errorf("user = %+v, token %q", user, token)
	}

	resolved, err := service.SessionUser(context.Background(), token)
	if err != nil || resolved.ID != user.ID {
		t.Errorf("SessionUser = %+v, %v", resolved, err)
	}
	assertMemberships(t, db, user.ID, []string{orgID})

	member, err := service.MemberOfRepository(context.Background(), user.ID,
		seedRepository(t, db, orgID, 1001, "widget"))
	if err != nil || !member {
		t.Errorf("MemberOfRepository = %v, %v; want true", member, err)
	}

	if err := service.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := service.SessionUser(context.Background(), token); err != ErrNoSession {
		t.Errorf("after logout error = %v, want ErrNoSession", err)
	}
}

func TestCompleteLoginSurvivesMembershipSyncFailure(t *testing.T) {
	db := dbtest.Open(t)
	seedOrganization(t, db, 100, "acme")
	oauth := fakeGitHub(t, "", true)
	service := NewService(oauth, NewStore(db), testLogger(), "")

	user, token, err := service.CompleteLogin(context.Background(), "code-1",
		"https://app.example/callback")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if token == "" {
		t.Fatal("no session token")
	}
	assertMemberships(t, db, user.ID, nil)
}
