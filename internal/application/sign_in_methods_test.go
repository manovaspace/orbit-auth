package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const (
	testOtherUserID = "22222222-2222-4222-8222-222222222222"
	testInviteToken = "invite-token-for-google"
)

// fakeAccount records the calls of the sign-in method store.
type fakeAccount struct {
	link        *domain.GoogleLink
	linkErr     error
	linkCalls   int
	linkedUser  string
	unlinkErr   error
	unlinkCalls int
	setCalls    int
	setKeep     string
	setFirst    bool
	setHash     string
	revokeCalls int
	revokeKeep  string
}

func (a *fakeAccount) GoogleLinkForUser(_ context.Context, _, _ string) (domain.GoogleLink, bool, error) {
	if a.link != nil {
		return *a.link, true, nil
	}
	return domain.GoogleLink{}, false, nil
}

func (a *fakeAccount) LinkGoogleIdentity(_ context.Context, _ string, id domain.GoogleIdentity, userID string, _ time.Time) (string, error) {
	a.linkCalls++
	a.linkedUser = userID
	if a.linkErr != nil {
		return "", a.linkErr
	}
	return domain.NormalizeEmail(id.Email), nil
}

func (a *fakeAccount) UnlinkGoogleIdentity(context.Context, string, string, domain.AuditActor, time.Time) error {
	a.unlinkCalls++
	return a.unlinkErr
}

func (a *fakeAccount) SetPassword(_ context.Context, _, hash, keep string, first bool, _ domain.AuditActor, _ time.Time) error {
	a.setCalls++
	a.setHash, a.setKeep, a.setFirst = hash, keep, first
	return nil
}

func (a *fakeAccount) RevokeSessions(_ context.Context, _, keep string, _ domain.AuditActor, _ time.Time) error {
	a.revokeCalls++
	a.revokeKeep = keep
	return nil
}

// twoUsers answers FindByID for a second account as well as the primary one.
type twoUsers struct {
	*fakeUsers
	others map[string]domain.User
}

func (u twoUsers) FindByID(ctx context.Context, userID string) (domain.User, error) {
	if user, ok := u.others[userID]; ok {
		return user, nil
	}
	return u.fakeUsers.FindByID(ctx, userID)
}

// accountHarness is a service with the sign-in method store and a refresh store.
func accountHarness(t *testing.T, user domain.User) (*Service, *fakeAccount, *fakeRefreshStore) {
	t.Helper()
	acc := &fakeAccount{}
	refresh := newFakeRefreshStore()
	svc := NewService(Deps{
		Users:   &fakeUsers{user: user},
		Refresh: refresh,
		Account: acc,
		Hasher:  testHasher{},
		IDs:     testUUIDs{},
		Clock:   fixedClock{at: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)},
		Secrets: testSecrets{},
		Logger:  func(string, ...any) {},
	})
	return svc, acc, refresh
}

// passwordUser has a password and a username, so it has a usable identifier.
func signInMethodsPasswordUser(t *testing.T) domain.User {
	t.Helper()
	hash, err := testHasher{}.Hash("current-password")
	if err != nil {
		t.Fatal(err)
	}
	return domain.User{ID: testGoogleUserID, Username: "ali", PasswordHash: hash, Status: domain.UserActive}
}

