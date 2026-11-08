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

const signupPhone = "+989121230001"

func countOutbox(t *testing.T, store *Store, subject string) int {
	t.Helper()
	var count int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE subject = $1`, subject).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func phoneSignupUser(mobile string) domain.User {
	return domain.User{
		ID:           uuid.NewString(),
		Mobile:       mobile,
		PasswordHash: "hash",
		DisplayName:  "Sara",
		Status:       domain.UserActive,
	}
}

func TestPhoneInviteSignupCreatesUnverifiedMemberAtomically(t *testing.T) {
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
	invite := pendingPhoneInvite(org.ID, inviter.ID, signupPhone, domain.RoleAdmin, "phone-signup-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}

	usersBefore := countOutbox(t, store, domain.SubjectUserCreated)
	membershipsBefore := countOutbox(t, store, domain.SubjectMembershipAdded)
	user := phoneSignupUser(signupPhone)
	created, err := store.CreatePhoneInviteSignup(ctx, user, invite, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("phone invite sign-up was not created")
	}

	found, err := store.FindByMobile(ctx, signupPhone)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID || found.Status != domain.UserActive || found.Email != "" || found.DisplayName != "Sara" {
		t.Fatalf("created user = %+v", found)
	}
	if found.MobileVerified {
		t.Fatal("invite sign-up verified the mobile number without an OTP")
	}
	membership, err := store.GetMembership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive || membership.Role != domain.RoleAdmin {
		t.Fatalf("membership = %+v, want active admin", membership)
	}
	accepted, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("phone-signup-token"))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", accepted.Status)
	}
	if got := countOutbox(t, store, domain.SubjectUserCreated) - usersBefore; got != 1 {
		t.Fatalf("user.created events = %d, want 1", got)
	}
	if got := countOutbox(t, store, domain.SubjectMembershipAdded) - membershipsBefore; got != 1 {
		t.Fatalf("membership.added events = %d, want 1", got)
	}
}

func TestPhoneInviteSignupRollsBackWhenInviteIsNotPending(t *testing.T) {
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
	// The invitation is never written, so the accept step fails after the user
	// row was inserted in the same transaction.
	invite := pendingPhoneInvite(org.ID, inviter.ID, signupPhone, domain.RoleMember, "missing-token")

	usersBefore := countOutbox(t, store, domain.SubjectUserCreated)
	user := phoneSignupUser(signupPhone)
	created, err := store.CreatePhoneInviteSignup(ctx, user, invite, now)
	if !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("err = %v, want ErrInviteNotFound", err)
	}
	if created {
		t.Fatal("created = true after a failed accept")
	}
	if _, err := store.FindByMobile(ctx, signupPhone); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("user after rollback: err = %v, want no row", err)
	}
	if got := countOutbox(t, store, domain.SubjectUserCreated) - usersBefore; got != 0 {
		t.Fatalf("user.created events after rollback = %d, want 0", got)
	}
}

func TestPhoneInviteSignupExistingMobileIsNoOp(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	existing, err := store.FindOrCreateByMobile(ctx, signupPhone)
	if err != nil {
		t.Fatal(err)
	}
	inviter, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org := testOrg("Acme", now)
	if err := store.CreateOrg(ctx, org, inviter.ID); err != nil {
		t.Fatal(err)
	}
	invite := pendingPhoneInvite(org.ID, inviter.ID, signupPhone, domain.RoleMember, "existing-number-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreatePhoneInviteSignup(ctx, phoneSignupUser(signupPhone), invite, now)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("a number that already has an account was created again")
	}
	found, err := store.FindByMobile(ctx, signupPhone)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != existing.ID {
		t.Fatalf("mobile now belongs to %q, want the existing %q", found.ID, existing.ID)
	}
	still, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("existing-number-token"))
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", still.Status)
	}
}

func TestMarkMobileVerifiedKeepsFirstTimestamp(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	user, err := store.FindOrCreateByMobile(ctx, "+989121230002")
	if err != nil {
		t.Fatal(err)
	}
	if user.MobileVerified {
		t.Fatal("a new OTP-created account started verified before its OTP was checked")
	}
	first := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	if err := store.MarkMobileVerified(ctx, user.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMobileVerified(ctx, user.ID, first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var verifiedAt time.Time
	if err := db.Pool.QueryRow(ctx,
		`SELECT mobile_verified_at FROM users WHERE id = $1`, user.ID).Scan(&verifiedAt); err != nil {
		t.Fatal(err)
	}
	if !verifiedAt.Equal(first) {
		t.Fatalf("mobile_verified_at = %v, want the first verification %v", verifiedAt, first)
	}
	found, err := store.FindByMobile(ctx, "+989121230002")
	if err != nil {
		t.Fatal(err)
	}
	if !found.MobileVerified {
		t.Fatal("FindByMobile did not report the verified number")
	}
	if err := store.MarkMobileVerified(ctx, uuid.NewString(), first); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user err = %v, want ErrUserNotFound", err)
	}
}

func TestDemoSeededMobileCountsAsVerified(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	if err := store.UpsertDemoUser(ctx, "", "+989121230003", "hash", "Demo"); err != nil {
		t.Fatal(err)
	}
	found, err := store.FindByMobile(ctx, "+989121230003")
	if err != nil {
		t.Fatal(err)
	}
	if !found.MobileVerified {
		t.Fatal("a seeded demo mobile number was not marked verified")
	}
}
