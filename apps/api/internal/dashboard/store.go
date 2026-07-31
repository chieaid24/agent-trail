package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

const defaultPolicy = "platform default"
const defaultValidationFile = ".agent-trail/validation.yaml"

// Store builds dashboard read models from PostgreSQL.
type Store struct {
	db *sql.DB
}

// NewStore returns a dashboard Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const organizationColumns = `o.id, o.name, o.slug, o.github_account_login,
	o.github_account_type, count(r.id),
	count(r.id) FILTER (WHERE r.is_enabled), o.created_at, o.updated_at`

// ListOrganizations returns organizations ordered by name.
func (s *Store) ListOrganizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+organizationColumns+`
		FROM organizations o
		LEFT JOIN repositories r ON r.organization_id = o.id
		GROUP BY o.id
		ORDER BY lower(o.name), o.id`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	organizations := []Organization{}
	for rows.Next() {
		o, err := scanOrganization(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		organizations = append(organizations, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	return organizations, nil
}

// GetOrganization returns one organization by internal id.
func (s *Store) GetOrganization(ctx context.Context, id string) (Organization, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+organizationColumns+`
		FROM organizations o
		LEFT JOIN repositories r ON r.organization_id = o.id
		WHERE o.id = $1
		GROUP BY o.id`, id)
	o, err := scanOrganization(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, ErrOrganizationNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("get organization: %w", err)
	}
	return o, nil
}

func scanOrganization(row interface{ Scan(...any) error }) (Organization, error) {
	var o Organization
	err := row.Scan(&o.ID, &o.Name, &o.Slug, &o.GitHubAccountLogin,
		&o.GitHubAccountType, &o.RepositoryCount, &o.EnabledRepositoryCount,
		&o.CreatedAt, &o.UpdatedAt)
	return o, err
}

const repositoryColumns = `r.id, r.organization_id, r.github_repository_id,
	r.owner, r.name, r.full_name, r.default_branch, r.is_private,
	r.is_enabled, r.settings_json,
	count(t.id) FILTER (WHERE t.phase <> 'terminal'),
	count(t.id) FILTER (WHERE t.updated_at >= now() - interval '30 days'),
	r.created_at, r.updated_at`

// ListRepositories returns repositories, optionally scoped to an organization.
func (s *Store) ListRepositories(ctx context.Context, organizationID string, limit int) ([]Repository, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	query := `
		SELECT ` + repositoryColumns + `
		FROM repositories r
		LEFT JOIN tasks t ON t.repository_id = r.id`
	args := []any{}
	if organizationID != "" {
		query += ` WHERE r.organization_id = $1`
		args = append(args, organizationID)
	}
	query += fmt.Sprintf(`
		GROUP BY r.id
		ORDER BY r.updated_at DESC, lower(r.full_name), r.id
		LIMIT %d`, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	defer rows.Close()

	repositories := []Repository{}
	for rows.Next() {
		r, err := scanRepository(rows)
		if err != nil {
			return nil, fmt.Errorf("scan repository: %w", err)
		}
		repositories = append(repositories, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	return repositories, nil
}

// GetRepository returns the repository page read model.
func (s *Store) GetRepository(ctx context.Context, id string) (RepositoryDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+repositoryColumns+`
		FROM repositories r
		LEFT JOIN tasks t ON t.repository_id = r.id
		WHERE r.id = $1
		GROUP BY r.id`, id)
	repository, err := scanRepository(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryDetail{}, ErrRepositoryNotFound
	}
	if err != nil {
		return RepositoryDetail{}, fmt.Errorf("get repository: %w", err)
	}

	metrics, err := s.repositoryMetrics(ctx, id)
	if err != nil {
		return RepositoryDetail{}, err
	}
	active, err := s.repositoryTasks(ctx, id, true)
	if err != nil {
		return RepositoryDetail{}, err
	}
	recent, err := s.repositoryTasks(ctx, id, false)
	if err != nil {
		return RepositoryDetail{}, err
	}
	return RepositoryDetail{
		Repository: repository,
		Metrics:    metrics, ActiveTasks: active, RecentTasks: recent,
	}, nil
}

// GetRepositorySettings returns the interpreted repository settings.
func (s *Store) GetRepositorySettings(ctx context.Context, id string) (RepositorySettings, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT settings_json FROM repositories WHERE id = $1`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositorySettings{}, ErrRepositoryNotFound
	}
	if err != nil {
		return RepositorySettings{}, fmt.Errorf("get repository settings: %w", err)
	}
	settings, err := parseRepositorySettings(raw)
	if err != nil {
		return RepositorySettings{}, fmt.Errorf("parse repository settings: %w", err)
	}
	return settings, nil
}

func scanRepository(row interface{ Scan(...any) error }) (Repository, error) {
	var r Repository
	var raw []byte
	err := row.Scan(&r.ID, &r.OrganizationID, &r.GitHubRepositoryID,
		&r.Owner, &r.Name, &r.FullName, &r.DefaultBranch, &r.IsPrivate,
		&r.IsEnabled, &raw, &r.ActiveTaskCount, &r.RecentTaskCount,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return Repository{}, err
	}
	r.Settings, err = parseRepositorySettings(raw)
	return r, err
}