func storeRefresh(t *testing.T, refresh *fakeRefreshStore, userID, raw string) string {
	t.Helper()
	hash := domain.HashRefreshToken(raw)
	if err := refresh.Store(context.Background(), userID, "", "", hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestSetPasswordWrongCurrentPasswordChangesNothing(t *testing.T) {
	svc, acc, _ := accountHarness(t, signInMethodsPasswordUser(t))
	err := svc.SetPassword(context.Background(), testGoogleUserID, "wrong-password", "brand-new-password", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if acc.setCalls != 0 {
		t.Fatal("password changed after a wrong current password")
	}
}

func TestSetPasswordWithoutAPasswordIgnoresCurrentAndKeepsThePresentedSession(t *testing.T) {
	user := domain.User{ID: testGoogleUserID, Username: "ali", Status: domain.UserActive}
	svc, acc, refresh := accountHarness(t, user)
	keep := storeRefresh(t, refresh, testGoogleUserID, "raw-keep")
	storeRefresh(t, refresh, testGoogleUserID, "raw-other")

	if err := svc.SetPassword(context.Background(), testGoogleUserID, "", "brand-new-password", "raw-keep"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if acc.setCalls != 1 || !acc.setFirst || acc.setKeep != keep {
		t.Fatalf("store got calls=%d first=%v keep=%q, want one first call keeping the presented session", acc.setCalls, acc.setFirst, acc.setKeep)
	}
}

func TestSetPasswordRefusesShortPasswordAndUnusableAccount(t *testing.T) {
	svc, acc, _ := accountHarness(t, signInMethodsPasswordUser(t))
	if err := svc.SetPassword(context.Background(), testGoogleUserID, "current-password", "  short  ", ""); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("short password err = %v, want ErrPasswordTooShort", err)
	}

	// Unverified email only: no verified email, mobile or username.
	bare := domain.User{ID: testGoogleUserID, Email: "ali@example.com", Status: domain.UserActive}
	svc, acc2, _ := accountHarness(t, bare)
	if err := svc.SetPassword(context.Background(), testGoogleUserID, "", "brand-new-password", ""); !errors.Is(err, domain.ErrNoPasswordIdentifier) {
		t.Fatalf("no identifier err = %v, want ErrNoPasswordIdentifier", err)
	}
	if acc.setCalls != 0 || acc2.setCalls != 0 {
		t.Fatal("password written despite a refusal")
	}
}

func TestSetPasswordRefusesAForeignRefreshToken(t *testing.T) {
	svc, acc, refresh := accountHarness(t, signInMethodsPasswordUser(t))
	storeRefresh(t, refresh, testOtherUserID, "raw-foreign")

	err := svc.SetPassword(context.Background(), testGoogleUserID, "current-password", "brand-new-password", "raw-foreign")
	if !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("err = %v, want ErrInvalidRefresh", err)
	}
	if acc.setCalls != 0 {
		t.Fatal("password written with a foreign session")
	}
}

func TestRevokeAllSessionsKeepsOnlyThePresentedToken(t *testing.T) {
	svc, acc, refresh := accountHarness(t, signInMethodsPasswordUser(t))
	keep := storeRefresh(t, refresh, testGoogleUserID, "raw-keep")

	if err := svc.RevokeAllSessions(context.Background(), testGoogleUserID, "raw-keep"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if acc.revokeCalls != 1 || acc.revokeKeep != keep {
		t.Fatalf("revoke calls=%d keep=%q, want one call keeping the presented token", acc.revokeCalls, acc.revokeKeep)
	}

	if err := svc.RevokeAllSessions(context.Background(), testGoogleUserID, ""); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if acc.revokeKeep != "" {
		t.Fatalf("keep = %q without a presented token, want none", acc.revokeKeep)
	}
}

func TestRevokeAllSessionsRefusesAForeignToken(t *testing.T) {
	svc, acc, refresh := accountHarness(t, signInMethodsPasswordUser(t))
	storeRefresh(t, refresh, testOtherUserID, "raw-foreign")

	if err := svc.RevokeAllSessions(context.Background(), testGoogleUserID, "raw-foreign"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("err = %v, want ErrInvalidRefresh", err)
	}
	if acc.revokeCalls != 0 {
		t.Fatal("sessions revoked with a foreign token")
	}
}

func TestUnlinkGoogleChecksTheAccountFirstAndPassesTheStoreRefusal(t *testing.T) {
	disabled := signInMethodsPasswordUser(t)
	disabled.Status = domain.UserDisabled
	svc, acc, _ := accountHarness(t, disabled)
	if err := svc.UnlinkGoogle(context.Background(), testGoogleUserID); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("disabled err = %v, want ErrAccountNotActive", err)
	}
	if acc.unlinkCalls != 0 {
		t.Fatal("unlink reached the store for a disabled account")
	}

	svc, acc, _ = accountHarness(t, signInMethodsPasswordUser(t))
	acc.unlinkErr = domain.ErrLastSignInMethod
	if err := svc.UnlinkGoogle(context.Background(), testGoogleUserID); !errors.Is(err, domain.ErrLastSignInMethod) {
		t.Fatalf("err = %v, want ErrLastSignInMethod", err)
	}
}

func TestStartGoogleLinkRefusesAnAlreadyLinkedAccount(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	acc := &fakeAccount{link: &domain.GoogleLink{Email: "ali@example.com"}}
	h.svc.account = acc

	if _, _, err := h.svc.StartGoogleLink(context.Background(), testGoogleUserID, testGoogleRedirect, "/"); !errors.Is(err, domain.ErrGoogleAlreadyLinked) {
		t.Fatalf("err = %v, want ErrGoogleAlreadyLinked", err)
	}
	if h.store.stateCount() != 0 {
		t.Fatal("a refused link stored a state")
	}
}

func TestGoogleLinkStateIsBoundToItsAccountAndSingleUse(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	acc := &fakeAccount{}
	h.svc.account = acc
	h.svc.users = twoUsers{fakeUsers: &fakeUsers{user: activeGoogleUser()}, others: map[string]domain.User{
		testOtherUserID: {ID: testOtherUserID, Status: domain.UserActive},
	}}
	h.provider.identity = domain.GoogleIdentity{Subject: "sub-link", Email: "Ali@Example.com", EmailVerified: true}

	_, state, err := h.svc.StartGoogleLink(context.Background(), testGoogleUserID, testGoogleRedirect, "/settings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CompleteGoogleLink(context.Background(), testOtherUserID, state, "code"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("caller mismatch err = %v, want ErrInvalidGoogleState", err)
	}
	if acc.linkCalls != 0 {
		t.Fatal("identity linked for a caller that did not start the link")
	}

	// The mismatched attempt consumed the state, so the owner cannot reuse it.
	if _, err := h.svc.CompleteGoogleLink(context.Background(), testGoogleUserID, state, "code"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("replay err = %v, want ErrInvalidGoogleState", err)
	}

	_, state, err = h.svc.StartGoogleLink(context.Background(), testGoogleUserID, testGoogleRedirect, "/settings")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.CompleteGoogleLink(context.Background(), testGoogleUserID, state, "code")
	if err != nil {
		t.Fatalf("complete link: %v", err)
	}
	if res.ReturnTo != "/settings" || res.Email != "ali@example.com" || acc.linkedUser != testGoogleUserID {
		t.Fatalf("result = %+v, linked user = %q", res, acc.linkedUser)
	}
}

func TestGoogleStatePurposesAreNotInterchangeable(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	h.svc.account = &fakeAccount{}

	// A link state cannot be used to sign in.
	_, linkState, err := h.svc.StartGoogleLink(context.Background(), testGoogleUserID, testGoogleRedirect, "/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), linkState, "code"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("link state at sign-in err = %v, want ErrInvalidGoogleState", err)
	}
	if h.provider.exCalls != 0 {
		t.Fatal("the code was exchanged for a link state at sign-in")
	}

	// A sign-in state cannot be used to link.
	signInState := startSignIn(t, h)
	if _, err := h.svc.CompleteGoogleLink(context.Background(), testGoogleUserID, signInState, "code"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("sign-in state at link err = %v, want ErrInvalidGoogleState", err)
	}
}

// inviteHarness adds an organization and one pending invitation to h.
func inviteHarness(t *testing.T, h *googleHarness, phone bool) (domain.Organization, domain.Invite) {
	t.Helper()
	orgs := newFakeOrgStore()
	org := domain.Organization{ID: "33333333-3333-4333-8333-333333333333", Name: "Acme", Slug: "acme", Status: domain.OrgStatusActive}
	orgs.orgs[org.ID] = org
	h.svc.orgs = orgs
	h.svc.invites = newFakeInviteStore(orgs)

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	invite := domain.Invite{
		ID:        "44444444-4444-4444-8444-444444444444",
		OrgID:     org.ID,
		Role:      domain.RoleMember,
		TokenHash: domain.HashInviteToken(testInviteToken),
		InviterID: testOtherUserID,
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(domain.InviteTTL),
		CreatedAt: now,
	}
	if phone {
		invite.Phone = "+989121230002"
	} else {
		invite.Email = "ali@example.com"
	}
	if err := h.svc.invites.CreateInvite(context.Background(), invite, domain.SystemAuditActor()); err != nil {
		t.Fatal(err)
	}
	return org, invite
}

func TestStartGoogleSignInWithInviteRefusesUnknownAndPhoneInvites(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	inviteHarness(t, h, false)
	if _, _, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", "no-such-token"); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("unknown token err = %v, want ErrInvalidInvite", err)
	}

	h = newGoogleHarness(t, activeGoogleUser(), false)
	inviteHarness(t, h, true)
	if _, _, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", testInviteToken); !errors.Is(err, domain.ErrInviteNeedsPassword) {
		t.Fatalf("phone invite err = %v, want ErrInviteNeedsPassword", err)
	}
	if h.store.stateCount() != 0 {
		t.Fatal("a refused invite start stored a state")
	}
}

