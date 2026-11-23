package migrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// TestRefreshTokenFamilyMigrationAppliesAndRollsBack checks migration 022 up,
// down and up. Tokens that exist before 022 runs become families of one, and
// the family and rotation rules hold after it.
func TestRefreshTokenFamilyMigrationAppliesAndRollsBack(t *testing.T) {
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
	if !columnExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "family_id") ||
		!columnExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "rotated_at") ||
		!indexExists(t, ctx, db.Pool, db.Schema, "refresh_tokens_family_live_idx") ||
		!constraintExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "refresh_tokens_rotated_is_revoked_chk") {
		t.Fatal("up did not create the refresh token family objects")
	}
	if constraintExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "refresh_tokens_family_not_null_chk") {
		t.Fatal("up left the temporary family NOT NULL check in place")
	}

	// Roll back 022 and put a token in the old shape, as a database that ran
	// migrations 001 to 021 holds it, then apply 022 again.
	result, err := m.Down(ctx)
	if err != nil {
		t.Fatalf("down 022: %v", err)
	}
	if result.Source.Version != 22 {
		t.Fatalf("rolled back version %d, want 22", result.Source.Version)
	}
	if columnExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "family_id") ||
		columnExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "rotated_at") ||
		indexExists(t, ctx, db.Pool, db.Schema, "refresh_tokens_family_live_idx") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "refresh_tokens", "refresh_tokens_rotated_is_revoked_chk") {
		t.Fatal("down 022 left a refresh token family object in place")
	}

	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, status) VALUES ('d1000000-0000-0000-0000-000000000001', 'legacy-family@example.com', 'active')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES ('d1000000-0000-0000-0000-000000000002', 'd1000000-0000-0000-0000-000000000001', 'legacy-family-hash', NOW() + INTERVAL '1 hour')`); err != nil {
		t.Fatalf("insert legacy token: %v", err)
	}

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("reapply 022: %v", err)
	}
	assertAppliedVersions(t, results, 22)

	var familyID, id string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text, family_id::text FROM refresh_tokens WHERE token_hash = 'legacy-family-hash'`).Scan(&id, &familyID); err != nil {
		t.Fatal(err)
	}
	if familyID != id {
		t.Fatalf("pre-existing token family_id = %s, want its own id %s", familyID, id)
	}

	if _, err := db.Pool.Exec(ctx, `UPDATE refresh_tokens SET family_id = NULL WHERE token_hash = 'legacy-family-hash'`); err == nil || !strings.Contains(err.Error(), "not-null") {
		t.Fatalf("NULL family_id: got %v, want a not-null violation", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = NOW() WHERE token_hash = 'legacy-family-hash'`); err == nil || !strings.Contains(err.Error(), "refresh_tokens_rotated_is_revoked_chk") {
		t.Fatalf("rotated_at with NULL revoked_at: got %v, want refresh_tokens_rotated_is_revoked_chk", err)
	}

	if current, target, err := m.Version(ctx); err != nil || current != 22 || target != 22 {
		t.Fatalf("version = %d/%d, err=%v, want 22/22", current, target, err)
	}
	assertGooseRows(t, ctx, db.Pool, db.Schema, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22)
}
