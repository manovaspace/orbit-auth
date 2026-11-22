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
	if err := store.CreateInvite(ctx, invite, domain.SystemAuditActor()); err != nil {
		t.Fatal(err)
	}

	usersBefore := countOutbox(t, store, domain.SubjectUserCreated)
	membershipsBefore := countOutbox(t, store, domain.SubjectMembershipAdded)
	user := phoneSignupUser(signupPhone)
	_, created, err := store.CreateInviteSignup(ctx, user, invite, now)
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
	_, created, err := store.CreateInviteSignup(ctx, user, invite, now)
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
	if err := store.CreateInvite(ctx, invite, domain.SystemAuditActor()); err != nil {
		t.Fatal(err)
	}

	_, created, err := store.CreateInviteSignup(ctx, phoneSignupUser(signupPhone), invite, now)
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

	if err := store.UpsertDemoUser(ctx, "", "+989121230003", "", "hash", "Demo"); err != nil {
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

// emailSignupUser is the account an email invitation writes. The application
// sets the status; the store sets the verification time.
func emailSignupUser(email string) domain.User {
	return domain.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: "new-hash",
		DisplayName:  "Sara",
		Status:       domain.UserActive,
	}
}

// emailInviteFixtureStore returns a store with one organization owned by the
// "owner@example.com" account and one pending email invitation to email.
func emailInviteFixtureStore(t *testing.T, email, token string) (*Store, domain.Organization, domain.Invite, time.Time) {
	t.Helper()
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
	invite := pendingInvite(org.ID, inviter.ID, email, token)
	if err := store.CreateInvite(ctx, invite, domain.SystemAuditActor()); err != nil {
		t.Fatal(err)
	}
	return store, org, invite, now
}

func TestEmailInviteSignupCreatesVerifiedActiveMemberAtomically(t *testing.T) {
	store, org, invite, now := emailInviteFixtureStore(t, "member@example.com", "email-signup-token")
	ctx := context.Background()

	usersBefore := countOutbox(t, store, domain.SubjectUserCreated)
	membershipsBefore := countOutbox(t, store, domain.SubjectMembershipAdded)
	user := emailSignupUser("member@example.com")
	holderID, created, err := store.CreateInviteSignup(ctx, user, invite, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created || holderID != user.ID {
		t.Fatalf("holder = %q created = %v, want the new account", holderID, created)
	}

	found, err := store.FindByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != user.ID || found.Status != domain.UserActive || !found.EmailVerified || found.Mobile != "" || found.DisplayName != "Sara" {
		t.Fatalf("created user = %+v", found)
	}
	membership, err := store.GetMembership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive || membership.Role != domain.RoleMember {
		t.Fatalf("membership = %+v, want active member", membership)
	}
	accepted, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("email-signup-token"))
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

func TestEmailInviteSignupTakesOverPendingAccount(t *testing.T) {
	store, org, invite, now := emailInviteFixtureStore(t, "member@example.com", "takeover-token")
	ctx := context.Background()

	pendingID := uuid.NewString()
	if created, err := store.CreateSignupUser(ctx, domain.User{
		ID: pendingID, Email: "member@example.com", PasswordHash: "old-hash", DisplayName: "Old", Status: domain.UserSignupPending,
	}, nil, nil, now); err != nil || !created {
		t.Fatalf("pending account: created=%v err=%v", created, err)
	}
	usersBefore := countOutbox(t, store, domain.SubjectUserCreated)

	holderID, created, err := store.CreateInviteSignup(ctx, emailSignupUser("member@example.com"), invite, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created || holderID != pendingID {
		t.Fatalf("holder = %q created = %v, want the pending account %q", holderID, created, pendingID)
	}
	found, err := store.FindByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != pendingID || found.Status != domain.UserActive || !found.EmailVerified || found.DisplayName != "Sara" {
		t.Fatalf("taken-over account = %+v", found)
	}
	var hash string
	if err := store.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, pendingID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "new-hash" {
		t.Fatal("takeover kept the pending password hash")
	}
	if got := countOutbox(t, store, domain.SubjectUserCreated) - usersBefore; got != 0 {
		t.Fatalf("user.created events on takeover = %d, want 0", got)
	}
	membership, err := store.GetMembership(ctx, org.ID, pendingID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %+v, want active", membership)
	}
}

func TestEmailInviteSignupRefusesActiveAccount(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "active-email-token")
	ctx := context.Background()

	existingID := uuid.NewString()
	if created, err := store.CreateSignupUser(ctx, domain.User{
		ID: existingID, Email: "member@example.com", PasswordHash: "hash", DisplayName: "Existing", Status: domain.UserActive, EmailVerified: true,
	}, nil, nil, now); err != nil || !created {
		t.Fatalf("existing account: created=%v err=%v", created, err)
	}

	holderID, created, err := store.CreateInviteSignup(ctx, emailSignupUser("member@example.com"), invite, now)
	if err != nil {
		t.Fatal(err)
	}
	if created || holderID != "" {
		t.Fatalf("holder = %q created = %v, want nothing written", holderID, created)
	}
	found, err := store.FindByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != existingID || found.DisplayName != "Existing" {
		t.Fatalf("active account changed: %+v", found)
	}
	still, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("active-email-token"))
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", still.Status)
	}
}

func TestEmailInviteSignupRollsBackTakeoverWhenInviteIsNotPending(t *testing.T) {
	store, org, _, now := emailInviteFixtureStore(t, "member@example.com", "unwritten-token")
	ctx := context.Background()

	pendingID := uuid.NewString()
	if created, err := store.CreateSignupUser(ctx, domain.User{
		ID: pendingID, Email: "member@example.com", PasswordHash: "old-hash", DisplayName: "Old", Status: domain.UserSignupPending,
	}, nil, nil, now); err != nil || !created {
		t.Fatalf("pending account: created=%v err=%v", created, err)
	}
	// The invitation is never written, so the accept step fails after the
	// takeover ran in the same transaction.
	missing := pendingInvite(org.ID, pendingID, "member@example.com", "missing-email-token")

	_, created, err := store.CreateInviteSignup(ctx, emailSignupUser("member@example.com"), missing, now)
	if !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("err = %v, want ErrInviteNotFound", err)
	}
	if created {
		t.Fatal("created = true after a failed accept")
	}
	found, err := store.FindByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.UserSignupPending || found.EmailVerified || found.DisplayName != "Old" {
		t.Fatalf("pending account changed after rollback: %+v", found)
	}
}