func TestStartGoogleSignInWithInviteStoresTheInviteIDNotTheToken(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	_, invite := inviteHarness(t, h, false)

	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", testInviteToken)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := h.store.states[googleStateHash(state)]
	if !ok || st.Purpose != domain.OAuthPurposeInvite || st.InviteID != invite.ID {
		t.Fatalf("stored state = %+v, want the invite purpose and id", st)
	}
	for hash, stored := range h.store.states {
		if stored.InviteID == testInviteToken || hash == testInviteToken {
			t.Fatal("the raw invite token was stored")
		}
	}
}

func TestInviteSignInIssuesTokensForTheInvitedOrganization(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	org, invite := inviteHarness(t, h, false)
	h.store.acceptResult = domain.InviteAcceptance{
		User:  domain.User{ID: testGoogleUserID, Status: domain.UserActive},
		OrgID: org.ID,
		Role:  domain.RoleMember,
	}
	h.provider.identity = domain.GoogleIdentity{Subject: "sub-invite", Email: "ali@example.com", EmailVerified: true}
	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/inbox", testInviteToken)
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if h.store.acceptCalls != 1 || h.store.acceptInviteID != invite.ID || !h.store.acceptAllowSignup {
		t.Fatalf("accept calls=%d invite=%q allowSignup=%v", h.store.acceptCalls, h.store.acceptInviteID, h.store.acceptAllowSignup)
	}
	if res.InviteID != invite.ID || res.OrgID != org.ID || res.OrgSlug != "acme" || res.Role != domain.RoleMember || res.ReturnTo != "/inbox" {
		t.Fatalf("result = %+v", res)
	}
	if _, orgID, err := h.refresh.FindValid(context.Background(), domain.HashRefreshToken(res.Refresh)); err != nil || orgID != org.ID {
		t.Fatalf("refresh org = %q, err = %v; want the invited organization", orgID, err)
	}
}

