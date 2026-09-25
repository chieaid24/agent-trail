package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/auth"
	"github.com/chieaid24/agent-trail/apps/api/internal/dashboard"
)

const testOrigin = "http://dashboard.test:3000"

type fakeAuth struct {
	sessionToken string
	user         auth.User
	member       bool
	memberErr    error
	loginErr     error
	loggedOut    []string
	installURL   string
}

func (f *fakeAuth) AuthorizeURL(state, redirectURI string) string {
	return "https://github.test/login/oauth/authorize?" + url.Values{
		"state": {state}, "redirect_uri": {redirectURI},
	}.Encode()
}

func (f *fakeAuth) CompleteLogin(_ context.Context, code, _ string) (auth.User, string, error) {
	if f.loginErr != nil {
		return auth.User{}, "", f.loginErr
	}
	return f.user, f.sessionToken, nil
}

func (f *fakeAuth) SessionUser(_ context.Context, token string) (auth.User, error) {
	if token != f.sessionToken {
		return auth.User{}, auth.ErrNoSession
	}
	return f.user, nil
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.loggedOut = append(f.loggedOut, token)
	return nil
}

func (f *fakeAuth) MemberOfRepository(context.Context, string, string) (bool, error) {
	return f.member, f.memberErr
}

func (f *fakeAuth) SessionTTL() time.Duration { return time.Hour }
func (f *fakeAuth) InstallURL() string        { return f.installURL }

func validFakeAuth() *fakeAuth {
	return &fakeAuth{
		sessionToken: "good-token",
		user: auth.User{ID: "2f34f662-77f0-4a4f-a1c4-56176aa264c9",
			GitHubUserID: 42, GitHubLogin: "octocat"},
		member:     true,
		installURL: "https://github.test/apps/agent-trail/installations/new",
	}
}

func authedHandler(f *fakeAuth, options ...Option) http.Handler {
	options = append([]Option{WithAuth(f, testOrigin, false),
		WithDashboard(fakeDashboard{})}, options...)
	return New(testLogger(), nil, nil, nil, nil, nil, nil, options...).Handler()
}

func doWithCookie(t *testing.T, h http.Handler, method, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doWithCookieAndBody(t *testing.T, h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(value string) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: value}
}

func findCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	for _, cookie := range res.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestAuthRoutesUnavailableWithoutConfiguration(t *testing.T) {
	h := New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithDashboard(fakeDashboard{})).Handler()
	for _, tc := range [][2]string{
		{http.MethodGet, "/auth/github/start"},
		{http.MethodGet, "/auth/github/callback"},
		{http.MethodPost, "/auth/logout"},
		{http.MethodGet, "/me"},
	} {
		rec := do(t, h, tc[0], tc[1], "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", tc[0], tc[1], rec.Code)
		}
	}
	rec := do(t, h, http.MethodGet, "/api/v1/organizations", "")
	if rec.Code != http.StatusOK {
		t.Errorf("organizations without auth = %d, want 200", rec.Code)
	}
}

func TestRequireSessionGuardsAPI(t *testing.T) {
	f := validFakeAuth()
	h := authedHandler(f)

	if rec := do(t, h, http.MethodGet, "/api/v1/organizations", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie = %d, want 401", rec.Code)
	}
	rec := doWithCookie(t, h, http.MethodGet, "/api/v1/organizations",
		sessionCookie("wrong-token"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad cookie = %d, want 401", rec.Code)
	}
	rec = doWithCookie(t, h, http.MethodGet, "/api/v1/organizations",
		sessionCookie("good-token"))
	if rec.Code != http.StatusOK {
		t.Errorf("good cookie = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", rec.Code)
	}
}

func TestAuthStartRedirectsWithBoundState(t *testing.T) {
	h := authedHandler(validFakeAuth())
	rec := do(t, h, http.MethodGet, "/auth/github/start", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	state := findCookie(t, rec, stateCookieName)
	if state == nil || state.Value == "" || !state.HttpOnly ||
		state.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie = %+v", state)
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("state") != state.Value {
		t.Errorf("authorize state %q != cookie %q",
			location.Query().Get("state"), state.Value)
	}
	if got := location.Query().Get("redirect_uri"); got != testOrigin+callbackPath {
		t.Errorf("redirect_uri = %q", got)
	}
}

func TestAuthCallback(t *testing.T) {
	state := &http.Cookie{Name: stateCookieName, Value: "state-1"}
	cases := []struct {
		name      string
		path      string
		cookies   []*http.Cookie
		loginErr  error
		wantError string
	}{
		{name: "missing state cookie", path: "/auth/github/callback?code=c&state=state-1",
			wantError: "state_mismatch"},
		{name: "state mismatch", path: "/auth/github/callback?code=c&state=other",
			cookies: []*http.Cookie{state}, wantError: "state_mismatch"},
		{name: "github denied", path: "/auth/github/callback?error=access_denied&state=state-1",
			cookies: []*http.Cookie{state}, wantError: "github_denied"},
		{name: "missing code", path: "/auth/github/callback?state=state-1",
			cookies: []*http.Cookie{state}, wantError: "missing_code"},
		{name: "login failure", path: "/auth/github/callback?code=c&state=state-1",
			cookies:  []*http.Cookie{state},
			loginErr: errors.New("boom"), wantError: "login_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validFakeAuth()
			f.loginErr = tc.loginErr
			h := authedHandler(f)
			rec := doWithCookie(t, h, http.MethodGet, tc.path, tc.cookies...)
			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", rec.Code)
			}
			want := testOrigin + "/login?error=" + tc.wantError
			if got := rec.Header().Get("Location"); got != want {
				t.Errorf("location = %q, want %q", got, want)
			}
			if cookie := findCookie(t, rec, sessionCookieName); cookie != nil {
				t.Errorf("session cookie set on failure: %+v", cookie)
			}
		})
	}
}

