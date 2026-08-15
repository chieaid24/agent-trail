package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNoSession is returned when no live session matches the presented
// token; the HTTP layer maps it to 401.
var ErrNoSession = errors.New("no valid session")

// User is one dashboard user row. DisplayName and AvatarURL are empty when
// GitHub reports none.
type User struct {
	ID           string    `json:"id"`
	GitHubUserID int64     `json:"github_user_id"`
	GitHubLogin  string    `json:"github_login"`
	DisplayName  string    `json:"display_name"`
	AvatarURL    string    `json:"avatar_url"`
	CreatedAt    time.Time `json:"created_at"`
	LastLoginAt  time.Time `json:"last_login_at"`
}

// Store persists users, sessions, and memberships
// (migration 00007_dashboard_auth.sql).
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// UpsertUser stores the GitHub user and stamps last_login_at.
func (s *Store) UpsertUser(ctx context.Context, gh GitHubUser) (User, error) {
	var u User
	var displayName, avatarURL sql.NullString
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (github_user_id, github_login, display_name, avatar_url)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
		ON CONFLICT (github_user_id) DO UPDATE SET
			github_login = EXCLUDED.github_login,
			display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url,
			last_login_at = now()
		RETURNING id, github_user_id, github_login, display_name, avatar_url,
			created_at, last_login_at`,
		gh.ID, gh.Login, gh.Name, gh.AvatarURL).Scan(
		&u.ID, &u.GitHubUserID, &u.GitHubLogin, &displayName, &avatarURL,
		&u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	u.DisplayName = displayName.String
	u.AvatarURL = avatarURL.String
	return u, nil
}

// CreateSession mints a session token for the user and returns it. Only
// the token's SHA-256 is stored; expired rows are reaped opportunistically.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= now()`); err != nil {
		return "", fmt.Errorf("reap sessions: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, now() + make_interval(secs => $3))`,
		userID, hashToken(token), ttl.Seconds())
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// SessionUser resolves a live session token to its user.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	var u User
	var displayName, avatarURL sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.github_user_id, u.github_login, u.display_name,
			u.avatar_url, u.created_at, u.last_login_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`,
		hashToken(token)).Scan(
		&u.ID, &u.GitHubUserID, &u.GitHubLogin, &displayName, &avatarURL,
		&u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, fmt.Errorf("session user: %w", err)
	}
	u.DisplayName = displayName.String
	u.AvatarURL = avatarURL.String
	return u, nil
}

// DeleteSession revokes the session; a missing row is already logged out.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE token_hash = $1`, hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SyncMemberships replaces the user's memberships with one per account
// that has a synced organization row. Accounts without one (app installed
// but no webhook received yet) are skipped until the next login.
func (s *Store) SyncMemberships(ctx context.Context, userID string, accounts []InstallationAccount) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sync memberships: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memberships WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("sync memberships: clear: %w", err)
	}
	for _, account := range accounts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memberships (organization_id, user_id)
			SELECT id, $1 FROM organizations WHERE github_account_id = $2
			ON CONFLICT DO NOTHING`,
			userID, account.ID); err != nil {
			return fmt.Errorf("sync memberships: insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sync memberships: commit: %w", err)
	}
	return nil
}

// MemberOfRepository reports whether the user belongs to the repository's
// organization (docs/security/threat-model.md: resource authorization).
func (s *Store) MemberOfRepository(ctx context.Context, userID, repositoryID string) (bool, error) {
	var member bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM memberships m
			JOIN repositories r ON r.organization_id = m.organization_id
			WHERE m.user_id = $1 AND r.id = $2)`,
		userID, repositoryID).Scan(&member)
	if err != nil {
		return false, fmt.Errorf("member of repository: %w", err)
	}
	return member, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