func TestInviteSignInSuspendedOrganizationGetsTokensWithoutOrg(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	org, _ := inviteHarness(t, h, false)
	suspended := h.svc.orgs.(*fakeOrgStore).orgs[org.ID]
	suspended.Status = domain.OrgStatusSuspended
	h.svc.orgs.(*fakeOrgStore).orgs[org.ID] = suspended
	h.store.acceptResult = domain.InviteAcceptance{User: domain.User{ID: testGoogleUserID, Status: domain.UserActive}, OrgID: org.ID, Role: domain.RoleMember}
	h.provider.identity = domain.GoogleIdentity{Subject: "sub-s", Email: "ali@example.com", EmailVerified: true}
	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", testInviteToken)
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, orgID, err := h.refresh.FindValid(context.Background(), domain.HashRefreshToken(res.Refresh)); err != nil || orgID != "" {
		t.Fatalf("refresh org = %q, err = %v; want no organization", orgID, err)
	}
}

func TestInviteSignInErrorsFromTheStore(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    error
		signOff bool
	}{
		{"not found becomes invalid invite", domain.ErrInviteNotFound, domain.ErrInvalidInvite, false},
		{"email mismatch passes through", domain.ErrInviteEmailMismatch, domain.ErrInviteEmailMismatch, false},
		{"sign-up off passes through", domain.ErrInviteSignupDisabled, domain.ErrInviteSignupDisabled, false},
		{"not allowed passes through", domain.ErrGoogleAccountNotAllowed, domain.ErrGoogleAccountNotAllowed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newGoogleHarness(t, activeGoogleUser(), false)
			inviteHarness(t, h, false)
			h.store.acceptErr = tc.err
			h.provider.identity = domain.GoogleIdentity{Subject: "sub-e", Email: "ali@example.com", EmailVerified: true}
			_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", testInviteToken)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestInviteSignInRefusesANonActiveAccount(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	org, _ := inviteHarness(t, h, false)
	h.store.acceptResult = domain.InviteAcceptance{User: domain.User{ID: testGoogleUserID, Status: domain.UserDisabled}, OrgID: org.ID, Role: domain.RoleMember}
	h.provider.identity = domain.GoogleIdentity{Subject: "sub-d", Email: "ali@example.com", EmailVerified: true}
	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", testInviteToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("err = %v, want ErrGoogleAccountNotAllowed", err)
	}
}

func TestInviteSignInPassesTheSignupSwitchToTheStore(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	h.svc.inviteSignupOff = true
	org, _ := inviteHarness(t, h, false)
	h.store.acceptErr = domain.ErrInviteSignupDisabled
	h.provider.identity = domain.GoogleIdentity{Subject: "sub-off", Email: "ali@example.com", EmailVerified: true}
	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", testInviteToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrInviteSignupDisabled) {
		t.Fatalf("err = %v, want ErrInviteSignupDisabled", err)
	}
	if h.store.acceptAllowSignup {
		t.Fatal("store told that sign-up is allowed while it is off")
	}
	_ = org
}
