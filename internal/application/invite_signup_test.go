package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const invitedMobile = "+989121234567"

// phoneInviteFixture creates an organization owned by "owner" and one pending
// phone invitation with role. The signup store is shared with the returned
// service so tests can inspect the rows it writes.
func phoneInviteFixture(t *testing.T, role domain.MembershipRole) (*Service, *signupStore, domain.Organization, domain.Invite, string) {
	t.Helper()
	store := newSignupStore()
	store.add(domain.User{ID: "owner", Email: "owner@example.com", DisplayName: "Owner", Status: domain.UserActive, EmailVerified: true})
	svc := newSignupService(t, store, &fakeNotifier{}, true)
	ctx := context.Background()
	org, err := svc.CreateOrg(ctx, "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	invite, token, err := svc.InviteMember(ctx, org.ID, "owner", domain.InviteTarget{Phone: invitedMobile}, role)
	if err != nil {
		t.Fatal(err)
	}
	return svc, store, org, invite, token
}

// userByMobile returns the stored account for mobile, or false.
func (s *signupStore) userByMobile(mobile string) (domain.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.Mobile == mobile {
			return user, true
		}
	}
	return domain.User{}, false
}

func TestAcceptInviteWithSignupCreatesActiveUnverifiedMember(t *testing.T) {
	svc, store, org, invite, token := phoneInviteFixture(t, domain.RoleAdmin)
	ctx := context.Background()

	userID, orgID, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "  Sara  ")
	if err != nil {
		t.Fatal(err)
	}
	if orgID != org.ID || userID == "" {
		t.Fatalf("ids = %q %q, want org %q and a user id", userID, orgID, org.ID)
	}

	user, ok := store.userByMobile(invitedMobile)
	if !ok {
		t.Fatal("no account was written for the invited number")
	}
	if user.ID != userID || user.Status != domain.UserActive || user.DisplayName != "Sara" {
		t.Fatalf("user = %+v", user)
	}
	if user.Email != "" {
		t.Fatalf("email = %q, want none for a phone sign-up", user.Email)
	}
	if user.PasswordHash == "" || user.PasswordHash == "correct horse" {
		t.Fatal("password was not stored as a hash")
	}
	if user.MobileVerified {
		t.Fatal("invite sign-up marked the mobile verified; only an OTP may")
	}

	membership, err := store.orgs.GetMembership(ctx, org.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive || membership.Role != domain.RoleAdmin {
		t.Fatalf("membership = %+v, want active admin", membership)
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", got.Status)
	}
	if store.phoneCalls != 1 {
		t.Fatalf("phone signup transactions = %d, want 1", store.phoneCalls)
	}
}

func TestAcceptInviteWithSignupExistingMobileNeedsSignIn(t *testing.T) {
	svc, store, _, invite, token := phoneInviteFixture(t, domain.RoleMember)
	store.add(domain.User{ID: "existing", Mobile: invitedMobile, Status: domain.UserActive})

	_, _, err := svc.AcceptInviteWithSignup(context.Background(), token, "correct horse", "Sara")
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("err = %v, want ErrAccountExists", err)
	}
	if store.phoneCalls != 0 {
		t.Fatal("an existing number reached the sign-up transaction")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want still pending for sign-in acceptance", got.Status)
	}
}

func TestAcceptInviteWithSignupRejectsEmailInvitation(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: "owner", Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	org, err := svc.CreateOrg(ctx, "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.InviteMember(ctx, org.ID, "owner", domain.InviteTarget{Email: "member@example.com"}, domain.RoleMember); err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.AcceptInviteWithSignup(ctx, notifier.invite.AcceptToken, "correct horse", "Sara")
	if !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite", err)
	}
	if store.phoneCalls != 0 || len(store.users) != 1 {
		t.Fatal("an email invitation wrote an account")
	}
}

func TestAcceptInviteWithSignupRejectsUnusableTokens(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(t *testing.T, svc *Service, store *signupStore, invite domain.Invite, org domain.Organization, token string) string{
		"unknown": func(*testing.T, *Service, *signupStore, domain.Invite, domain.Organization, string) string {
			return "not-a-token"
		},
		"expired": func(t *testing.T, _ *Service, store *signupStore, invite domain.Invite, _ domain.Organization, token string) string {
			store.invites.setExpiry(t, invite.ID, signupNow().Add(-time.Minute))
			return token
		},
		"revoked": func(t *testing.T, svc *Service, _ *signupStore, invite domain.Invite, org domain.Organization, token string) string {
			if err := svc.RevokeInvite(ctx, org.ID, invite.ID, "owner"); err != nil {
				t.Fatal(err)
			}
			return token
		},
		"used": func(t *testing.T, svc *Service, _ *signupStore, _ domain.Invite, _ domain.Organization, token string) string {
			if _, _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara"); err != nil {
				t.Fatal(err)
			}
			return token
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store, org, invite, token := phoneInviteFixture(t, domain.RoleMember)
			token = prepare(t, svc, store, invite, org, token)
			before := store.phoneCalls

			_, _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara")
			if !errors.Is(err, domain.ErrInvalidInvite) {
				t.Fatalf("err = %v, want ErrInvalidInvite", err)
			}
			if store.phoneCalls != before {
				t.Fatal("an unusable token reached the sign-up transaction")
			}
		})
	}
}

