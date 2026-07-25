// Package dbtest is the shared harness for integration tests that need a
// real database. It skips without TEST_DATABASE_URL, serializes access
// across concurrently running test binaries with a session advisory lock
// (go test runs packages in parallel against the one shared database),
// applies migrations, and starts each test from empty task tables.
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/chieaid24/agent-trail/apps/api/migrations"
)

// lockKey is the advisory lock shared by every integration-test package.
const lockKey = 0x61747261 // "atra"

// Open returns a migrated, truncated test database, holding the advisory
// lock until test cleanup. Skips when TEST_DATABASE_URL is not set.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}

	// The lock must live on one session, so pin a connection for its whole
	// lifetime; pool queries elsewhere are unaffected.
	ctx := context.Background()
	lockConn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := lockConn.ExecContext(ctx,
		`SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		lockConn.Close()
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockKey)
		lockConn.Close()
		db.Close()
	})

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// TRUNCATE bypasses the append-only row trigger by design.
	if _, err := db.ExecContext(ctx,
		`TRUNCATE tasks, task_attempts, activity_events, runners`); err != nil {
		t.Fatal(err)
	}
	return db
}
