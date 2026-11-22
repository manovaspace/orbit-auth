package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func TestUsernameSetFindAndUniqueness(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	first, err := store.FindOrCreateByEmail(ctx, "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.FindOrCreateByEmail(ctx, "second@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Accounts without a username coexist: the index allows many NULLs.
	if err := store.SetUsername(ctx, first.ID, "chef.one"); err != nil {
		t.Fatal(err)
	}
	found, err := store.FindByUsername(ctx, "chef.one")
	if err != nil || found.ID != first.ID || found.Username != "chef.one" || found.Email != "first@example.com" {
		t.Fatalf("find by username = %+v, err = %v", found, err)
	}
	if byID, err := store.FindByID(ctx, first.ID); err != nil || byID.Username != "chef.one" {
		t.Fatalf("find by id username = %+v, err = %v", byID, err)
	}

	if err := store.SetUsername(ctx, second.ID, "chef.one"); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Fatalf("taken username err = %v, want ErrUsernameTaken", err)
	}
	if err := store.SetUsername(ctx, first.ID, "chef.one"); err != nil {
		t.Fatalf("setting the same username again: %v", err)
	}
	if err := store.SetUsername(ctx, "11111111-1111-4111-8111-111111111111", "ghost.user"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user err = %v, want ErrUserNotFound", err)
	}
	if _, err := store.FindByUsername(ctx, "nobody.here"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing username err = %v, want pgx.ErrNoRows", err)
	}
	if byID, err := store.FindByID(ctx, second.ID); err != nil || byID.Username != "" {
		t.Fatalf("second user username = %q, err = %v; want empty", byID.Username, err)
	}

	// The database pins the same rule as the domain, including lower case.
	for _, value := range []string{"Upper.Case", "ab", "1abc", "has space", "+abc"} {
		_, err := db.Pool.Exec(ctx, `UPDATE users SET username = $1 WHERE id = $2::uuid`, value, second.ID)
		if err == nil || !strings.Contains(err.Error(), "users_username_format") {
			t.Fatalf("username %q: err = %v, want users_username_format", value, err)
		}
	}
}

func TestUpsertDemoUserStoresAndKeepsUsername(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	if err := store.UpsertDemoUser(ctx, "demo@example.com", "", "demo.user", "hash-1", "Demo"); err != nil {
		t.Fatal(err)
	}
	found, err := store.FindByUsername(ctx, "demo.user")
	if err != nil || found.Email != "demo@example.com" {
		t.Fatalf("demo user by username = %+v, err = %v", found, err)
	}

	// An upsert without a username keeps the stored one.
	if err := store.UpsertDemoUser(ctx, "demo@example.com", "", "", "hash-2", "Demo Two"); err != nil {
		t.Fatal(err)
	}
	found, err = store.FindByUsername(ctx, "demo.user")
	if err != nil || found.PasswordHash != "hash-2" || found.DisplayName != "Demo Two" {
		t.Fatalf("after upsert without username = %+v, err = %v", found, err)
	}
}
