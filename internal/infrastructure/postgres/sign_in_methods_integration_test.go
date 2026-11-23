package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func TestGoogleLinkRulesAreEnforced(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	a, err := store.FindOrCreateByEmail(ctx, "link-a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.FindOrCreateByEmail(ctx, "link-b@example.com")
	if err != nil {
		t.Fatal(err)
	}

	email, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-link-a", "Link-A@Example.com", true), a.ID, now)
	if err != nil || email != "link-a@example.com" {
		t.Fatalf("link = %q, %v; want the normalised email", email, err)
	}
	link, linked, err := store.GoogleLinkForUser(ctx, domain.GoogleProviderName, a.ID)
	if err != nil || !linked || link.Email != "link-a@example.com" || link.LinkedAt.IsZero() {
		t.Fatalf("GoogleLinkForUser = %+v, %v, %v", link, linked, err)
	}

	if _, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-link-a2", "link-a@example.com", true), a.ID, now); !errors.Is(err, domain.ErrGoogleAlreadyLinked) {
		t.Fatalf("second link to the same account err = %v, want ErrGoogleAlreadyLinked", err)
	}
	if _, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-link-a", "link-a@example.com", true), b.ID, now); !errors.Is(err, domain.ErrGoogleIdentityTaken) {
		t.Fatalf("subject held by another account err = %v, want ErrGoogleIdentityTaken", err)
	}
	if _, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-ghost", "ghost@example.com", true), "00000000-0000-0000-0000-000000000000", now); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown account err = %v, want ErrUserNotFound", err)
	}

	if _, err := store.DisableUser(ctx, b.ID, domain.StaffActor{Subject: "staff-1", Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-disabled", "link-b@example.com", true), b.ID, now); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("disabled account err = %v, want ErrAccountNotActive", err)
	}
}

func TestGoogleUnlinkRefusesTheLastSignInMethod(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Google only: no password, so unlinking would leave no way to sign in.
	only, err := store.FindOrCreateByEmail(ctx, "unlink-only@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-only", "unlink-only@example.com", true), only.ID, now); err != nil {
		t.Fatal(err)
	}
	actor := domain.UserAuditActor(only.ID)
	if err := store.UnlinkGoogleIdentity(ctx, domain.GoogleProviderName, only.ID, actor, now); !errors.Is(err, domain.ErrLastSignInMethod) {
		t.Fatalf("unlink last method err = %v, want ErrLastSignInMethod", err)
	}
	if _, linked, _ := store.GoogleLinkForUser(ctx, domain.GoogleProviderName, only.ID); !linked {
		t.Fatal("refused unlink removed the identity")
	}

	// With a password and a username, the password is a usable method.
	if err := store.UpsertDemoUser(ctx, "unlink-pass@example.com", "", "unlink-pass-name", "hash", "Pass"); err != nil {
		t.Fatal(err)
	}
	withPass, err := store.FindOrCreateByEmail(ctx, "unlink-pass@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkGoogleIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-pass", "unlink-pass@example.com", true), withPass.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.UnlinkGoogleIdentity(ctx, domain.GoogleProviderName, withPass.ID, domain.UserAuditActor(withPass.ID), now); err != nil {
		t.Fatalf("unlink with a password method: %v", err)
	}
	if _, linked, _ := store.GoogleLinkForUser(ctx, domain.GoogleProviderName, withPass.ID); linked {
		t.Fatal("identity still linked after unlink")
	}
	if err := store.UnlinkGoogleIdentity(ctx, domain.GoogleProviderName, withPass.ID, domain.UserAuditActor(withPass.ID), now); !errors.Is(err, domain.ErrGoogleNotLinked) {
		t.Fatalf("unlink without identity err = %v, want ErrGoogleNotLinked", err)
	}
}

func TestSetPasswordRevokesOtherSessionsAndKeepsTheCurrentOne(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.UpsertDemoUser(ctx, "password-set@example.com", "", "password-set-name", "old-hash", "Pw"); err != nil {
		t.Fatal(err)
	}
	user, err := store.FindOrCreateByEmail(ctx, "password-set@example.com")
	if err != nil {
		t.Fatal(err)
	}
	exp := now.Add(time.Hour)
	for _, hash := range []string{"keep-hash", "other-hash"} {
		if err := store.Store(ctx, user.ID, "", "", hash, exp); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetPassword(ctx, user.ID, "new-hash", "keep-hash", false, domain.UserAuditActor(user.ID), now); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, _, err := store.FindValid(ctx, "keep-hash"); err != nil {
		t.Fatalf("current session revoked: %v", err)
	}
	if _, _, err := store.FindValid(ctx, "other-hash"); err == nil {
		t.Fatal("other session still valid")
	}
	var hash string
	if err := store.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1::uuid`, user.ID).Scan(&hash); err != nil || hash != "new-hash" {
		t.Fatalf("stored hash = %q, err = %v", hash, err)
	}

	if _, err := store.DisableUser(ctx, user.ID, domain.StaffActor{Subject: "staff-1", Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword(ctx, user.ID, "again-hash", "", false, domain.UserAuditActor(user.ID), now); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("disabled account err = %v, want ErrAccountNotActive", err)
	}
}

func TestRevokeSessionsKeepsOnlyThePresentedToken(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	user, err := store.FindOrCreateByEmail(ctx, "revoke-all@example.com")
	if err != nil {
		t.Fatal(err)
	}
	exp := now.Add(time.Hour)
	for _, hash := range []string{"rev-keep", "rev-one", "rev-two"} {
		if err := store.Store(ctx, user.ID, "", "", hash, exp); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.RevokeSessions(ctx, user.ID, "rev-keep", domain.UserAuditActor(user.ID), now); err != nil {
		t.Fatalf("revoke sessions: %v", err)
	}
	if _, _, err := store.FindValid(ctx, "rev-keep"); err != nil {
		t.Fatalf("kept session revoked: %v", err)
	}
	for _, hash := range []string{"rev-one", "rev-two"} {
		if _, _, err := store.FindValid(ctx, hash); err == nil {
			t.Fatalf("session %s still valid", hash)
		}
	}

	if err := store.RevokeSessions(ctx, user.ID, "", domain.UserAuditActor(user.ID), now); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if _, _, err := store.FindValid(ctx, "rev-keep"); err == nil {
		t.Fatal("kept session still valid after revoke-all")
	}
}
