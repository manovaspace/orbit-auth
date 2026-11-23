package migrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// rollBackIdentityConstraints rolls back migrations 021, 020 and 019, in that
// order, and fails unless each rollback reports the expected version.
func rollBackIdentityConstraints(t *testing.T, ctx context.Context, m *Migrator) {
	t.Helper()
	for _, want := range []int64{21, 20, 19} {
		result, err := m.Down(ctx)
		if err != nil {
			t.Fatalf("down %d: %v", want, err)
		}
		if result.Source.Version != want {
			t.Fatalf("rolled back version %d, want %d", result.Source.Version, want)
		}
	}
}

// assertAppliedVersions fails unless results are exactly the want versions, in order.
func assertAppliedVersions(t *testing.T, results []*goose.MigrationResult, want ...int64) {
	t.Helper()
	if len(results) != len(want) {
		t.Fatalf("applied %d migrations, want %d", len(results), len(want))
	}
	for i, version := range want {
		if results[i].Source.Version != version {
			t.Fatalf("applied migration %d at position %d, want %d", results[i].Source.Version, i, version)
		}
	}
}

// constraintExists reports whether a constraint named name exists on table.
func constraintExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, table, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_constraint c
			JOIN pg_class cl ON cl.oid = c.conrelid
			JOIN pg_namespace n ON n.oid = cl.relnamespace
			WHERE n.nspname = $1 AND cl.relname = $2 AND c.conname = $3
		)`, schema, table, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

// uniqueMobileIndexes counts the unique indexes on users that cover mobile.
// Constraint-backed indexes appear in pg_indexes too.
func uniqueMobileIndexes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = $1 AND tablename = 'users'
		  AND indexdef LIKE 'CREATE UNIQUE INDEX%'
		  AND indexdef LIKE '%(mobile)%'`, schema).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIdentityConstraintMigrationsRollBackAndReapply(t *testing.T) {
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
	if !indexExists(t, ctx, db.Pool, db.Schema, "users_email_lower_key") ||
		!indexExists(t, ctx, db.Pool, db.Schema, "refresh_tokens_token_hash_key") ||
		!indexExists(t, ctx, db.Pool, db.Schema, "otp_challenges_expires_idx") ||
		indexExists(t, ctx, db.Pool, db.Schema, "users_mobile_idx") {
		t.Fatal("up did not leave the expected identity indexes")
	}
	if n := uniqueMobileIndexes(t, ctx, db.Pool, db.Schema); n != 1 {
		t.Fatalf("users.mobile has %d unique indexes after up, want exactly 1", n)
	}
	if !constraintExists(t, ctx, db.Pool, db.Schema, "api_tokens", "api_tokens_user_id_cascade_fkey") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "api_tokens", "api_tokens_user_id_fkey") {
		t.Fatal("up did not replace the api_tokens user foreign key")
	}

	rollBackIdentityConstraints(t, ctx, m)
	if current, target, err := m.Version(ctx); err != nil || current != 18 || target != 21 {
		t.Fatalf("version after down = %d/%d, err=%v, want 18/21", current, target, err)
	}
	if indexExists(t, ctx, db.Pool, db.Schema, "users_email_lower_key") ||
		indexExists(t, ctx, db.Pool, db.Schema, "refresh_tokens_token_hash_key") ||
		indexExists(t, ctx, db.Pool, db.Schema, "otp_challenges_expires_idx") {
		t.Fatal("down left an identity index in place")
	}
	if !indexExists(t, ctx, db.Pool, db.Schema, "users_mobile_idx") {
		t.Fatal("down did not restore users_mobile_idx from migration 002")
	}
	if constraintExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "refresh_tokens_user_id_fkey") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "refresh_tokens_org_id_fkey") {
		t.Fatal("down left a refresh token foreign key in place")
	}
	if !constraintExists(t, ctx, db.Pool, db.Schema, "api_tokens", "api_tokens_user_id_fkey") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "api_tokens", "api_tokens_user_id_cascade_fkey") {
		t.Fatal("down did not restore the original api_tokens user foreign key")
	}
	if constraintExists(t, ctx, db.Pool, db.Schema, "users", "users_mobile_e164_chk") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "otp_challenges", "otp_challenges_channel_chk") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "otp_challenges", "otp_challenges_expiry_after_created_chk") {
		t.Fatal("down left a check constraint of migration 019 in place")
	}

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("reapply 019 to 021: %v", err)
	}
	assertAppliedVersions(t, results, 19, 20, 21)
	if !indexExists(t, ctx, db.Pool, db.Schema, "users_email_lower_key") ||
		!indexExists(t, ctx, db.Pool, db.Schema, "refresh_tokens_token_hash_key") ||
		indexExists(t, ctx, db.Pool, db.Schema, "users_mobile_idx") {
		t.Fatal("reapply did not recreate the identity indexes")
	}
	if !constraintExists(t, ctx, db.Pool, db.Schema, "api_tokens", "api_tokens_user_id_cascade_fkey") {
		t.Fatal("reapply did not recreate the api_tokens cascade foreign key")
	}
	if current, target, err := m.Version(ctx); err != nil || current != 21 || target != 21 {
		t.Fatalf("version after reapply = %d/%d, err=%v, want 21/21", current, target, err)
	}
	assertGooseRows(t, ctx, db.Pool, db.Schema, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21)
}