func TestAuthCallbackSuccess(t *testing.T) {
	h := authedHandler(validFakeAuth())
	rec := doWithCookie(t, h, http.MethodGet,
		"/auth/github/callback?code=c&state=state-1",
		&http.Cookie{Name: stateCookieName, Value: "state-1"})
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != testOrigin+"/" {
		t.Errorf("location = %q", got)
	}
	session := findCookie(t, rec, sessionCookieName)
	if session == nil || session.Value != "good-token" || !session.HttpOnly ||
		session.SameSite != http.SameSiteLaxMode || session.MaxAge != 3600 {
		t.Fatalf("session cookie = %+v", session)
	}
	state := findCookie(t, rec, stateCookieName)
	if state == nil || state.MaxAge != -1 {
		t.Errorf("state cookie not cleared: %+v", state)
	}
}

func TestAuthLogout(t *testing.T) {
	f := validFakeAuth()
	h := authedHandler(f)

	rec := doWithCookie(t, h, http.MethodPost, "/auth/logout",
		sessionCookie("good-token"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(f.loggedOut) != 1 || f.loggedOut[0] != "good-token" {
		t.Errorf("loggedOut = %v", f.loggedOut)
	}
	if cookie := findCookie(t, rec, sessionCookieName); cookie == nil || cookie.MaxAge != -1 {
		t.Errorf("session cookie not cleared: %+v", cookie)
	}

	if rec := do(t, h, http.MethodPost, "/auth/logout", ""); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestMe(t *testing.T) {
	f := validFakeAuth()
	h := authedHandler(f)

	if rec := do(t, h, http.MethodGet, "/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie = %d, want 401", rec.Code)
	}
	rec := doWithCookie(t, h, http.MethodGet, "/me", sessionCookie("good-token"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec.Body.Bytes())
	user, ok := body["user"].(map[string]any)
	if !ok || user["github_login"] != "octocat" {
		t.Errorf("body = %s", rec.Body.String())
	}
	if body["install_url"] != f.installURL {
		t.Errorf("install_url = %v", body["install_url"])
	}
}

func TestRepositoryEnablement(t *testing.T) {
	path := "/api/v1/repositories/" + repositoryUUID

	t.Run("member can enable and disable", func(t *testing.T) {
		h := authedHandler(validFakeAuth())
		for _, tc := range []struct {
			action string
			want   bool
		}{{"enable", true}, {"disable", false}} {
			rec := doWithCookie(t, h, http.MethodPost, path+"/"+tc.action,
				sessionCookie("good-token"))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d: %s", tc.action, rec.Code, rec.Body.String())
			}
			if got := decodeBody(t, rec.Body.Bytes())["is_enabled"]; got != tc.want {
				t.Errorf("%s is_enabled = %v, want %v", tc.action, got, tc.want)
			}
		}
	})

	t.Run("non-member is forbidden", func(t *testing.T) {
		f := validFakeAuth()
		f.member = false
		h := authedHandler(f)
		rec := doWithCookie(t, h, http.MethodPost, path+"/enable",
			sessionCookie("good-token"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("settings write follows the same membership gate", func(t *testing.T) {
		member := authedHandler(validFakeAuth())
		rec := doWithCookieAndBody(t, member, http.MethodPut, path+"/settings",
			`{"max_attempts": 3}`, sessionCookie("good-token"))
		if rec.Code != http.StatusOK {
			t.Fatalf("member status = %d: %s", rec.Code, rec.Body.String())
		}
		if got := decodeBody(t, rec.Body.Bytes())["max_attempts"]; got != float64(3) {
			t.Errorf("max_attempts = %v, want 3", got)
		}
		f := validFakeAuth()
		f.member = false
		rec = doWithCookieAndBody(t, authedHandler(f), http.MethodPut, path+"/settings",
			`{"max_attempts": 3}`, sessionCookie("good-token"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("non-member status = %d, want 403", rec.Code)
		}
		if rec := do(t, member, http.MethodPut, path+"/settings", `{"max_attempts": 3}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated status = %d, want 401", rec.Code)
		}
	})

	t.Run("unauthenticated is rejected when auth is on", func(t *testing.T) {
		h := authedHandler(validFakeAuth())
		if rec := do(t, h, http.MethodPost, path+"/enable", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("open when auth is not configured", func(t *testing.T) {
		h := New(testLogger(), nil, nil, nil, nil, nil, nil,
			WithDashboard(fakeDashboard{})).Handler()
		rec := do(t, h, http.MethodPost, path+"/disable", "")
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown repository is 404", func(t *testing.T) {
		h := New(testLogger(), nil, nil, nil, nil, nil, nil,
			WithDashboard(fakeDashboard{err: dashboard.ErrRepositoryNotFound})).Handler()
		rec := do(t, h, http.MethodPost, path+"/enable", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("bad uuid is 400", func(t *testing.T) {
		h := authedHandler(validFakeAuth())
		rec := doWithCookie(t, h, http.MethodPost,
			"/api/v1/repositories/not-a-uuid/enable", sessionCookie("good-token"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestSSEStreamRequiresSession(t *testing.T) {
	h := authedHandler(validFakeAuth())
	rec := do(t, h, http.MethodGet, "/api/v1/tasks/"+testUUID+"/stream", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("stream without session = %d, want 401", rec.Code)
	}
}
