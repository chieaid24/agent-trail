package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Store persists the GitHub integration state: organizations,
// installations, repositories, and the webhook delivery ledger
// (migration 00003_github_integration.sql).
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ErrRepositoryNotFound is returned when no synced repository matches.
var ErrRepositoryNotFound = errors.New("repository not found")

// StoredRepository is one repositories row.
type StoredRepository struct {
	ID                 string
	OrganizationID     string
	GitHubRepositoryID int64
	Owner              string
	Name               string
	FullName           string
	DefaultBranch      string
	CloneURL           string
	IsEnabled          bool
}

// RepositoryContext is everything publishing needs about a task's
// repository: the stored row plus its installation for API credentials.
type RepositoryContext struct {
	StoredRepository
	InstallationID int64
}

// InstallationParams describe an installation upsert; the organization is
// upserted with it from the installation account.
type InstallationParams struct {
	GitHubInstallationID int64
	AccountID            int64
	AccountLogin         string
	AccountType          string // User or Organization
	Permissions          map[string]string
	Events               []string
}

// RecordDelivery inserts the delivery under the github_delivery_id unique
// constraint. inserted=false means a replay: the id is already recorded.
func (s *Store) RecordDelivery(ctx context.Context, deliveryID, eventType, action string, installationID, repositoryID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO github_webhook_deliveries
			(github_delivery_id, event_type, action, installation_id,
			 repository_id)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, 0), NULLIF($5, 0))
		ON CONFLICT (github_delivery_id) DO NOTHING`,
		deliveryID, eventType, action, installationID, repositoryID)
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	return n == 1, nil
}

// MarkDelivery finishes a delivery: processed, ignored, or failed.
func (s *Store) MarkDelivery(ctx context.Context, deliveryID, status, failureMessage string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE github_webhook_deliveries
		SET processing_status = $2, processed_at = now(),
			failure_message = NULLIF($3, '')
		WHERE github_delivery_id = $1`,
		deliveryID, status, truncate(failureMessage, 10000))
	if err != nil {
		return fmt.Errorf("mark delivery: %w", err)
	}
	return nil
}