func TestEmailUniquenessIgnoresCaseAndFindUserByEmailMatches(t *testing.T) {
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

	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, status) VALUES ('c1000000-0000-0000-0000-000000000001', 'Ali@X.com', 'active')`); err != nil {
		t.Fatalf("insert mixed-case email: %v", err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO users (id, email, status) VALUES ('c1000000-0000-0000-0000-000000000002', 'ali@x.com', 'active')`)
	if err == nil || !strings.Contains(err.Error(), "users_email_lower_key") {
		t.Fatalf("case-only duplicate email: got %v, want a violation of users_email_lower_key", err)
	}

	row, err := sqlcgen.New(db.Pool).FindUserByEmail(ctx, "ali@x.com")
	if err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if row.ID != "c1000000-0000-0000-0000-000000000001" || row.Email != "Ali@X.com" {
		t.Fatalf("FindUserByEmail returned id %q email %q", row.ID, row.Email)
	}

	// The lower-cased lookup must be able to use the expression index. The
	// planner setting is session-scoped, so the EXPLAIN runs in one transaction.
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT id FROM users WHERE lower(email) = lower('ali@x.com')`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "users_email_lower_key") {
		t.Fatalf("lower(email) lookup does not use users_email_lower_key:\n%s", plan.String())
	}
}

func TestIdentityCheckConstraintsRejectBadRows(t *testing.T) {
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

	for _, rejected := range []struct{ name, statement, constraint string }{
		{"mobile without plus", `INSERT INTO users (id, mobile, status) VALUES ('c2000000-0000-0000-0000-000000000001', '0912', 'active')`, "users_mobile_e164_chk"},
		{"mobile with spaces", `INSERT INTO users (id, mobile, status) VALUES ('c2000000-0000-0000-0000-000000000002', '+98 912 123 4567', 'active')`, "users_mobile_e164_chk"},
		{"mobile with leading zero after plus", `INSERT INTO users (id, mobile, status) VALUES ('c2000000-0000-0000-0000-000000000003', '+0912123456', 'active')`, "users_mobile_e164_chk"},
		{"unknown OTP channel", `INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at, created_at) VALUES ('c3000000-0000-0000-0000-000000000001', 'ali@x.com', 'fax', 'h', NOW() + INTERVAL '5 minutes', NOW())`, "otp_challenges_channel_chk"},
		{"OTP expiry before creation", `INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at, created_at) VALUES ('c3000000-0000-0000-0000-000000000002', 'ali@x.com', 'email', 'h', NOW() - INTERVAL '1 minute', NOW())`, "otp_challenges_expiry_after_created_chk"},
		{"refresh token expiry before creation", `INSERT INTO users (id, email, status) VALUES ('c4000000-0000-0000-0000-000000000001', 'rt@x.com', 'active');
		 INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at) VALUES ('c4000000-0000-0000-0000-000000000002', 'c4000000-0000-0000-0000-000000000001', 'rt-hash', NOW() - INTERVAL '1 minute', NOW())`, "refresh_tokens_expiry_after_created_chk"},
	} {
		_, err := db.Pool.Exec(ctx, rejected.statement)
		if err == nil || !strings.Contains(err.Error(), rejected.constraint) {
			t.Fatalf("%s: got %v, want a violation of %s", rejected.name, err, rejected.constraint)
		}
	}

	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, mobile, status) VALUES ('c2000000-0000-0000-0000-000000000004', '+989121234567', 'active')`); err != nil {
		t.Fatalf("valid E.164 mobile rejected: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at, created_at) VALUES ('c3000000-0000-0000-0000-000000000003', '+989121234567', 'sms', 'h', NOW() + INTERVAL '5 minutes', NOW())`); err != nil {
		t.Fatalf("valid sms challenge rejected: %v", err)
	}
}

func TestPurgeUnverifiedSignupsCascadesTokens(t *testing.T) {
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

	for _, statement := range []string{
		`INSERT INTO users (id, email, status) VALUES ('c5000000-0000-0000-0000-000000000001', 'pending@x.com', 'signup_pending')`,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES ('c5000000-0000-0000-0000-000000000002', 'c5000000-0000-0000-0000-000000000001', 'pending-rt-hash', NOW() + INTERVAL '30 days')`,
		`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash) VALUES ('c5000000-0000-0000-0000-000000000003', 'c5000000-0000-0000-0000-000000000001', 'deploy', 'pendtok1', 'pending-api-hash')`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	purged, err := sqlcgen.New(db.Pool).PurgeUnverifiedSignups(ctx, pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true})
	if err != nil {
		t.Fatalf("PurgeUnverifiedSignups: %v", err)
	}
	if purged != 1 {
		t.Fatalf("PurgeUnverifiedSignups deleted %d users, want 1", purged)
	}
	for _, table := range []string{"refresh_tokens", "api_tokens"} {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s still holds %d rows of the purged account", table, count)
		}
	}
}

func TestDeleteEmptyOrganizationsWithRefreshTokenReferencingIt(t *testing.T) {
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

	for _, statement := range []string{
		`INSERT INTO users (id, email, status) VALUES ('c6000000-0000-0000-0000-000000000001', 'member@x.com', 'active')`,
		`INSERT INTO organizations (id, name, slug) VALUES ('c6000000-0000-0000-0000-000000000002', 'Empty Org', 'empty-org')`,
		`INSERT INTO refresh_tokens (id, user_id, org_id, token_hash, expires_at) VALUES ('c6000000-0000-0000-0000-000000000003', 'c6000000-0000-0000-0000-000000000001', 'c6000000-0000-0000-0000-000000000002', 'org-rt-hash', NOW() + INTERVAL '30 days')`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	deleted, err := sqlcgen.New(db.Pool).DeleteEmptyOrganizations(ctx)
	if err != nil {
		t.Fatalf("DeleteEmptyOrganizations: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteEmptyOrganizations deleted %d organizations, want 1", deleted)
	}
	var tokens int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens`).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 0 {
		t.Fatalf("refresh token of the deleted organization survived with %d rows", tokens)
	}
}
