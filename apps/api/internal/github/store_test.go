package github

import (
	"context"
	"errors"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
)

// syncOneRepo stores an installation with one synced repository and returns
// the stored row.
func syncOneRepo(t *testing.T, s *Store) StoredRepository {
	t.Helper()
	ctx := context.Background()
	err := s.UpsertInstallation(ctx, InstallationParams{
		GitHubInstallationID: 999, AccountID: 61,
		AccountLogin: "acme", AccountType: "Organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncRepositories(ctx, 999, []Repository{testRepo(501, "acme/service")}); err != nil {
		t.Fatal(err)
	}
	repo, err := s.RepositoryByGitHubID(ctx, 501)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRepositoryContextByIDJoinsInstallation(t *testing.T) {
	s := NewStore(dbtest.Open(t))
	repo := syncOneRepo(t, s)

	rc, err := s.RepositoryContextByID(context.Background(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.InstallationID != 999 {
		t.Fatalf("installation id = %d, want 999", rc.InstallationID)
	}
	if rc.Owner != "acme" || rc.Name != "service" {
		t.Fatalf("repo = %s/%s", rc.Owner, rc.Name)
	}
	if rc.CloneURL == "" {
		t.Fatal("clone url not stored")
	}
}

func TestRepositoryContextByIDRejectsSuspendedInstallation(t *testing.T) {
	s := NewStore(dbtest.Open(t))
	repo := syncOneRepo(t, s)

	if err := s.SetInstallationSuspended(context.Background(), 999, true); err != nil {
		t.Fatal(err)
	}
	_, err := s.RepositoryContextByID(context.Background(), repo.ID)
	if !errors.Is(err, ErrNoInstallation) {
		t.Fatalf("err = %v, want ErrNoInstallation", err)
	}
}

func TestRepositoryContextByIDUnknownRepository(t *testing.T) {
	s := NewStore(dbtest.Open(t))
	_, err := s.RepositoryContextByID(context.Background(),
		"00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("err = %v, want ErrRepositoryNotFound", err)
	}
}
