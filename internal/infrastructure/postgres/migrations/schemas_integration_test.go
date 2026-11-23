package migrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// schemaTable is a table that migration 024 moves, with the schema it moves to.
type schemaTable struct{ schema, name string }

// movedTables are the 14 tables of the schema layout (migration 024).
var movedTables = []schemaTable{
	{"identity", "users"},
	{"identity", "user_identities"},
	{"identity", "otp_challenges"},
	{"identity", "refresh_tokens"},
	{"identity", "api_tokens"},
	{"identity", "oauth_sign_in_states"},
	{"tenancy", "organizations"},
	{"tenancy", "organization_memberships"},
	{"tenancy", "organization_invites"},
	{"tenancy", "organization_slugs"},
	{"tenancy", "organization_provisions"},
	{"platform", "feature_flags"},
	{"platform", "outbox_events"},
	{"platform", "audit_events"},
}

// rollBackSchemaMove rolls back migration 024 and fails unless it reports that
// version. Migration 024 sits on top of every earlier migration, so a test that
// rolls back to an earlier version calls this first.
func rollBackSchemaMove(t *testing.T, ctx context.Context, m *Migrator) {
	t.Helper()
	result, err := m.Down(ctx)
	if err != nil {
		t.Fatalf("down 024: %v", err)
	}
	if result.Source.Version != 24 {
		t.Fatalf("rolled back version %d, want 24", result.Source.Version)
	}
}

// assertLocations fails unless every table that 024 moves is in the schema the
// move gives it (inSchema) or in public (not inSchema), and in no other schema.
// goose_db_version is always in public.
func assertLocations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, inSchema bool) {
	t.Helper()
	for _, table := range movedTables {
		want := "public"
		if inSchema {
			want = table.schema
		}
		for _, schema := range allSchemas {
			exists := tableExists(t, ctx, pool, schema, table.name)
			if schema == want && !exists {
				t.Fatalf("%s.%s is missing", schema, table.name)
			}
			if schema != want && exists {
				t.Fatalf("%s.%s exists, want it only in %s", schema, table.name, want)
			}
		}
	}
	if !tableExists(t, ctx, pool, "public", "goose_db_version") {
		t.Fatal("goose_db_version is not in public")
	}
}

// assertNoOtherPublicTables fails unless public holds goose_db_version and
// nothing else, which is the state after 024 has moved every table out.
func assertNoOtherPublicTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var names string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(string_agg(tablename, ',' ORDER BY tablename), '')
		FROM pg_tables WHERE schemaname = 'public'`).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names != "goose_db_version" {
		t.Fatalf("public tables = %q, want only goose_db_version", names)
	}
}

// countSchemas returns how many of the three schemas 024 creates exist.
func countSchemas(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_namespace
		WHERE nspname IN ('identity', 'tenancy', 'platform')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestSchemaMigrationMovesTablesAndRollsBack checks migration 024 up, down and
// up. After up each of the 14 tables is in its schema and none is left in
// public except goose_db_version. A foreign key across schemas still holds.
// Down puts every table back in public and drops the three schemas. Data
// written before 024 runs survives up, down and up.
func TestSchemaMigrationMovesTablesAndRollsBack(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	m, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	assertLocations(t, ctx, db.Pool, true)
	assertNoOtherPublicTables(t, ctx, db.Pool)
	if n := countSchemas(t, ctx, db.Pool); n != 3 {
		t.Fatalf("%d of the three schemas exist after up, want 3", n)
	}

	// A foreign key across schemas: a refresh token of a user that does not
	// exist is refused, and the same token of an existing user is accepted.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO identity.refresh_tokens (id, family_id, user_id, token_hash, expires_at) VALUES ('f2000000-0000-0000-0000-000000000001', 'f2000000-0000-0000-0000-000000000001', 'f2000000-0000-0000-0000-000000000002', 'orphan-hash', NOW() + INTERVAL '1 hour')`); err == nil || !strings.Contains(err.Error(), "refresh_tokens_user_id_fkey") {
		t.Fatalf("token of a missing user: got %v, want a violation of refresh_tokens_user_id_fkey", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO identity.users (id, email, status) VALUES ('f2000000-0000-0000-0000-000000000003', 'fk@example.com', 'active')`); err != nil {
		t.Fatalf("insert user in identity: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO identity.refresh_tokens (id, family_id, user_id, token_hash, expires_at) VALUES ('f2000000-0000-0000-0000-000000000004', 'f2000000-0000-0000-0000-000000000004', 'f2000000-0000-0000-0000-000000000003', 'valid-fk-hash', NOW() + INTERVAL '1 hour')`); err != nil {
		t.Fatalf("token of an existing user: %v", err)
	}

	// Roll back 024. Every table is in public again and no schema is left.
	rollBackSchemaMove(t, ctx, m)
	assertLocations(t, ctx, db.Pool, false)
	if n := countSchemas(t, ctx, db.Pool); n != 0 {
		t.Fatalf("%d of the three schemas exist after down, want none", n)
	}

	// A user written while 024 is not applied, in the 023 layout.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, status) VALUES ('f1000000-0000-0000-0000-000000000010', 'before-024@example.com', 'active')`); err != nil {
		t.Fatalf("insert user before 024: %v", err)
	}

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("reapply 024: %v", err)
	}
	assertAppliedVersions(t, results, 24)
	assertLocations(t, ctx, db.Pool, true)
	assertNoOtherPublicTables(t, ctx, db.Pool)
	var email string
	if err := db.Pool.QueryRow(ctx, `SELECT email FROM identity.users WHERE id = 'f1000000-0000-0000-0000-000000000010'`).Scan(&email); err != nil {
		t.Fatalf("read user from identity.users after up: %v", err)
	}
	if email != "before-024@example.com" {
		t.Fatalf("email after up = %q, want before-024@example.com", email)
	}

	// Down again: the same user is back in public.
	rollBackSchemaMove(t, ctx, m)
	assertLocations(t, ctx, db.Pool, false)
	if err := db.Pool.QueryRow(ctx, `SELECT email FROM public.users WHERE id = 'f1000000-0000-0000-0000-000000000010'`).Scan(&email); err != nil {
		t.Fatalf("read user from public.users after down: %v", err)
	}
	if email != "before-024@example.com" {
		t.Fatalf("email after down = %q, want before-024@example.com", email)
	}

	// Up again: the schemas come back and the row is still there.
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("second reapply 024: %v", err)
	}
	assertLocations(t, ctx, db.Pool, true)
	if err := db.Pool.QueryRow(ctx, `SELECT email FROM identity.users WHERE id = 'f1000000-0000-0000-0000-000000000010'`).Scan(&email); err != nil {
		t.Fatalf("read user from identity.users after second up: %v", err)
	}
	if email != "before-024@example.com" {
		t.Fatalf("email after second up = %q, want before-024@example.com", email)
	}
	if current, target, err := m.Version(ctx); err != nil || current != 24 || target != 24 {
		t.Fatalf("version = %d/%d, err=%v, want 24/24", current, target, err)
	}
	assertGooseRows(t, ctx, db.Pool, db.Schema, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24)
}
