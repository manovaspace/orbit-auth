package migrations

import (
	"context"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// TestRestartAfterSchemaMoveKeepsVersionTableInPublic simulates a restart of the
// service once migration 024 has created the identity schema. A new migrator
// must read the applied history from public and must not create a second
// goose_db_version in identity: that table would hold only version 0, and the
// next start would re-run 001 on tables that already exist.
func TestRestartAfterSchemaMoveKeepsVersionTableInPublic(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	first, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Up(ctx); err != nil {
		_ = first.Close()
		t.Fatalf("up: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()

	current, target, err := restarted.Version(ctx)
	if err != nil {
		t.Fatalf("version after restart: %v", err)
	}
	if target == 0 || current != target {
		t.Fatalf("after restart current = %d, target = %d; want every migration applied", current, target)
	}
	if tableExists(t, ctx, db.Pool, "identity", "goose_db_version") {
		t.Fatal("restart created identity.goose_db_version; the version table must stay in public")
	}
	if _, err := restarted.Up(ctx); err != nil {
		t.Fatalf("up after restart: %v", err)
	}
}
