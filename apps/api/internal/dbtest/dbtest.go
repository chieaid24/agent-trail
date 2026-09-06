// advisory lock serializes parallel test binaries against the one shared database
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

const lockKey = 0x61747261 // "atra"

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

	// lock must live on one session, so pin a connection for its lifetime
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
	// truncate bypasses the append-only row trigger by design
	if _, err := db.ExecContext(ctx, `
		TRUNCATE task_spans, tasks, task_attempts, activity_events, organizations,
			github_installations, repositories,
			github_webhook_deliveries, runners,
			validation_results, evidence_reports, task_conflicts,
			users, sessions, memberships`); err != nil {
		t.Fatal(err)
	}
	return db
}
