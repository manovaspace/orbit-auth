package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func signupUser(email string, status string) domain.User {
	return domain.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: "hash",
		DisplayName:  "Signup User",
		Status:       status,
	}
}

func TestSignupUserCreateVerifyAndPurge(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := signupUser("new@example.com", domain.UserSignupPending)
	created, err := store.CreateSignupUser(ctx, user, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("new sign-up was not created")
	}
	found, err := store.FindByEmail(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.UserSignupPending || found.EmailVerified {
		t.Fatalf("pending user = %+v", found)
	}

	// A duplicate address is a no-op, not an error.
	duplicate := signupUser("new@example.com", domain.UserSignupPending)
	created, err = store.CreateSignupUser(ctx, duplicate, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate sign-up was created")
	}

	verifiedAt := now.Add(time.Minute)
	if err := store.VerifySignup(ctx, user.ID, verifiedAt); err != nil {
		t.Fatal(err)
	}
	found, err = store.FindByEmail(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.UserActive || !found.EmailVerified {
		t.Fatalf("verified user = %+v", found)
	}
	if err := store.VerifySignup(ctx, user.ID, verifiedAt); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("second verify err = %v, want ErrUserNotFound", err)
	}

	// Old pending accounts are purged; fresh pending and verified accounts are
	// kept.
	old := signupUser("old@example.com", domain.UserSignupPending)
	oldOrg := testOrg("Old Org", now)
	if _, err := store.CreateSignupUser(ctx, old, &oldOrg, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx,
		`UPDATE users SET created_at = $1::timestamptz WHERE id = $2::uuid`,
		now.Add(-8*24*time.Hour), old.ID); err != nil {
		t.Fatal(err)
	}
	fresh := signupUser("fresh@example.com", domain.UserSignupPending)
	if _, err := store.CreateSignupUser(ctx, fresh, nil, nil, now); err != nil {
		t.Fatal(err)
	}

	removed, err := store.PurgeUnverifiedSignups(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := store.FindByEmail(ctx, "old@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("old pending user err = %v, want no rows", err)
	}
	if _, err := store.GetOrg(ctx, oldOrg.ID); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("old signup organization err = %v, want ErrOrgNotFound", err)
	}
	if _, err := store.FindByEmail(ctx, "fresh@example.com"); err != nil {
		t.Fatalf("fresh pending user was purged: %v", err)
	}
	if _, err := store.FindByEmail(ctx, "new@example.com"); err != nil {
		t.Fatalf("verified user was purged: %v", err)
	}
}

func TestReissuePendingSignupRotatesChallengeAndCredentials(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := signupUser("retry@example.com", domain.UserSignupPending)
	if created, err := store.CreateSignupUser(ctx, user, nil, nil, now); err != nil || !created {
		t.Fatalf("create pending signup = %v, %v", created, err)
	}
	oldChallenge := domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: user.Email, Channel: domain.ChannelEmail,
		CodeHash: "old-hash", ExpiresAt: now.Add(domain.OTPTTL), CreatedAt: now,
	}
	if err := store.UpsertChallenge(ctx, oldChallenge); err != nil {
		t.Fatal(err)
	}

	newChallenge := domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: user.Email, Channel: domain.ChannelEmail,
		CodeHash: "new-hash", ExpiresAt: now.Add(2 * domain.OTPTTL), CreatedAt: now.Add(time.Minute),
	}
	updated := domain.User{Email: user.Email, PasswordHash: "owner-hash", DisplayName: "Owner"}
	userID, reissued, err := store.ReissuePendingSignup(ctx, updated, &newChallenge, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !reissued || userID != user.ID {
		t.Fatalf("reissue = %q, %v, want the existing user", userID, reissued)
	}
	found, err := store.FindByEmail(ctx, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if found.PasswordHash != "owner-hash" || found.DisplayName != "Owner" || found.Status != domain.UserSignupPending {
		t.Fatalf("reissued user = %+v", found)
	}
	latest, err := store.GetLatestChallenge(ctx, user.Email, domain.ChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != newChallenge.ID || latest.CodeHash != "new-hash" || !latest.CreatedAt.Equal(newChallenge.CreatedAt) {
		t.Fatalf("latest challenge = %+v, want the new one", latest)
	}
	var count int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_challenges WHERE identifier = $1 AND channel = $2`,
		user.Email, domain.ChannelEmail).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("challenges = %d, want only the new one", count)
	}

	// Once verified, the account is not reissued again.
	if err := store.VerifySignup(ctx, user.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	attacker := domain.User{Email: user.Email, PasswordHash: "attacker-hash", DisplayName: "Attacker"}
	if _, reissued, err := store.ReissuePendingSignup(ctx, attacker, nil, nil, now.Add(3*time.Minute)); err != nil || reissued {
		t.Fatalf("verified reissue = %v, %v, want a no-op", reissued, err)
	}
	found, err = store.FindByEmail(ctx, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if found.PasswordHash != "owner-hash" || found.DisplayName != "Owner" || !found.EmailVerified {
		t.Fatalf("verified account changed: %+v", found)
	}
}

func TestSignupUserCreatesOrganizationAndOwnerAtomically(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := signupUser("owner@example.com", domain.UserSignupPending)
	org := testOrg("Acme", now)
	created, err := store.CreateSignupUser(ctx, user, &org, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("sign-up with an organization was not created")
	}
	storedOrg, err := store.GetOrg(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOrg.Name != "Acme" {
		t.Fatalf("organization = %+v", storedOrg)
	}
	membership, err := store.GetMembership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive || membership.Role != domain.RoleOwner {
		t.Fatalf("membership = %q/%q, want active owner", membership.Status, membership.Role)
	}

	// A failure after the user insert rolls the account back with the
	// organization write.
	failing := signupUser("rollback@example.com", domain.UserSignupPending)
	badOrg := domain.Organization{ID: "not-a-uuid", Name: "Bad", Slug: "bad-org", CreatedAt: now, UpdatedAt: now}
	if _, err := store.CreateSignupUser(ctx, failing, &badOrg, nil, now); err == nil {
		t.Fatal("invalid organization id was accepted")
	}
	if _, err := store.FindByEmail(ctx, "rollback@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rollback user err = %v, want no rows", err)
	}
}

func TestSignupUserAcceptsInvitationAtomically(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	inviter, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org := testOrg("Acme", now)
	if err := store.CreateOrg(ctx, org, inviter.ID); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "new@example.com", "signup-invite-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}

	user := signupUser("new@example.com", domain.UserActive)
	user.EmailVerified = true
	created, err := store.CreateSignupUser(ctx, user, nil, &invite, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("invited sign-up was not created")
	}
	found, err := store.FindByEmail(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.UserActive || !found.EmailVerified {
		t.Fatalf("invited user = %+v", found)
	}
	accepted, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("signup-invite-token"))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", accepted.Status)
	}
	membership, err := store.GetMembership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}
}
