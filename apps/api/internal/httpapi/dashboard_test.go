package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dashboard"
)

const (
	organizationUUID = "6c4f8ea8-2131-436d-8fd8-8acaf079c2a2"
	repositoryUUID   = "39be2f56-3419-4a0a-a7ad-1a72698c0cc5"
	runnerUUID       = "88f57a9f-0cd6-4fd8-8c14-6d4710a3370f"
)

type fakeDashboard struct {
	err error
}

func (f fakeDashboard) ListOrganizations(context.Context) ([]dashboard.Organization, error) {
	return []dashboard.Organization{{ID: organizationUUID, Name: "Agent Trail"}}, f.err
}

func (f fakeDashboard) GetOrganization(_ context.Context, id string) (dashboard.Organization, error) {
	if f.err != nil {
		return dashboard.Organization{}, f.err
	}
	return dashboard.Organization{ID: id, Name: "Agent Trail"}, nil
}

func (f fakeDashboard) ListRepositories(_ context.Context, organizationID string, _ int) ([]dashboard.Repository, error) {
	return []dashboard.Repository{{
		ID: repositoryUUID, OrganizationID: organizationID,
		FullName: "agent-trail/control-plane",
	}}, f.err
}

func (f fakeDashboard) GetRepository(_ context.Context, id string) (dashboard.RepositoryDetail, error) {
	if f.err != nil {
		return dashboard.RepositoryDetail{}, f.err
	}
	return dashboard.RepositoryDetail{
		Repository: dashboard.Repository{ID: id, FullName: "agent-trail/control-plane"},
	}, nil
}

func (f fakeDashboard) GetRepositorySettings(context.Context, string) (dashboard.RepositorySettings, error) {
	return dashboard.RepositorySettings{DefaultPolicy: "platform default", MaxAttempts: 5}, f.err
}

func (f fakeDashboard) UpdateRepositorySettings(_ context.Context, _ string, patch dashboard.RepositorySettingsPatch) (dashboard.RepositorySettings, error) {
	if f.err != nil {
		return dashboard.RepositorySettings{}, f.err
	}
	settings := dashboard.RepositorySettings{DefaultPolicy: "platform default", MaxAttempts: 5}
	if patch.MaxAttempts != nil {
		settings.MaxAttempts = *patch.MaxAttempts
	}
	return settings, nil
}

func (f fakeDashboard) SetRepositoryEnabled(_ context.Context, id string, enabled bool) (dashboard.Repository, error) {
	if f.err != nil {
		return dashboard.Repository{}, f.err
	}
	return dashboard.Repository{
		ID: id, FullName: "agent-trail/control-plane", IsEnabled: enabled,
	}, nil
}

func (f fakeDashboard) ListRunners(context.Context) ([]dashboard.Runner, error) {
	return []dashboard.Runner{{ID: runnerUUID, HostnameOrPod: "runner-1"}}, f.err
}

func (f fakeDashboard) GetRunner(_ context.Context, id string) (dashboard.RunnerDetail, error) {
	if f.err != nil {
		return dashboard.RunnerDetail{}, f.err
	}
	return dashboard.RunnerDetail{
		Runner: dashboard.Runner{ID: id, HostnameOrPod: "runner-1"},
	}, nil
}

func dashboardHandler(service DashboardService) http.Handler {
	return New(testLogger(), nil, nil, nil, nil, nil, nil,
		WithDashboard(service)).Handler()
}

func decodeBody(t *testing.T, recBody []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recBody, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDashboardListRoutes(t *testing.T) {
	h := dashboardHandler(fakeDashboard{})
	tests := []struct {
		path string
		key  string
	}{
		{"/api/v1/organizations", "organizations"},
		{"/api/v1/organizations/" + organizationUUID + "/repositories", "repositories"},
		{"/api/v1/repositories?limit=10", "repositories"},
		{"/api/v1/runners", "runners"},
	}
	for _, tc := range tests {
		t.Run(tc.key+" "+tc.path, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if _, ok := decodeBody(t, rec.Body.Bytes())[tc.key]; !ok {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestDashboardDetailRoutes(t *testing.T) {
	h := dashboardHandler(fakeDashboard{})
	tests := []struct {
		path string
		key  string
		want string
	}{
		{"/api/v1/organizations/" + organizationUUID, "name", "Agent Trail"},
		{"/api/v1/repositories/" + repositoryUUID, "full_name", "agent-trail/control-plane"},
		{"/api/v1/repositories/" + repositoryUUID + "/settings", "default_policy", "platform default"},
		{"/api/v1/runners/" + runnerUUID, "hostname_or_pod", "runner-1"},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := decodeBody(t, rec.Body.Bytes())[tc.key]; got != tc.want {
				t.Fatalf("%s = %#v, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestDashboardRouteErrors(t *testing.T) {
	unavailable := New(testLogger(), nil, nil, nil, nil, nil, nil).Handler()
	rec := do(t, unavailable, http.MethodGet, "/api/v1/runners", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d", rec.Code)
	}

	h := dashboardHandler(fakeDashboard{})
	rec = do(t, h, http.MethodGet, "/api/v1/runners/not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/repositories?limit=201", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", rec.Code)
	}

	missing := dashboardHandler(fakeDashboard{err: dashboard.ErrRunnerNotFound})
	rec = do(t, missing, http.MethodGet, "/api/v1/runners/"+runnerUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", rec.Code)
	}

	broken := dashboardHandler(fakeDashboard{err: errors.New("database failed")})
	rec = do(t, broken, http.MethodGet, "/api/v1/organizations", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken status = %d", rec.Code)
	}
}

func TestUpdateRepositorySettings(t *testing.T) {
	path := "/api/v1/repositories/" + repositoryUUID + "/settings"
	h := dashboardHandler(fakeDashboard{})

	rec := do(t, h, http.MethodPut, path, `{"max_attempts": 7}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := decodeBody(t, rec.Body.Bytes())["max_attempts"]; got != float64(7) {
		t.Fatalf("max_attempts = %#v, want 7", got)
	}

	for name, body := range map[string]string{
		"missing field": `{}`,
		"below bound":   `{"max_attempts": 0}`,
		"above bound":   `{"max_attempts": 21}`,
		"wrong type":    `{"max_attempts": "5"}`,
		"unknown field": `{"max_attempts": 5, "default_policy": "x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, http.MethodPut, path, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}

	missing := dashboardHandler(fakeDashboard{err: dashboard.ErrRepositoryNotFound})
	if rec := do(t, missing, http.MethodPut, path, `{"max_attempts": 7}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", rec.Code)
	}
	invalid := dashboardHandler(fakeDashboard{err: dashboard.ErrInvalidSettings})
	if rec := do(t, invalid, http.MethodPut, path, `{"max_attempts": 7}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/repositories/not-a-uuid/settings", `{"max_attempts": 7}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad uuid status = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, path, strings.Repeat("1", 10)); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-object body status = %d", rec.Code)
	}
}
