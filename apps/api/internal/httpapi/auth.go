package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/auth"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// Cookie names. Both are host-scoped: the dashboard and the API share one
// browser origin through the /backend proxy (apps/web/next.config.ts).
const (
	sessionCookieName = "agent_trail_session"
	stateCookieName   = "agent_trail_oauth_state"
	stateCookieMaxAge = 10 * time.Minute
)

// callbackPath is where GitHub redirects the browser after authorization,
// on the dashboard origin (the /backend prefix is the proxy's).
const callbackPath = "/backend/auth/github/callback"

// AuthService is the slice of auth.Service the HTTP layer needs.
type AuthService interface {
	AuthorizeURL(state, redirectURI string) string
	CompleteLogin(ctx context.Context, code, redirectURI string) (auth.User, string, error)
	SessionUser(ctx context.Context, token string) (auth.User, error)
	Logout(ctx context.Context, token string) error
	MemberOfRepository(ctx context.Context, userID, repositoryID string) (bool, error)
	SessionTTL() time.Duration
	InstallURL() string
}

// WithAuth enables GitHub OAuth login and session enforcement across
// /api/v1. publicOrigin is the browser-facing dashboard origin; secure
// marks the cookies Secure.
func WithAuth(service AuthService, publicOrigin string, secure bool) Option {
	return func(s *Server) {
		s.auth = service
		s.authPublicOrigin = publicOrigin
		s.authCookieSecure = secure
	}
}

type userContextKey struct{}

func withUser(ctx context.Context, user auth.User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// userFrom returns the session user requireSession stored on the context.
func userFrom(ctx context.Context) (auth.User, bool) {
	user, ok := ctx.Value(userContextKey{}).(auth.User)
	return user, ok
}

// requireSession rejects requests without a live session cookie. It is
// applied to /api/v1 and /me only when auth is configured; the SSE stream
// sits under it too, which is why the session travels as a cookie
// (EventSource cannot set headers).
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		user, err := s.auth.SessionUser(r.Context(), cookie.Value)
		if errors.Is(err, auth.ErrNoSession) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if err != nil {
			s.logger.LogAttrs(r.Context(), slog.LevelError, "session lookup failed",
				slog.String("event", "auth_session_lookup_failed"),
				slog.String("trace_id", observability.TraceIDFrom(r.Context())),
				slog.String("error", err.Error()),
			)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

func (s *Server) authAvailable(w http.ResponseWriter) bool {
	if s.auth != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "authentication is not configured")
	return false
}

// handleAuthStart begins the OAuth flow: it binds a random state to the
// browser and redirects to GitHub.
func (s *Server) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	if !s.authAvailable(w) {
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.logger.LogAttrs(r.Context(), slog.LevelError, "state generation failed",
			slog.String("event", "auth_state_generation_failed"),
			slog.String("trace_id", observability.TraceIDFrom(r.Context())),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	state := hex.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Value: state, Path: "/",
		MaxAge:   int(stateCookieMaxAge.Seconds()),
		HttpOnly: true, Secure: s.authCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.auth.AuthorizeURL(state, s.authPublicOrigin+callbackPath),
		http.StatusFound)
}

// handleAuthCallback finishes the OAuth flow. Failures redirect to the
// login page with an error code; details stay in the logs.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.authAvailable(w) {
		return
	}
	clearCookie(w, stateCookieName, s.authCookieSecure)

	query := r.URL.Query()
	if query.Get("error") != "" {
		// The user cancelled on GitHub, or GitHub rejected the request.
		// Reported before the state check so a denial after the state
		// cookie expired still reads as what it was.
		s.redirectLoginError(w, r, "github_denied")
		return
	}
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value == "" ||
		subtle.ConstantTimeCompare([]byte(stateCookie.Value),
			[]byte(query.Get("state"))) != 1 {
		s.redirectLoginError(w, r, "state_mismatch")
		return
	}
	code := query.Get("code")
	if code == "" {
		s.redirectLoginError(w, r, "missing_code")
		return
	}

	_, session, err := s.auth.CompleteLogin(r.Context(), code, s.authPublicOrigin+callbackPath)
	if err != nil {
		s.logger.LogAttrs(r.Context(), slog.LevelError, "login failed",
			slog.String("event", "auth_login_failed"),
			slog.String("trace_id", observability.TraceIDFrom(r.Context())),
			slog.String("error", err.Error()),
		)
		s.redirectLoginError(w, r, "login_failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: session, Path: "/",
		MaxAge:   int(s.auth.SessionTTL().Seconds()),
		HttpOnly: true, Secure: s.authCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.authPublicOrigin+"/", http.StatusFound)
}

// handleAuthLogout revokes the session; logging out twice is a 204 both
// times.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if !s.authAvailable(w) {
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if err := s.auth.Logout(r.Context(), cookie.Value); err != nil {
			s.logger.LogAttrs(r.Context(), slog.LevelError, "logout failed",
				slog.String("event", "auth_logout_failed"),
				slog.String("trace_id", observability.TraceIDFrom(r.Context())),
				slog.String("error", err.Error()),
			)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	clearCookie(w, sessionCookieName, s.authCookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	User       auth.User `json:"user"`
	InstallURL string    `json:"install_url,omitempty"`
}

// handleMe returns the session user; requireSession guards the route.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		User: user, InstallURL: s.auth.InstallURL(),
	})
}

func (s *Server) redirectLoginError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r,
		s.authPublicOrigin+"/login?error="+url.QueryEscape(code),
		http.StatusFound)
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}
