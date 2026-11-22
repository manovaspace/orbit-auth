package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const (
	acceptHost     = "app.example.com"
	acceptURLTmpl  = "https://{slug}.app.example.com/invite"
	invitedEmail   = "member@example.com"
	ownerUserID    = "owner"
	invitePassword = "correct horse"
)

// acceptService builds a service over store with the accept hosts and the
// invite sign-up switch set, and records every log line in logs.
func acceptService(store *signupStore, notifier *fakeNotifier, hosts []string, inviteOff bool, logs *[]string) *Service {
	return NewService(Deps{
		OTP:                  store.otp,
		Users:                store,
		Refresh:              newFakeRefreshStore(),
		Tokens:               fakeApiTokens{},
		Orgs:                 store.orgs,
		Invites:              store.invites,
		Signup:               store,
		Notifier:             notifier,
		Flags:                fakeFlags{email: true},
		Clock:                fixedClock{at: signupNow()},
		IDs:                  testUUIDs{},
		Signer:               testSigner{},
		Hasher:               testHasher{},
		Codes:                testCodes{},
		Secrets:              testSecrets{},
		InviteSignupDisabled: inviteOff,
		InviteAcceptHosts:    hosts,
		AutoSelectSingleOrg:  true,
		Logger: func(msg string, args ...any) {
			if logs != nil {
				*logs = append(*logs, msg+" "+strings.Join(anyStrings(args), " "))
			}
		},
	})
}

