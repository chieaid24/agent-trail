package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// SessionTTL bounds every session; there is no sliding refresh
// (docs/adr/0013-dashboard-sessions.md).
const SessionTTL = 30 * 24 * time.Hour

// Service wires the OAuth client and store into the login flow the HTTP
// layer drives.
type Service struct {
	oauth      *OAuthClient
	store      *Store
	logger     *slog.Logger
	installURL string
}

// NewService returns a Service. installURL is the GitHub App installation
// link surfaced to signed-in users; empty hides it.
func NewService(oauth *OAuthClient, store *Store, logger *slog.Logger, installURL string) *Service {
	return &Service{oauth: oauth, store: store, logger: logger, installURL: installURL}
}

// AuthorizeURL returns the GitHub authorize URL for the state and
// redirect URI.
func (s *Service) AuthorizeURL(state, redirectURI string) string {
	return s.oauth.AuthorizeURL(state, redirectURI)
}

// SessionTTL returns the session lifetime (cookie Max-Age).
func (s *Service) SessionTTL() time.Duration { return SessionTTL }

// InstallURL returns the GitHub App installation link, or empty.
func (s *Service) InstallURL() string { return s.installURL }

// CompleteLogin exchanges the callback code, upserts the user, resyncs
// memberships, and mints a session token. A failed membership sync keeps
// the previous memberships and the login succeeds: GitHub flaking on one
// endpoint must not lock the dashboard, and the next login resyncs.
func (s *Service) CompleteLogin(ctx context.Context, code, redirectURI string) (User, string, error) {
	token, err := s.oauth.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		return User{}, "", err
	}
	gh, err := s.oauth.User(ctx, token)
	if err != nil {
		return User{}, "", err
	}
	user, err := s.store.UpsertUser(ctx, gh)
	if err != nil {
		return User{}, "", err
	}

	if accounts, err := s.oauth.InstallationAccounts(ctx, token); err != nil {
		s.logMembershipSyncFailure(ctx, user, err)
	} else if err := s.store.SyncMemberships(ctx, user.ID, accounts); err != nil {
		s.logMembershipSyncFailure(ctx, user, err)
	}

	session, err := s.store.CreateSession(ctx, user.ID, SessionTTL)
	if err != nil {
		return User{}, "", err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "user logged in",
		slog.String("event", "auth_login"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("user_id", user.ID),
		slog.String("github_login", user.GitHubLogin),
	)
	return user, session, nil
}

// SessionUser resolves a session token to its user.
func (s *Service) SessionUser(ctx context.Context, token string) (User, error) {
	return s.store.SessionUser(ctx, token)
}

// Logout revokes the session.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, token)
}

// MemberOfRepository reports whether the user belongs to the repository's
// organization.
func (s *Service) MemberOfRepository(ctx context.Context, userID, repositoryID string) (bool, error) {
	return s.store.MemberOfRepository(ctx, userID, repositoryID)
}

func (s *Service) logMembershipSyncFailure(ctx context.Context, user User, err error) {
	s.logger.LogAttrs(ctx, slog.LevelWarn, "membership sync failed",
		slog.String("event", "auth_membership_sync_failed"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("user_id", user.ID),
		slog.String("error", err.Error()),
	)
}
