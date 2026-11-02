package migrations

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// applyLegacyRunner reproduces the state the previous hand-written runner left
// behind: schema_migrations with every file name and the Up SQL applied.
func applyLegacyRunner(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, legacyUp(t, string(body))); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			t.Fatalf("record legacy %s: %v", name, err)
		}
	}
}

func legacyUp(t *testing.T, sql string) string {
	t.Helper()
	const marker = "-- +goose Up"
	idx := strings.Index(sql, marker)
	if idx < 0 {
		t.Fatalf("migration has no Up marker")
	}
	up := sql[idx+len(marker):]
	if down := strings.Index(up, "-- +goose Down"); down >= 0 {
		up = up[:down]
	}
	return strings.TrimSpace(up)
}

func insertLegacyData(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []string{
		`INSERT INTO users (id, email, mobile, password_hash, display_name, status)
		 VALUES ('11111111-1111-1111-1111-111111111111', 'person@example.com', '+989121234567', 'hash-1', 'Person', 'active')`,
		`INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at)
		 VALUES ('22222222-2222-2222-2222-222222222222', 'person@example.com', 'email', 'code-hash', NOW() + INTERVAL '10 minutes')`,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
		 VALUES ('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'refresh-hash', NOW() + INTERVAL '30 days')`,
		`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes)
		 VALUES ('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'deploy', 'abcdefgh', 'token-hash', ARRAY['read'])`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("insert legacy data: %v", err)
		}
	}
}

func TestFreshDatabaseAppliesEmbeddedMigrations(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	m, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("applied %d migrations, want 3", len(results))
	}
	for _, table := range []string{"users", "otp_challenges", "refresh_tokens", "api_tokens", "goose_db_version"} {
		if !tableExists(t, ctx, db.Pool, db.Schema, table) {
			t.Fatalf("%s was not created", table)
		}
	}
	if current, target, err := m.Version(ctx); err != nil || current != 3 || target != 3 {
		t.Fatalf("version = %d/%d, err=%v, want 3/3", current, target, err)
	}
}

// TestAdoptionOnLegacyDatabaseChangesNothing is the production adoption proof:
// a database created by the old runner (real users, OTP, session and API-token
// rows) is adopted without running migration SQL. Table and index relfilenodes
// and every row must be identical afterwards, and a second `up` is a no-op.
func TestAdoptionOnLegacyDatabaseChangesNothing(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	applyLegacyRunner(t, ctx, db.Pool)
	insertLegacyData(t, ctx, db.Pool)
	beforeSchema := schemaSnapshot(t, ctx, db.Pool, db.Schema)
	beforeData := dataSnapshot(t, ctx, db.Pool)
	beforeLegacy := legacySnapshot(t, ctx, db.Pool)

	m, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("adopt legacy database: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("adoption ran %d migrations, want 0", len(results))
	}

	if after := schemaSnapshot(t, ctx, db.Pool, db.Schema); after != beforeSchema {
		t.Fatalf("adoption changed the schema:\nbefore: %s\nafter:  %s", beforeSchema, after)
	}
	if after := dataSnapshot(t, ctx, db.Pool); after != beforeData {
		t.Fatalf("adoption changed the data:\nbefore: %s\nafter:  %s", beforeData, after)
	}
	if after := legacySnapshot(t, ctx, db.Pool); after != beforeLegacy {
		t.Fatalf("adoption changed schema_migrations:\nbefore: %s\nafter:  %s", beforeLegacy, after)
	}
	if current, target, err := m.Version(ctx); err != nil || current != 3 || target != 3 {
		t.Fatalf("version = %d/%d, err=%v, want 3/3", current, target, err)
	}
	assertGooseRows(t, ctx, db.Pool, db.Schema, 0, 1, 2, 3)

	// A second migrate up must change nothing and run no migration.
	again, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("second adoption: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second up ran %d migrations, want 0", len(again))
	}
	if after := schemaSnapshot(t, ctx, db.Pool, db.Schema); after != beforeSchema {
		t.Fatalf("second up changed the schema:\nbefore: %s\nafter:  %s", beforeSchema, after)
	}
	if after := dataSnapshot(t, ctx, db.Pool); after != beforeData {
		t.Fatalf("second up changed the data:\nbefore: %s\nafter:  %s", beforeData, after)
	}
}

