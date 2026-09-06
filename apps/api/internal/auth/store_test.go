package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
)

func seedOrganization(t *testing.T, db *sql.DB, accountID int64, login string) string {
	t.Helper()
	var id string
	err := db.QueryRow(`
		INSERT INTO organizations
			(name, slug, github_account_id, github_account_login,
			 github_account_type)
		VALUES ($1, $1, $2, $1, 'Organization')
		RETURNING id`, login, accountID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedRepository(t *testing.T, db *sql.DB, organizationID string, githubID int64, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(`
		INSERT INTO repositories
			(organization_id, github_repository_id, owner, name, full_name,
			 clone_url)
		VALUES ($1, $2, 'acme', $3, 'acme/' || $3,
			'https://github.example/acme/' || $3 || '.git')
		RETURNING id`, organizationID, githubID, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUpsertUserInsertsThenUpdates(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	ctx := context.Background()

	first, err := store.UpsertUser(ctx, GitHubUser{
		ID: 42, Login: "octocat", Name: "Octo Cat",
		AvatarURL: "https://avatars.example/42",
	})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if first.GitHubLogin != "octocat" || first.DisplayName != "Octo Cat" {
		t.Errorf("first = %+v", first)
	}

	second, err := store.UpsertUser(ctx, GitHubUser{ID: 42, Login: "renamed"})
	if err != nil {
		t.Fatalf("UpsertUser again: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second login created a new row: %q != %q", second.ID, first.ID)
	}
	if second.GitHubLogin != "renamed" || second.DisplayName != "" || second.AvatarURL != "" {
		t.Errorf("second = %+v, want renamed login and cleared profile", second)
	}
	if !second.LastLoginAt.After(first.LastLoginAt) {
		t.Errorf("last_login_at not advanced: %v -> %v", first.LastLoginAt, second.LastLoginAt)
	}
}

func TestSessionLifecycle(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	ctx := context.Background()

	user, err := store.UpsertUser(ctx, GitHubUser{ID: 7, Login: "octocat"})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	token, err := store.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.SessionUser(ctx, token)
	if err != nil {
		t.Fatalf("SessionUser: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("SessionUser = %q, want %q", got.ID, user.ID)
	}

	if _, err := store.SessionUser(ctx, "not-a-real-token"); err != ErrNoSession {
		t.Errorf("unknown token error = %v, want ErrNoSession", err)
	}

	if err := store.DeleteSession(ctx, token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := store.SessionUser(ctx, token); err != ErrNoSession {
		t.Errorf("deleted token error = %v, want ErrNoSession", err)
	}
	if err := store.DeleteSession(ctx, token); err != nil {
		t.Errorf("second DeleteSession: %v", err)
	}
}

func TestExpiredSessionsRejectAndReap(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	ctx := context.Background()

	user, err := store.UpsertUser(ctx, GitHubUser{ID: 7, Login: "octocat"})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	expired, err := store.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE sessions SET expires_at = created_at + interval '1 ms'`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.SessionUser(ctx, expired); err != ErrNoSession {
		t.Errorf("expired token error = %v, want ErrNoSession", err)
	}

	if _, err := store.CreateSession(ctx, user.ID, time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("sessions after reap = %d, want 1", count)
	}
}

func TestSyncMembershipsReplacesAndSkipsUnknownAccounts(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	ctx := context.Background()

	orgA := seedOrganization(t, db, 100, "acme")
	orgB := seedOrganization(t, db, 200, "globex")
	user, err := store.UpsertUser(ctx, GitHubUser{ID: 7, Login: "octocat"})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	err = store.SyncMemberships(ctx, user.ID, []InstallationAccount{
		{ID: 100, Login: "acme", Type: "Organization"},
		{ID: 999, Login: "unknown", Type: "Organization"},
	})
	if err != nil {
		t.Fatalf("SyncMemberships: %v", err)
	}
	assertMemberships(t, db, user.ID, []string{orgA})

	err = store.SyncMemberships(ctx, user.ID, []InstallationAccount{
		{ID: 200, Login: "globex", Type: "Organization"},
	})
	if err != nil {
		t.Fatalf("SyncMemberships again: %v", err)
	}
	assertMemberships(t, db, user.ID, []string{orgB})

	if err := store.SyncMemberships(ctx, user.ID, nil); err != nil {
		t.Fatalf("SyncMemberships to none: %v", err)
	}
	assertMemberships(t, db, user.ID, nil)
}

func TestMemberOfRepository(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	ctx := context.Background()

	orgA := seedOrganization(t, db, 100, "acme")
	orgB := seedOrganization(t, db, 200, "globex")
	repoA := seedRepository(t, db, orgA, 1001, "widget")
	repoB := seedRepository(t, db, orgB, 1002, "gadget")

	user, err := store.UpsertUser(ctx, GitHubUser{ID: 7, Login: "octocat"})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	err = store.SyncMemberships(ctx, user.ID, []InstallationAccount{
		{ID: 100, Login: "acme", Type: "Organization"},
	})
	if err != nil {
		t.Fatalf("SyncMemberships: %v", err)
	}

	member, err := store.MemberOfRepository(ctx, user.ID, repoA)
	if err != nil || !member {
		t.Errorf("MemberOfRepository(own org) = %v, %v; want true", member, err)
	}
	member, err = store.MemberOfRepository(ctx, user.ID, repoB)
	if err != nil || member {
		t.Errorf("MemberOfRepository(other org) = %v, %v; want false", member, err)
	}
}

func assertMemberships(t *testing.T, db *sql.DB, userID string, want []string) {
	t.Helper()
	rows, err := db.Query(
		`SELECT organization_id FROM memberships WHERE user_id = $1
		 ORDER BY organization_id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if len(got) != len(want) {
		t.Fatalf("memberships = %v, want %v", got, want)
	}
	wantSet := map[string]bool{}
	for _, id := range want {
		wantSet[id] = true
	}
	for _, id := range got {
		if !wantSet[id] {
			t.Fatalf("memberships = %v, want %v", got, want)
		}
	}
}