func anyStrings(args []any) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if s, ok := arg.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// emailInviteFixture creates an organization owned by ownerUserID and one
// pending email invitation to invitedEmail. It returns the org and the raw token
// the org_invite email carried.
func emailInviteFixture(t *testing.T, svc *Service, notifier *fakeNotifier, store *signupStore) (domain.Organization, domain.Invite, string) {
	t.Helper()
	ctx := context.Background()
	org, err := svc.CreateOrg(ctx, ownerUserID, "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	invite, _, err := svc.InviteMember(ctx, org.ID, ownerUserID, domain.InviteTarget{Email: invitedEmail}, domain.RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}
	return org, invite, notifier.invite.AcceptToken
}

func TestEmailInviteCarriesAcceptLinkWithTokenInFragment(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	var logs []string
	svc := acceptService(store, notifier, []string{acceptHost}, false, &logs)
	ctx := context.Background()
	org, err := svc.CreateOrg(ctx, ownerUserID, "Acme", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.InviteMember(ctx, org.ID, ownerUserID, domain.InviteTarget{Email: invitedEmail}, domain.RoleMember, acceptURLTmpl); err != nil {
		t.Fatal(err)
	}
	token := notifier.invite.AcceptToken
	if token == "" {
		t.Fatal("no token was sent")
	}
	want := "https://" + org.Slug + ".app.example.com/invite#" + token
	if notifier.invite.AcceptURL != want {
		t.Fatalf("accept url = %q, want %q", notifier.invite.AcceptURL, want)
	}
	for _, line := range logs {
		if strings.Contains(line, token) {
			t.Fatalf("log line carries the raw token: %q", line)
		}
	}
}

func TestEmailInviteWithoutAcceptURLSendsNoLink(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := acceptService(store, notifier, []string{acceptHost}, false, nil)

	emailInviteFixture(t, svc, notifier, store)
	if notifier.invite.AcceptURL != "" {
		t.Fatalf("accept url = %q, want none", notifier.invite.AcceptURL)
	}
}

func TestInvalidAcceptURLWritesNothing(t *testing.T) {
	cases := map[string]struct {
		hosts     []string
		acceptURL string
		target    domain.InviteTarget
	}{
		"wrong host email":          {[]string{acceptHost}, "https://evil.example.com/invite", domain.InviteTarget{Email: invitedEmail}},
		"suffix trick email":        {[]string{acceptHost}, "https://evil" + acceptHost + "/invite", domain.InviteTarget{Email: invitedEmail}},
		"no hosts configured email": {nil, acceptURLTmpl, domain.InviteTarget{Email: invitedEmail}},
		"userinfo email":            {[]string{acceptHost}, "https://user@" + acceptHost + "/invite", domain.InviteTarget{Email: invitedEmail}},
		"wrong host phone":          {[]string{acceptHost}, "https://evil.example.com/invite", domain.InviteTarget{Phone: invitedMobile}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newSignupStore()
			store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
			notifier := &fakeNotifier{}
			svc := acceptService(store, notifier, tc.hosts, false, nil)
			ctx := context.Background()
			org, err := svc.CreateOrg(ctx, ownerUserID, "Acme", "")
			if err != nil {
				t.Fatal(err)
			}
			before := len(store.invites.invites)

			_, _, err = svc.InviteMember(ctx, org.ID, ownerUserID, tc.target, domain.RoleMember, tc.acceptURL)
			if !errors.Is(err, domain.ErrInvalidAcceptURL) {
				t.Fatalf("err = %v, want ErrInvalidAcceptURL", err)
			}
			if len(store.invites.invites) != before || notifier.inviteCalls != 0 {
				t.Fatal("an invalid accept_url wrote an invitation or sent a message")
			}
		})
	}
}

func TestEmailSignupCreatesVerifiedActiveAccount(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
	org, invite, token := emailInviteFixture(t, svc, notifier, store)
	ctx := context.Background()

	result, err := svc.AcceptInviteWithSignup(ctx, token, invitePassword, "  Sara  ")
	if err != nil {
		t.Fatal(err)
	}
	if result.UserID == "" || result.OrgID != org.ID || result.InviteID != invite.ID ||
		result.Role != domain.RoleMember || result.Identifier != invitedEmail {
		t.Fatalf("result = %+v", result)
	}
	user := store.user(t, invitedEmail)
	if user.ID != result.UserID || user.Status != domain.UserActive || !user.EmailVerified || user.DisplayName != "Sara" {
		t.Fatalf("user = %+v", user)
	}
	if user.Mobile != "" || user.MobileVerified {
		t.Fatalf("email sign-up set a mobile: %+v", user)
	}
	if user.PasswordHash == "" || user.PasswordHash == invitePassword {
		t.Fatal("password was not stored as a hash")
	}
	membership, err := store.orgs.GetMembership(ctx, org.ID, result.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive || membership.Role != domain.RoleMember {
		t.Fatalf("membership = %+v", membership)
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", got.Status)
	}
}

func TestEmailSignupRefusesActiveAccountAndWritesNothing(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	store.add(domain.User{ID: "existing", Email: invitedEmail, Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
	_, invite, token := emailInviteFixture(t, svc, notifier, store)

	_, err := svc.AcceptInviteWithSignup(context.Background(), token, invitePassword, "Sara")
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("err = %v, want ErrAccountExists", err)
	}
	if store.phoneCalls != 0 {
		t.Fatal("an active account reached the sign-up transaction")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", got.Status)
	}
	if user := store.user(t, invitedEmail); user.ID != "existing" || user.DisplayName != "" {
		t.Fatalf("existing account changed: %+v", user)
	}
}

func TestEmailSignupTakesOverPendingAccount(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	store.add(domain.User{ID: "pending-1", Email: invitedEmail, PasswordHash: "old-hash", DisplayName: "Old", Status: domain.UserSignupPending})
	notifier := &fakeNotifier{}
	svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
	_, invite, token := emailInviteFixture(t, svc, notifier, store)
	usersBefore := len(store.users)

	result, err := svc.AcceptInviteWithSignup(context.Background(), token, invitePassword, "Sara")
	if err != nil {
		t.Fatal(err)
	}
	if result.UserID != "pending-1" {
		t.Fatalf("user id = %q, want the pending account", result.UserID)
	}
	user := store.user(t, invitedEmail)
	if user.Status != domain.UserActive || !user.EmailVerified || user.DisplayName != "Sara" || user.PasswordHash == "old-hash" {
		t.Fatalf("pending account not taken over: %+v", user)
	}
	if len(store.users) != usersBefore {
		t.Fatal("takeover created a second account row")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", got.Status)
	}
}

func TestEmailSignupShortPasswordWritesNothing(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
	_, invite, token := emailInviteFixture(t, svc, notifier, store)

	_, err := svc.AcceptInviteWithSignup(context.Background(), token, "1234567", "Sara")
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("err = %v, want ErrPasswordTooShort", err)
	}
	if store.phoneCalls != 0 {
		t.Fatal("a short password reached the transaction")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", got.Status)
	}
}

func TestEmailSignupSwitchOffRefusesAndWritesNothing(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
	_, _, token := emailInviteFixture(t, svc, notifier, store)
	off := acceptService(store, notifier, []string{acceptHost}, true, nil)

	_, err := off.AcceptInviteWithSignup(context.Background(), token, invitePassword, "Sara")
	if !errors.Is(err, ErrInviteSignupDisabled) {
		t.Fatalf("err = %v, want ErrInviteSignupDisabled", err)
	}
	if store.phoneCalls != 0 {
		t.Fatal("disabled invite sign-up reached the transaction")
	}
}

// lookupFailure is a user store whose email lookup fails with a database error.
type lookupFailure struct{ *signupStore }

func (lookupFailure) FindByEmail(context.Context, string) (domain.User, error) {
	return domain.User{}, errors.New("database down")
}

func TestGetInvitePreviewChannelAndAccountExists(t *testing.T) {
	ctx := context.Background()

	t.Run("phone without account", func(t *testing.T) {
		svc, _, _, _, org := inviteFixture(t)
		_, token, err := svc.InviteMember(ctx, org.ID, "inviter", domain.InviteTarget{Phone: testMobile}, domain.RoleMember, "")
		if err != nil {
			t.Fatal(err)
		}
		preview, err := svc.GetInvitePreview(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if preview.Channel != "phone" || preview.AccountExists {
			t.Fatalf("preview = %+v", preview)
		}
	})

	t.Run("phone with active account", func(t *testing.T) {
		store := newSignupStore()
		store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
		store.add(domain.User{ID: "phone-user", Mobile: invitedMobile, Status: domain.UserActive})
		notifier := &fakeNotifier{}
		svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
		org, err := svc.CreateOrg(ctx, ownerUserID, "Acme", "")
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := svc.InviteMember(ctx, org.ID, ownerUserID, domain.InviteTarget{Phone: invitedMobile}, domain.RoleMember, "")
		if err != nil {
			t.Fatal(err)
		}
		preview, err := svc.GetInvitePreview(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if preview.Channel != "phone" || !preview.AccountExists {
			t.Fatalf("preview = %+v", preview)
		}
	})

	t.Run("email channel and account states", func(t *testing.T) {
		store := newSignupStore()
		store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
		notifier := &fakeNotifier{}
		svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
		_, _, token := emailInviteFixture(t, svc, notifier, store)

		preview, err := svc.GetInvitePreview(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if preview.Channel != "email" || preview.AccountExists {
			t.Fatalf("no account: preview = %+v", preview)
		}

		store.add(domain.User{ID: "pending-1", Email: invitedEmail, Status: domain.UserSignupPending})
		preview, err = svc.GetInvitePreview(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if preview.AccountExists {
			t.Fatal("a pending sign-up is not an active account")
		}

		store.add(domain.User{ID: "pending-1", Email: invitedEmail, Status: domain.UserActive, EmailVerified: true})
		preview, err = svc.GetInvitePreview(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if !preview.AccountExists {
			t.Fatal("an active account with the email was not reported")
		}
	})

	t.Run("other lookup error is returned", func(t *testing.T) {
		store := newSignupStore()
		store.add(domain.User{ID: ownerUserID, Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
		notifier := &fakeNotifier{}
		svc := acceptService(store, notifier, []string{acceptHost}, false, nil)
		_, _, token := emailInviteFixture(t, svc, notifier, store)
		failing := NewService(Deps{
			OTP: store.otp, Users: lookupFailure{store}, Refresh: newFakeRefreshStore(), Tokens: fakeApiTokens{},
			Orgs: store.orgs, Invites: store.invites, Signup: store, Notifier: notifier, Flags: fakeFlags{email: true},
			Clock: fixedClock{at: signupNow()}, IDs: testUUIDs{}, Signer: testSigner{}, Hasher: testHasher{},
			Codes: testCodes{}, Secrets: testSecrets{}, InviteAcceptHosts: []string{acceptHost},
			AutoSelectSingleOrg: true, Logger: func(string, ...any) {},
		})
		if _, err := failing.GetInvitePreview(ctx, token); err == nil || errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want the lookup error", err)
		}
	})
}

func TestStaffProvisionAcceptURLUsesAllocatedSlug(t *testing.T) {
	f := newStaffFixtureWithHosts(t, []string{acceptHost})
	result, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "Acme",
		IdempotencyKey: "key-accept-1",
		Target:         domain.InviteTarget{Email: "owner@example.com"},
		AcceptURL:      acceptURLTmpl,
	})
	if err != nil {
		t.Fatal(err)
	}
	slug := result.Summary.Organization.Slug
	want := "https://" + slug + ".app.example.com/invite#" + f.notifier.invite.AcceptToken
	if f.notifier.invite.AcceptURL != want {
		t.Fatalf("accept url = %q, want %q", f.notifier.invite.AcceptURL, want)
	}
}

func TestStaffProvisionInvalidAcceptURLWritesNothing(t *testing.T) {
	f := newStaffFixtureWithHosts(t, []string{acceptHost})
	_, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "Acme",
		IdempotencyKey: "key-accept-bad",
		Target:         domain.InviteTarget{Email: "owner@example.com"},
		AcceptURL:      "https://evil.example.com/invite",
	})
	if !errors.Is(err, domain.ErrInvalidAcceptURL) {
		t.Fatalf("err = %v, want ErrInvalidAcceptURL", err)
	}
	if len(f.orgs.orgs) != 0 || len(f.invites.invites) != 0 || f.notifier.inviteCalls != 0 {
		t.Fatal("an invalid accept_url wrote an organization, an invitation or a message")
	}
}

func TestStaffReinviteInvalidAcceptURLWritesNothing(t *testing.T) {
	f := newStaffFixtureWithHosts(t, []string{acceptHost})
	org := provisionPhone(t, f, "Acme", "key-reinvite-bad", "+989121110000")
	before := len(f.invites.invites)

	_, _, err := f.svc.StaffReinviteOwner(context.Background(), staffAlice, org.Summary.Organization.ID,
		domain.InviteTarget{Email: "next@example.com"}, "https://evil.example.com/invite")
	if !errors.Is(err, domain.ErrInvalidAcceptURL) {
		t.Fatalf("err = %v, want ErrInvalidAcceptURL", err)
	}
	if len(f.invites.invites) != before {
		t.Fatal("an invalid accept_url wrote an invitation")
	}
}
