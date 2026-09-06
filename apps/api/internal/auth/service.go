package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// no sliding refresh
const SessionTTL = 30 * 24 * time.Hour

type Service struct {
	oauth      *OAuthClient
	store      *Store
	logger     *slog.Logger
	installURL string
}

func NewService(oauth *OAuthClient, store *Store, logger *slog.Logger, installURL string) *Service {
	return &Service{oauth: oauth, store: store, logger: logger, installURL: installURL}
}

func (s *Service) AuthorizeURL(state, redirectURI string) string {
	return s.oauth.AuthorizeURL(state, redirectURI)
}

func (s *Service) SessionTTL() time.Duration { return SessionTTL }

func (s *Service) InstallURL() string { return s.installURL }

// failed membership sync keeps old memberships and login succeeds; next login resyncs
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

func (s *Service) SessionUser(ctx context.Context, token string) (User, error) {
	return s.store.SessionUser(ctx, token)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, token)
}

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