// UpsertInstallation stores the installation and its owning organization.
// Nil Permissions/Events (the self-heal path has neither) keep any values a
// previous sync stored.
func (s *Store) UpsertInstallation(ctx context.Context, p InstallationParams) error {
	var permissions, events []byte
	var err error
	if p.Permissions != nil {
		if permissions, err = json.Marshal(p.Permissions); err != nil {
			return fmt.Errorf("marshal permissions: %w", err)
		}
	}
	if p.Events != nil {
		if events, err = json.Marshal(p.Events); err != nil {
			return fmt.Errorf("marshal events: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	orgID, err := upsertOrganization(ctx, tx, p.AccountID, p.AccountLogin, p.AccountType)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO github_installations
			(organization_id, github_installation_id, account_login,
			 account_type, permissions_json, events_json)
		VALUES ($1, $2, $3, $4,
			COALESCE($5::jsonb, '{}'::jsonb), COALESCE($6::jsonb, '[]'::jsonb))
		ON CONFLICT (github_installation_id) DO UPDATE SET
			organization_id = EXCLUDED.organization_id,
			account_login = EXCLUDED.account_login,
			account_type = EXCLUDED.account_type,
			permissions_json = COALESCE($5::jsonb,
				github_installations.permissions_json),
			events_json = COALESCE($6::jsonb,
				github_installations.events_json),
			suspended_at = NULL,
			updated_at = now()`,
		orgID, p.GitHubInstallationID, p.AccountLogin, p.AccountType,
		permissions, events)
	if err != nil {
		return fmt.Errorf("upsert installation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// upsertOrganization stores the GitHub account backing an installation and
// returns its id. The slug is the lowercased login; on collision with a
// different account the github_account_id unique constraint surfaces it.
func upsertOrganization(ctx context.Context, tx *sql.Tx, accountID int64, login, accountType string) (string, error) {
	var orgID string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO organizations
			(name, slug, github_account_id, github_account_login,
			 github_account_type)
		VALUES ($1, lower($1), $2, $1, $3)
		ON CONFLICT (github_account_id) DO UPDATE SET
			name = EXCLUDED.name,
			slug = EXCLUDED.slug,
			github_account_login = EXCLUDED.github_account_login,
			github_account_type = EXCLUDED.github_account_type,
			updated_at = now()
		RETURNING id`,
		login, accountID, accountType).Scan(&orgID)
	if err != nil {
		return "", fmt.Errorf("upsert organization: %w", err)
	}
	return orgID, nil
}

// SetInstallationSuspended marks the installation suspended or clears it.
func (s *Store) SetInstallationSuspended(ctx context.Context, githubInstallationID int64, suspended bool) error {
	var at *time.Time
	if suspended {
		now := time.Now().UTC()
		at = &now
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE github_installations
		SET suspended_at = $2, updated_at = now()
		WHERE github_installation_id = $1`, githubInstallationID, at)
	if err != nil {
		return fmt.Errorf("set installation suspended: %w", err)
	}
	return nil
}

// DeleteInstallation removes the installation and disables the
// organization's repositories. Organization and repository rows stay: tasks
// reference them and history outlives the installation.
func (s *Store) DeleteInstallation(ctx context.Context, githubInstallationID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	err = tx.QueryRowContext(ctx, `
		DELETE FROM github_installations
		WHERE github_installation_id = $1
		RETURNING organization_id`, githubInstallationID).Scan(&orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit() // already gone; deletion is idempotent
	}
	if err != nil {
		return fmt.Errorf("delete installation: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE repositories SET is_enabled = false, updated_at = now()
		WHERE organization_id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("disable repositories: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// SyncRepositories replaces the organization's repository set with repos:
// listed ones are upserted and enabled, previously synced ones missing from
// the list are disabled. Repository rows are never deleted (tasks reference
// them).
func (s *Store) SyncRepositories(ctx context.Context, githubInstallationID int64, repos []Repository) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	err = tx.QueryRowContext(ctx, `
		SELECT organization_id FROM github_installations
		WHERE github_installation_id = $1`, githubInstallationID).Scan(&orgID)
	if err != nil {
		return fmt.Errorf("installation organization: %w", err)
	}

	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO repositories
				(organization_id, github_repository_id, owner, name,
				 full_name, default_branch, clone_url, is_private,
				 is_enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true)
			ON CONFLICT (github_repository_id) DO UPDATE SET
				organization_id = EXCLUDED.organization_id,
				owner = EXCLUDED.owner,
				name = EXCLUDED.name,
				full_name = EXCLUDED.full_name,
				default_branch = EXCLUDED.default_branch,
				clone_url = EXCLUDED.clone_url,
				is_private = EXCLUDED.is_private,
				is_enabled = true,
				updated_at = now()`,
			orgID, r.ID, r.Owner.Login, r.Name, r.FullName,
			defaultIfEmpty(r.DefaultBranch, "main"), r.CloneURL, r.Private)
		if err != nil {
			return fmt.Errorf("upsert repository %d: %w", r.ID, err)
		}
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE repositories SET is_enabled = false, updated_at = now()
		WHERE organization_id = $1
			AND NOT (github_repository_id = ANY($2))
			AND is_enabled`, orgID, ids)
	if err != nil {
		return fmt.Errorf("disable removed repositories: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// RepositoryByGitHubID returns the synced repository with that GitHub id.
func (s *Store) RepositoryByGitHubID(ctx context.Context, githubRepositoryID int64) (StoredRepository, error) {
	var r StoredRepository
	err := s.db.QueryRowContext(ctx, `
		SELECT id, organization_id, github_repository_id, owner, name,
			full_name, default_branch, clone_url, is_enabled
		FROM repositories WHERE github_repository_id = $1`,
		githubRepositoryID).Scan(&r.ID, &r.OrganizationID,
		&r.GitHubRepositoryID, &r.Owner, &r.Name, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &r.IsEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredRepository{}, ErrRepositoryNotFound
	}
	if err != nil {
		return StoredRepository{}, fmt.Errorf("repository by github id: %w", err)
	}
	return r, nil
}

// ErrNoInstallation marks a repository whose organization has no live GitHub
// App installation, so no credential can be minted for it.
var ErrNoInstallation = errors.New("repository has no installation")

// RepositoryContextByID returns the publishing context for the repository
// with that internal id (the tasks.repository_id value). A suspended or
// missing installation returns ErrNoInstallation: publishing must not mint
// tokens for it.
func (s *Store) RepositoryContextByID(ctx context.Context, repositoryID string) (RepositoryContext, error) {
	var r RepositoryContext
	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.organization_id, r.github_repository_id, r.owner,
			r.name, r.full_name, r.default_branch, r.clone_url, r.is_enabled,
			i.github_installation_id
		FROM repositories r
		LEFT JOIN github_installations i
			ON i.organization_id = r.organization_id
			AND i.suspended_at IS NULL
		WHERE r.id = $1`, repositoryID).Scan(&r.ID, &r.OrganizationID,
		&r.GitHubRepositoryID, &r.Owner, &r.Name, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &r.IsEnabled, &nullableInt64{&r.InstallationID})
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryContext{}, ErrRepositoryNotFound
	}
	if err != nil {
		return RepositoryContext{}, fmt.Errorf("repository context: %w", err)
	}
	if r.InstallationID == 0 {
		return RepositoryContext{}, ErrNoInstallation
	}
	return r, nil
}

// nullableInt64 scans a NULLable bigint into an int64, mapping NULL to 0.
type nullableInt64 struct{ v *int64 }

func (n *nullableInt64) Scan(src any) error {
	if src == nil {
		*n.v = 0
		return nil
	}
	i, ok := src.(int64)
	if !ok {
		return fmt.Errorf("nullable int64: unexpected type %T", src)
	}
	*n.v = i
	return nil
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
