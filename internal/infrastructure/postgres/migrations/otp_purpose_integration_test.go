package migrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// TestOTPPurposeMigrationAppliesAndRollsBack checks migration 023 up, down and
// up. A challenge written before 023 runs becomes a sign-in code, the check
// refuses a purpose outside the three flows, and down then up works. Migration
// 024 sits on top of 023, so it is rolled back first and applied again with 023.
func TestOTPPurposeMigrationAppliesAndRollsBack(t *testing.T) {
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
	if !columnExists(t, ctx, db.Pool, "identity", "otp_challenges", "purpose") ||
		!constraintExists(t, ctx, db.Pool, "identity", "otp_challenges", "otp_challenges_purpose_chk") ||
		!indexExists(t, ctx, db.Pool, "identity", "otp_challenges_lookup_idx") ||
		indexExists(t, ctx, db.Pool, "identity", "otp_challenges_identifier_idx") {
		t.Fatal("up did not create the purpose column, check and lookup index")
	}

	// Roll back 024 and 023 and write a challenge in the old shape, as a database
	// that ran migrations 001 to 022 holds it, then apply 023 and 024 again.
	rollBackSchemaMove(t, ctx, m)
	result, err := m.Down(ctx)
	if err != nil {
		t.Fatalf("down 023: %v", err)
	}
	if result.Source.Version != 23 {
		t.Fatalf("rolled back version %d, want 23", result.Source.Version)
	}
	if columnExists(t, ctx, db.Pool, db.Schema, "otp_challenges", "purpose") ||
		constraintExists(t, ctx, db.Pool, db.Schema, "otp_challenges", "otp_challenges_purpose_chk") ||
		indexExists(t, ctx, db.Pool, db.Schema, "otp_challenges_lookup_idx") ||
		!indexExists(t, ctx, db.Pool, db.Schema, "otp_challenges_identifier_idx") {
		t.Fatal("down 023 did not restore the 022 shape of otp_challenges")
	}

	if _, err := db.Pool.Exec(ctx, `INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at) VALUES ('e1000000-0000-0000-0000-000000000001', 'purpose-legacy@example.com', 'email', 'legacy-purpose-hash', NOW() + INTERVAL '10 minutes')`); err != nil {
		t.Fatalf("insert legacy challenge: %v", err)
	}

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("reapply 023 and 024: %v", err)
	}
	assertAppliedVersions(t, results, 23, 24)

	var purpose string
	if err := db.Pool.QueryRow(ctx, `SELECT purpose FROM otp_challenges WHERE id = 'e1000000-0000-0000-0000-000000000001'`).Scan(&purpose); err != nil {
		t.Fatal(err)
	}
	if purpose != "sign_in" {
		t.Fatalf("purpose of a challenge written before 023 = %q, want sign_in", purpose)
	}

	if _, err := db.Pool.Exec(ctx, `INSERT INTO otp_challenges (id, identifier, channel, purpose, code_hash, expires_at) VALUES ('e1000000-0000-0000-0000-000000000002', 'purpose-other@example.com', 'email', 'other', 'other-hash', NOW() + INTERVAL '10 minutes')`); err == nil || !strings.Contains(err.Error(), "otp_challenges_purpose_chk") {
		t.Fatalf("purpose 'other': got %v, want otp_challenges_purpose_chk violation", err)
	}

	if current, target, err := m.Version(ctx); err != nil || current != 24 || target != 24 {
		t.Fatalf("version = %d/%d, err=%v, want 24/24", current, target, err)
	}
	assertGooseRows(t, ctx, db.Pool, db.Schema, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24)
}