func TestAcceptInviteWithSignupRequiresPassword(t *testing.T) {
	svc, store, _, invite, token := phoneInviteFixture(t, domain.RoleMember)

	for _, password := range []string{"", "   "} {
		_, _, err := svc.AcceptInviteWithSignup(context.Background(), token, password, "Sara")
		if !errors.Is(err, ErrPasswordRequired) {
			t.Fatalf("password %q: err = %v, want ErrPasswordRequired", password, err)
		}
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("a rejected password wrote an account")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", got.Status)
	}
}

func TestAcceptInviteWithSignupRollsBackWhenInviteIsConsumedMidway(t *testing.T) {
	svc, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	store.invites.acceptErr = domain.ErrInviteNotFound

	_, _, err := svc.AcceptInviteWithSignup(context.Background(), token, "correct horse", "Sara")
	if !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite", err)
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("a failed invitation left the account behind")
	}
}

// newInviteSignupService builds a service over store with the sign-up switch
// and the invite sign-up switch set independently.
func newInviteSignupService(store *signupStore, signupOpen, inviteOff bool) *Service {
	return NewService(Deps{
		OTP:                  store.otp,
		Users:                store,
		Refresh:              newFakeRefreshStore(),
		Tokens:               fakeApiTokens{},
		Orgs:                 store.orgs,
		Invites:              store.invites,
		Signup:               store,
		Notifier:             &fakeNotifier{},
		Flags:                fakeFlags{email: true},
		Clock:                fixedClock{at: signupNow()},
		IDs:                  testUUIDs{},
		Signer:               testSigner{},
		Hasher:               testHasher{},
		Codes:                testCodes{},
		Secrets:              testSecrets{},
		SignupOpen:           signupOpen,
		InviteSignupDisabled: inviteOff,
		AutoSelectSingleOrg:  true,
		Logger:               func(string, ...any) {},
	})
}

func TestAcceptInviteWithSignupOnByDefaultWhileAuthSignupIsOff(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	svc := newInviteSignupService(store, false, false)

	if _, _, err := svc.AcceptInviteWithSignup(context.Background(), token, "correct horse", "Sara"); err != nil {
		t.Fatalf("invite sign-up with AUTH_SIGNUP off: %v", err)
	}
	if _, ok := store.userByMobile(invitedMobile); !ok {
		t.Fatal("invite sign-up wrote no account")
	}
}

func TestAcceptInviteWithSignupExplicitOffRefusesAndWritesNothing(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	svc := newInviteSignupService(store, true, true)

	_, _, err := svc.AcceptInviteWithSignup(context.Background(), token, "correct horse", "Sara")
	if !errors.Is(err, ErrInviteSignupDisabled) {
		t.Fatalf("err = %v, want ErrInviteSignupDisabled", err)
	}
	if store.phoneCalls != 0 {
		t.Fatal("disabled invite sign-up reached the transaction")
	}
}

func TestAcceptInviteWithSignupPasswordMinimumIsEightCharacters(t *testing.T) {
	svc, store, _, invite, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()

	for _, password := range []string{"1234567", "  1234567  ", "سلام"} {
		_, _, err := svc.AcceptInviteWithSignup(ctx, token, password, "Sara")
		if !errors.Is(err, ErrPasswordTooShort) {
			t.Fatalf("password %q: err = %v, want ErrPasswordTooShort", password, err)
		}
	}
	if store.phoneCalls != 0 {
		t.Fatal("a short password reached the transaction")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", got.Status)
	}

	// Eight Persian characters, more than eight bytes, are accepted.
	if _, _, err := svc.AcceptInviteWithSignup(ctx, token, "ابتثجحخد", "Sara"); err != nil {
		t.Fatalf("eight characters: %v", err)
	}
}

func TestGetInvitePreviewReportsGrantWithoutRecipient(t *testing.T) {
	svc, _, org, invite, token := phoneInviteFixture(t, domain.RoleAdmin)
	ctx := context.Background()

	preview, err := svc.GetInvitePreview(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if preview.OrgID != org.ID || preview.OrgName != "Acme" || preview.OrgSlug != org.Slug || preview.Role != domain.RoleAdmin {
		t.Fatalf("preview = %+v", preview)
	}

	if err := svc.RevokeInvite(ctx, org.ID, invite.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetInvitePreview(ctx, token); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("revoked preview err = %v, want ErrInvalidInvite", err)
	}
}

func TestVerifyOTPMobileMarksNumberVerified(t *testing.T) {
	code, err := testCodes{}.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := testHasher{}.Hash(code)
	if err != nil {
		t.Fatal(err)
	}
	otp := &fakeOTP{}
	if err := otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
		ID:         "ch-mobile-verified",
		Identifier: invitedMobile,
		Channel:    domain.ChannelSMS,
		CodeHash:   hash,
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	users := &fakeUsers{}
	svc := mustServiceWithRefresh(t, otp, users, newFakeRefreshStore(), &fakeNotifier{}, fakeFlags{mobile: true})

	if _, _, _, err := svc.VerifyOTP(context.Background(), invitedMobile, domain.ChannelSMS, code); err != nil {
		t.Fatal(err)
	}
	if !users.user.MobileVerified {
		t.Fatal("a successful mobile OTP did not mark the number verified")
	}
}
