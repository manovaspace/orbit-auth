package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// tokenFamily returns the id and family_id of the token stored under hash.
func tokenFamily(t *testing.T, ctx context.Context, store *Store, hash string) (id, family string) {
	t.Helper()
	if err := store.pool.QueryRow(ctx, `SELECT id::text, family_id::text FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&id, &family); err != nil {
		t.Fatalf("token family %q: %v", hash, err)
	}
	return id, family
}

func TestRefreshTokenFamiliesThroughStore(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	exp := time.Now().UTC().Add(time.Hour)

	user, err := store.FindOrCreateByEmail(ctx, "families@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// An empty family starts a family of one: family_id is the token's own id.
	if err := store.Store(ctx, user.ID, "", "", "fam-a", exp); err != nil {
		t.Fatal(err)
	}
	idA, familyA := tokenFamily(t, ctx, store, "fam-a")
	if familyA != idA {
		t.Fatalf("new family = %s, want the token id %s", familyA, idA)
	}

	// A token with a family keeps it.
	if err := store.Store(ctx, user.ID, "", familyA, "fam-b", exp); err != nil {
		t.Fatal(err)
	}
	if _, familyB := tokenFamily(t, ctx, store, "fam-b"); familyB != familyA {
		t.Fatalf("rotated family = %s, want %s", familyB, familyA)
	}
	if err := store.Store(ctx, user.ID, "", "not-a-uuid", "fam-bad", exp); err == nil {
		t.Fatal("invalid family id was accepted")
	}

	// An unrelated sign-in gets its own family.
	if err := store.Store(ctx, user.ID, "", "", "fam-other", exp); err != nil {
		t.Fatal(err)
	}
	_, familyOther := tokenFamily(t, ctx, store, "fam-other")
	if familyOther == familyA {
		t.Fatal("a new sign-in joined an existing family")
	}

	// A rotation marks the token rotated; a logout does not.
	grant, err := store.Consume(ctx, "fam-a", true)
	if err != nil || grant.UserID != user.ID || grant.FamilyID != familyA {
		t.Fatalf("consume rotate = %+v, %v", grant, err)
	}
	if _, err := store.Consume(ctx, "fam-logout", false); !errors.Is(err, domain.ErrRefreshTokenNotFound) {
		t.Fatalf("consume of unknown token err = %v", err)
	}
	if err := store.Store(ctx, user.ID, "", familyA, "fam-logout", exp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(ctx, "fam-logout", false); err != nil {
		t.Fatalf("logout consume: %v", err)
	}
	var rotatedA, rotatedLogout *time.Time
	if err := store.pool.QueryRow(ctx, `SELECT rotated_at FROM refresh_tokens WHERE token_hash = 'fam-a'`).Scan(&rotatedA); err != nil || rotatedA == nil {
		t.Fatalf("rotated token rotated_at = %v, %v; want set", rotatedA, err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT rotated_at FROM refresh_tokens WHERE token_hash = 'fam-logout'`).Scan(&rotatedLogout); err != nil || rotatedLogout != nil {
		t.Fatalf("logout token rotated_at = %v, %v; want NULL", rotatedLogout, err)
	}

	// FindRotated finds only the rotated token.
	rotated, err := store.FindRotated(ctx, "fam-a")
	if err != nil || rotated.UserID != user.ID || rotated.FamilyID != familyA || rotated.RotatedAt.IsZero() {
		t.Fatalf("FindRotated = %+v, %v", rotated, err)
	}
	if _, err := store.FindRotated(ctx, "fam-logout"); !errors.Is(err, domain.ErrRefreshTokenNotFound) {
		t.Fatalf("FindRotated of logout token err = %v, want ErrRefreshTokenNotFound", err)
	}
	if _, err := store.FindRotated(ctx, "never-issued"); !errors.Is(err, domain.ErrRefreshTokenNotFound) {
		t.Fatalf("FindRotated of unknown token err = %v, want ErrRefreshTokenNotFound", err)
	}

	// RevokeFamily revokes the live rows of one family only.
	n, err := store.RevokeFamily(ctx, familyA)
	if err != nil || n != 1 {
		t.Fatalf("RevokeFamily = %d, %v; want 1 (fam-b only)", n, err)
	}
	if _, _, err := store.FindValid(ctx, "fam-b"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("family member still valid after RevokeFamily: %v", err)
	}
	if _, _, err := store.FindValid(ctx, "fam-other"); err != nil {
		t.Fatalf("token of another family revoked: %v", err)
	}
	if n, err := store.RevokeFamily(ctx, familyA); err != nil || n != 0 {
		t.Fatalf("second RevokeFamily = %d, %v; want 0", n, err)
	}
	if _, err := store.RevokeFamily(ctx, "not-a-uuid"); err == nil {
		t.Fatal("RevokeFamily accepted an invalid family id")
	}
	// The rotated token stays findable after its family is revoked, so a
	// later reuse is still recognised.
	if _, err := store.FindRotated(ctx, "fam-a"); err != nil {
		t.Fatalf("FindRotated after RevokeFamily: %v", err)
	}
}