func parseRepositorySettings(raw []byte) (RepositorySettings, error) {
	settings := RepositorySettings{
		DefaultPolicy: defaultPolicy, ValidationFile: defaultValidationFile,
	}
	var stored struct {
		DefaultPolicy  string `json:"default_policy"`
		ValidationFile string `json:"validation_file"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return RepositorySettings{}, err
	}
	if stored.DefaultPolicy != "" {
		settings.DefaultPolicy = stored.DefaultPolicy
	}
	if stored.ValidationFile != "" {
		settings.ValidationFile = stored.ValidationFile
	}
	return settings, nil
}

func (s *Store) repositoryMetrics(ctx context.Context, id string) (RepositoryMetrics, error) {
	var m RepositoryMetrics
	var completionRate, medianRuntime sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*),
			count(*) FILTER (WHERE phase <> 'terminal'),
			count(*) FILTER (WHERE status = 'completed'),
			count(*) FILTER (WHERE status IN ('failed', 'timed_out')),
			count(*) FILTER (WHERE status = 'completed')::float
				/ NULLIF(count(*) FILTER (WHERE phase = 'terminal'), 0),
			percentile_cont(0.5) WITHIN GROUP (
				ORDER BY extract(epoch FROM (completed_at - started_at)) * 1000
			) FILTER (WHERE started_at IS NOT NULL AND completed_at IS NOT NULL)
		FROM tasks WHERE repository_id = $1`, id).Scan(
		&m.TotalTasks, &m.ActiveTasks, &m.CompletedTasks, &m.FailedTasks,
		&completionRate, &medianRuntime)
	if err != nil {
		return RepositoryMetrics{}, fmt.Errorf("repository metrics: %w", err)
	}
	if completionRate.Valid {
		m.CompletionRate = &completionRate.Float64
	}
	if medianRuntime.Valid {
		ms := int64(medianRuntime.Float64)
		m.MedianRuntimeMillis = &ms
	}
	return m, nil
}

func (s *Store) repositoryTasks(ctx context.Context, id string, active bool) ([]TaskSummary, error) {
	query := `
		SELECT id, title, status, phase, source_issue_number, started_at,
			completed_at, failure_message, created_at, updated_at
		FROM tasks WHERE repository_id = $1`
	if active {
		query += ` AND phase <> 'terminal'`
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT 10`
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("repository tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

// ListRunners returns every runner, freshest heartbeat first.
func (s *Store) ListRunners(ctx context.Context) ([]Runner, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.runner_type, r.hostname_or_pod, r.status, r.capacity,
			count(a.id) FILTER (
				WHERE a.lease_owner = r.id AND a.status = 'active'
			), r.labels_json, r.last_heartbeat_at, r.created_at, r.updated_at
		FROM runners r
		LEFT JOIN task_attempts a ON a.lease_owner = r.id
		GROUP BY r.id
		ORDER BY r.last_heartbeat_at DESC, r.id`)
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	defer rows.Close()

	runners := []Runner{}
	for rows.Next() {
		r, err := scanRunner(rows)
		if err != nil {
			return nil, fmt.Errorf("scan runner: %w", err)
		}
		runners = append(runners, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	return runners, nil
}

// GetRunner returns the runner page read model.
func (s *Store) GetRunner(ctx context.Context, id string) (RunnerDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.runner_type, r.hostname_or_pod, r.status, r.capacity,
			count(a.id) FILTER (
				WHERE a.lease_owner = r.id AND a.status = 'active'
			), r.labels_json, r.last_heartbeat_at, r.created_at, r.updated_at
		FROM runners r
		LEFT JOIN task_attempts a ON a.lease_owner = r.id
		WHERE r.id = $1
		GROUP BY r.id`, id)
	runner, err := scanRunner(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RunnerDetail{}, ErrRunnerNotFound
	}
	if err != nil {
		return RunnerDetail{}, fmt.Errorf("get runner: %w", err)
	}

	current, err := s.runnerTasks(ctx, id, true)
	if err != nil {
		return RunnerDetail{}, err
	}
	failures, err := s.runnerTasks(ctx, id, false)
	if err != nil {
		return RunnerDetail{}, err
	}
	return RunnerDetail{
		Runner: runner, CurrentTasks: current, RecentFailures: failures,
	}, nil
}

func scanRunner(row interface{ Scan(...any) error }) (Runner, error) {
	var r Runner
	var raw []byte
	err := row.Scan(&r.ID, &r.Type, &r.HostnameOrPod, &r.Status,
		&r.Capacity, &r.ActiveTaskCount, &raw, &r.LastHeartbeatAt,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return Runner{}, err
	}
	if err := json.Unmarshal(raw, &r.Labels); err != nil {
		return Runner{}, err
	}
	r.Resources = resourceUsage(r.Labels)
	return r, nil
}

func resourceUsage(labels map[string]string) ResourceUsage {
	return ResourceUsage{
		CPUPercent:    percent(labels["cpu_percent"]),
		MemoryPercent: percent(labels["memory_percent"]),
		DiskPercent:   percent(labels["disk_percent"]),
	}
}

func percent(value string) *float64 {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < 0 || n > 100 {
		return nil
	}
	return &n
}

func (s *Store) runnerTasks(ctx context.Context, id string, current bool) ([]TaskSummary, error) {
	query := `
		SELECT DISTINCT t.id, t.title, t.status, t.phase,
			t.source_issue_number, t.started_at, t.completed_at,
			t.failure_message, t.created_at, t.updated_at
		FROM tasks t
		JOIN task_attempts a ON a.task_id = t.id`
	if current {
		query += ` WHERE a.lease_owner = $1 AND a.status = 'active'`
	} else {
		query += ` WHERE a.runner_id = $1 AND t.status IN ('failed', 'timed_out')`
	}
	query += ` ORDER BY t.updated_at DESC, t.id DESC LIMIT 10`
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("runner tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

func scanTasks(rows *sql.Rows) ([]TaskSummary, error) {
	tasks := []TaskSummary{}
	for rows.Next() {
		var t TaskSummary
		err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.Phase,
			&t.SourceIssueNumber, &t.StartedAt, &t.CompletedAt,
			&t.FailureMessage, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan tasks: %w", err)
	}
	return tasks, nil
}