// TestStatusBeforeUpStillAdopts covers the dangerous order: goose creates an
// empty version table when status runs first. Adoption must still map the
// legacy versions, or up would re-run ALTER statements.
func TestStatusBeforeUpStillAdopts(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	applyLegacyRunner(t, ctx, db.Pool)
	insertLegacyData(t, ctx, db.Pool)
	beforeSchema := schemaSnapshot(t, ctx, db.Pool, db.Schema)
	beforeData := dataSnapshot(t, ctx, db.Pool)
	beforeLegacy := legacySnapshot(t, ctx, db.Pool)

	m, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	if _, err := m.Status(ctx); err != nil {
		t.Fatalf("status on legacy database: %v", err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up after status: %v", err)
	}
	if after := schemaSnapshot(t, ctx, db.Pool, db.Schema); after != beforeSchema {
		t.Fatalf("status then up changed the schema:\nbefore: %s\nafter:  %s", beforeSchema, after)
	}
	if after := dataSnapshot(t, ctx, db.Pool); after != beforeData {
		t.Fatalf("status then up changed the data")
	}
	if after := legacySnapshot(t, ctx, db.Pool); after != beforeLegacy {
		t.Fatalf("status then up changed schema_migrations")
	}
	assertGooseRows(t, ctx, db.Pool, db.Schema, 0, 1, 2, 3)
}

func TestDownRemovesTheLastMigrationAndUpReappliesIt(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	m, err := Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := m.Down(ctx)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if result.Source.Version != 3 {
		t.Fatalf("rolled back version %d, want 3", result.Source.Version)
	}
	if tableExists(t, ctx, db.Pool, db.Schema, "api_tokens") {
		t.Fatal("down left api_tokens in place")
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("reapply after down: %v", err)
	}
	if !tableExists(t, ctx, db.Pool, db.Schema, "api_tokens") {
		t.Fatal("up after down did not recreate api_tokens")
	}
}

// schemaSnapshot is stable text for every legacy table and index. A
// relfilenode changes when an object is dropped and recreated.
func schemaSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) string {
	t.Helper()
	const query = `
		SELECT c.relname || ':' || c.relfilenode::text || ':' || c.relkind::text
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1
		  AND c.relname IN (
			'users', 'users_email_key', 'users_mobile_key', 'users_mobile_idx', 'users_pkey',
			'otp_challenges', 'otp_challenges_pkey', 'otp_challenges_identifier_idx',
			'refresh_tokens', 'refresh_tokens_pkey',
			'api_tokens', 'api_tokens_pkey', 'api_tokens_prefix_idx', 'api_tokens_user_idx'
		  )
		ORDER BY c.relname`
	rows, err := pool.Query(ctx, query, schema)
	if err != nil {
		t.Fatalf("read schema snapshot: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func dataSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	queries := []string{
		`SELECT id::text || '|' || COALESCE(email, '') || '|' || COALESCE(mobile, '') || '|' || COALESCE(password_hash, '') || '|' || COALESCE(display_name, '') || '|' || status FROM users ORDER BY id`,
		`SELECT id::text || '|' || identifier || '|' || channel || '|' || code_hash || '|' || attempts::text FROM otp_challenges ORDER BY id`,
		`SELECT id::text || '|' || user_id::text || '|' || token_hash || '|' || (revoked_at IS NULL)::text FROM refresh_tokens ORDER BY id`,
		`SELECT id::text || '|' || user_id::text || '|' || name || '|' || prefix || '|' || token_hash || '|' || array_to_string(scopes, ',') FROM api_tokens ORDER BY id`,
	}
	var lines []string
	for _, query := range queries {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatalf("read data snapshot: %v", err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return strings.Join(lines, "\n")
}

func legacySnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT name || '|' || applied_at::text FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func tableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')
		)`, schema, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func assertGooseRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string, want ...int64) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT version_id FROM "`+schema+`".goose_db_version WHERE is_applied ORDER BY id`)
	if err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		got = append(got, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("goose versions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("goose versions = %v, want %v", got, want)
		}
	}
}
