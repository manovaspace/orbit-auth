package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// fakeInviteStore models the Postgres adapter: it keeps the same rows, replaces
// a pending invite for the same organization and email, and never returns a
// token hash from a listing.
type fakeInviteStore struct {
	mu        sync.Mutex
	invites   map[string]domain.Invite
	orgs      *fakeOrgStore
	createErr error
	acceptErr error
	revokeErr error
	listErr   error
	findErr   error
}

func newFakeInviteStore(orgs *fakeOrgStore) *fakeInviteStore {
	return &fakeInviteStore{invites: make(map[string]domain.Invite), orgs: orgs}
}

func (f *fakeInviteStore) CreateInvite(_ context.Context, invite domain.Invite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	for id, existing := range f.invites {
		if existing.OrgID == invite.OrgID &&
			strings.EqualFold(existing.Email, invite.Email) &&
			existing.Status == domain.InvitePending {
			existing.Status = domain.InviteRevoked
			f.invites[id] = existing
		}
	}
	f.invites[invite.ID] = invite
	return nil
}

func (f *fakeInviteStore) FindInviteByTokenHash(_ context.Context, tokenHash string) (domain.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return domain.Invite{}, f.findErr
	}
	for _, invite := range f.invites {
		if invite.TokenHash == tokenHash {
			return invite, nil
		}
	}
	return domain.Invite{}, domain.ErrInviteNotFound
}

func (f *fakeInviteStore) ListInvites(_ context.Context, orgID string) ([]domain.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []domain.Invite
	for _, invite := range f.invites {
		if invite.OrgID != orgID {
			continue
		}
		invite.TokenHash = ""
		out = append(out, invite)
	}
	return out, nil
}

func (f *fakeInviteStore) RevokeInvite(_ context.Context, orgID, inviteID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revokeErr != nil {
		return f.revokeErr
	}
	invite, ok := f.invites[inviteID]
	if !ok || invite.OrgID != orgID || invite.Status != domain.InvitePending {
		return domain.ErrInviteNotFound
	}
	invite.Status = domain.InviteRevoked
	f.invites[inviteID] = invite
	return nil
}

func (f *fakeInviteStore) AcceptInvite(_ context.Context, inviteID, orgID, userID string, acceptedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acceptErr != nil {
		return f.acceptErr
	}
	invite, ok := f.invites[inviteID]
	if !ok || invite.OrgID != orgID || invite.Status != domain.InvitePending {
		return domain.ErrInviteNotFound
	}
	if f.orgs != nil {
		if existing, ok := f.orgs.memberships[orgID][userID]; ok && existing.Status == domain.MembershipSuspended {
			return domain.ErrInvalidInvite
		}
	}
	invite.Status = domain.InviteAccepted
	accepted := acceptedAt
	invite.AcceptedAt = &accepted
	f.invites[inviteID] = invite
	if f.orgs != nil {
		if f.orgs.memberships[orgID] == nil {
			f.orgs.memberships[orgID] = make(map[string]domain.Membership)
		}
		existing, ok := f.orgs.memberships[orgID][userID]
		if !ok || existing.Status == domain.MembershipInvited {
			f.orgs.memberships[orgID][userID] = domain.Membership{
				OrgID: orgID, UserID: userID, Status: domain.MembershipActive,
				CreatedAt: acceptedAt, UpdatedAt: acceptedAt,
			}
		}
	}
	return nil
}

func (f *fakeInviteStore) get(t *testing.T, inviteID string) domain.Invite {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	invite, ok := f.invites[inviteID]
	if !ok {
		t.Fatalf("invite %q not found", inviteID)
	}
	return invite
}

func (f *fakeInviteStore) pendingCount(orgID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, invite := range f.invites {
		if invite.OrgID == orgID && invite.Status == domain.InvitePending {
			count++
		}
	}
	return count
}

func (f *fakeInviteStore) byEmail(orgID, email string) []domain.Invite {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Invite
	for _, invite := range f.invites {
		if invite.OrgID == orgID && strings.EqualFold(invite.Email, email) {
			out = append(out, invite)
		}
	}
	return out
}

func (f *fakeInviteStore) setExpiry(t *testing.T, inviteID string, at time.Time) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	invite, ok := f.invites[inviteID]
	if !ok {
		t.Fatalf("invite %q not found", inviteID)
	}
	invite.ExpiresAt = at
	f.invites[inviteID] = invite
}

// inviteUsers returns different accounts by id. It keeps the existing fake's
// other store methods.
type inviteUsers struct {
	*fakeUsers
	byID map[string]domain.User
}

func (f inviteUsers) FindByID(_ context.Context, userID string) (domain.User, error) {
	user, ok := f.byID[userID]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return user, nil
}

func newInviteService(t *testing.T, orgs *fakeOrgStore, invites *fakeInviteStore, users domain.UserStore, notifier *fakeNotifier) *Service {
	t.Helper()
	return NewService(Deps{
		Orgs:     orgs,
		Invites:  invites,
		Users:    users,
		Notifier: notifier,
		Clock:    fixedClock{at: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)},
		IDs:      testUUIDs{},
		Secrets:  testSecrets{},
		Logger:   func(string, ...any) {},
	})
}

// inviteFixture creates an organization owned by inviter, with a second
// member account that can accept an invitation.
func inviteFixture(t *testing.T) (*Service, *fakeOrgStore, *fakeInviteStore, *fakeNotifier, domain.Organization) {
	t.Helper()
	orgs := newFakeOrgStore()
	invites := newFakeInviteStore(orgs)
	users := inviteUsers{
		fakeUsers: &fakeUsers{},
		byID: map[string]domain.User{
			"inviter": {ID: "inviter", Email: "owner@example.com", DisplayName: "Owner", Status: domain.UserActive, EmailVerified: true},
			"member":  {ID: "member", Email: "member@example.com", Status: domain.UserActive, EmailVerified: true},
		},
	}
	notifier := &fakeNotifier{}
	svc := newInviteService(t, orgs, invites, users, notifier)
	org, err := svc.CreateOrg(context.Background(), "inviter", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	return svc, orgs, invites, notifier, org
}

func TestInviteMemberStoresOnlyTheTokenHash(t *testing.T) {
	svc, _, invites, notifier, org := inviteFixture(t)
	ctx := context.Background()

	invite, err := svc.InviteMember(ctx, org.ID, "inviter", "  Member@Example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if invite.Email != "member@example.com" {
		t.Fatalf("email = %q, want normalised address", invite.Email)
	}
	if invite.Status != domain.InvitePending {
		t.Fatalf("status = %q, want pending", invite.Status)
	}
	if invite.TokenHash != "" {
		t.Fatal("InviteMember returned a token hash")
	}
	wantExpiry := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	if !invite.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expires_at = %v, want %v", invite.ExpiresAt, wantExpiry)
	}

	stored := invites.get(t, invite.ID)
	if stored.TokenHash == "" {
		t.Fatal("no token hash was stored")
	}
	if notifier.invite.AcceptToken == "" {
		t.Fatal("notification carried no accept token")
	}
	if stored.TokenHash != domain.HashInviteToken(notifier.invite.AcceptToken) {
		t.Fatal("stored hash does not match the notified token")
	}
	if stored.TokenHash == notifier.invite.AcceptToken {
		t.Fatal("the plaintext token was stored")
	}
	if notifier.invite.Recipient != "member@example.com" ||
		notifier.invite.OrgName != "Acme" ||
		notifier.invite.InviterName != "Owner" ||
		notifier.invite.Locale != "fa" ||
		notifier.invite.CorrelationID != invite.ID {
		t.Fatalf("notification = %+v", notifier.invite)
	}

	list, err := svc.ListInvites(ctx, org.ID, "inviter")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	if list[0].TokenHash != "" {
		t.Fatal("ListInvites returned a token hash")
	}
}

func TestInviteMemberRotatesAPendingInvite(t *testing.T) {
	svc, _, invites, notifier, org := inviteFixture(t)
	ctx := context.Background()

	first, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	firstToken := notifier.invite.AcceptToken
	second, err := svc.InviteMember(ctx, org.ID, "inviter", "MEMBER@example.com")
	if err != nil {
		t.Fatal(err)
	}
	secondToken := notifier.invite.AcceptToken
	if firstToken == secondToken {
		t.Fatal("re-inviting did not rotate the token")
	}
	if notifier.inviteCalls != 2 {
		t.Fatalf("notifications = %d, want 2", notifier.inviteCalls)
	}
	if invites.pendingCount(org.ID) != 1 {
		t.Fatalf("pending invites = %d, want 1", invites.pendingCount(org.ID))
	}
	if got := invites.get(t, first.ID); got.Status != domain.InviteRevoked {
		t.Fatalf("first status = %q, want revoked", got.Status)
	}
	if got := invites.get(t, second.ID); got.Status != domain.InvitePending {
		t.Fatalf("second status = %q, want pending", got.Status)
	}
	if got := len(invites.byEmail(org.ID, "member@example.com")); got != 2 {
		t.Fatalf("invite history = %d, want 2", got)
	}
}

func TestInviteMemberRequiresAnActiveMember(t *testing.T) {
	svc, _, invites, notifier, org := inviteFixture(t)

	_, err := svc.InviteMember(context.Background(), org.ID, "stranger", "member@example.com")
	if !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
	if invites.pendingCount(org.ID) != 0 || notifier.inviteCalls != 0 {
		t.Fatal("a non-member created an invitation")
	}
}

func TestInviteMemberRejectsInvalidEmail(t *testing.T) {
	svc, _, invites, notifier, org := inviteFixture(t)

	_, err := svc.InviteMember(context.Background(), org.ID, "inviter", "not-an-email")
	if !errors.Is(err, domain.ErrInvalidEmail) {
		t.Fatalf("err = %v, want ErrInvalidEmail", err)
	}
	if invites.pendingCount(org.ID) != 0 || notifier.inviteCalls != 0 {
		t.Fatal("an invalid address created an invitation")
	}
}

func TestInviteSendFailureRevokesThePendingInvite(t *testing.T) {
	svc, _, invites, notifier, org := inviteFixture(t)
	notifier.inviteErr = errors.New("notifications unavailable")

	_, err := svc.InviteMember(context.Background(), org.ID, "inviter", "member@example.com")
	if err == nil {
		t.Fatal("expected the notification failure")
	}
	if invites.pendingCount(org.ID) != 0 {
		t.Fatal("a failed send left a pending invite")
	}
	for _, invite := range invites.byEmail(org.ID, "member@example.com") {
		if invite.Status != domain.InviteRevoked {
			t.Fatalf("leftover invite status = %q, want revoked", invite.Status)
		}
	}
}

func TestAcceptInviteMakesTheUserAnActiveMember(t *testing.T) {
	svc, orgs, invites, notifier, org := inviteFixture(t)
	ctx := context.Background()

	invite, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token := notifier.invite.AcceptToken
	accepted, err := svc.AcceptInvite(ctx, "member", token)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != domain.InviteAccepted || accepted.AcceptedAt == nil || accepted.TokenHash != "" {
		t.Fatalf("accepted invite = %+v", accepted)
	}
	if got := invites.get(t, invite.ID); got.Status != domain.InviteAccepted || got.AcceptedAt == nil {
		t.Fatalf("stored invite = %+v", got)
	}
	membership, err := orgs.GetMembership(ctx, org.ID, "member")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}

	// Single use.
	if _, err := svc.AcceptInvite(ctx, "member", token); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("second accept err = %v, want ErrInvalidInvite", err)
	}
}

func TestAcceptInviteRefusesSuspendedMembership(t *testing.T) {
	svc, orgs, invites, notifier, org := inviteFixture(t)
	ctx := context.Background()
	orgs.seedMembership(domain.Membership{
		OrgID: org.ID, UserID: "member", Status: domain.MembershipSuspended,
	})

	invite, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite", err)
	}
	if got := invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("suspended acceptance changed the invite to %q", got.Status)
	}
	membership, err := orgs.GetMembership(ctx, org.ID, "member")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipSuspended {
		t.Fatalf("suspended membership became %q", membership.Status)
	}
}

func TestAcceptInviteUsesCaseInsensitiveEmail(t *testing.T) {
	svc, orgs, _, notifier, org := inviteFixture(t)
	ctx := context.Background()
	if _, err := svc.InviteMember(ctx, org.ID, "inviter", "MEMBER@example.com"); err != nil {
		t.Fatal(err)
	}
	orgs.memberships[org.ID] = map[string]domain.Membership{} // the user is not a member yet

	if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptInviteRejectsASingleGenericFailure(t *testing.T) {
	ctx := context.Background()

	t.Run("expired", func(t *testing.T) {
		svc, _, invites, notifier, org := inviteFixture(t)
		invite, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com")
		if err != nil {
			t.Fatal(err)
		}
		invites.setExpiry(t, invite.ID, time.Date(2026, 11, 2, 11, 59, 0, 0, time.UTC))
		if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
	})

	t.Run("revoked", func(t *testing.T) {
		svc, _, _, notifier, org := inviteFixture(t)
		if _, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com"); err != nil {
			t.Fatal(err)
		}
		if err := svc.RevokeInvite(ctx, org.ID, notifier.invite.CorrelationID, "inviter"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
	})

	t.Run("wrong account email", func(t *testing.T) {
		svc, _, invites, notifier, org := inviteFixture(t)
		invite, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com")
		if err != nil {
			t.Fatal(err)
		}
		svc.users = inviteUsers{
			fakeUsers: &fakeUsers{},
			byID: map[string]domain.User{
				"stranger": {ID: "stranger", Email: "stranger@example.com", Status: domain.UserActive, EmailVerified: true},
			},
		}
		if _, err := svc.AcceptInvite(ctx, "stranger", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
		if got := invites.get(t, invite.ID); got.Status != domain.InvitePending {
			t.Fatalf("wrong-account acceptance changed the invite to %q", got.Status)
		}
	})

	t.Run("account without a verified email", func(t *testing.T) {
		svc, _, _, notifier, org := inviteFixture(t)
		if _, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com"); err != nil {
			t.Fatal(err)
		}
		svc.users = inviteUsers{
			fakeUsers: &fakeUsers{},
			byID: map[string]domain.User{
				"member": {ID: "member", Email: "member@example.com", Status: domain.UserSignupPending},
			},
		}
		if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
	})

	t.Run("active account with an unverified email", func(t *testing.T) {
		svc, _, _, notifier, org := inviteFixture(t)
		if _, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com"); err != nil {
			t.Fatal(err)
		}
		svc.users = inviteUsers{
			fakeUsers: &fakeUsers{},
			byID: map[string]domain.User{
				"member": {ID: "member", Email: "member@example.com", Status: domain.UserActive},
			},
		}
		if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
	})

	t.Run("disabled account", func(t *testing.T) {
		svc, _, _, notifier, org := inviteFixture(t)
		if _, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com"); err != nil {
			t.Fatal(err)
		}
		svc.users = inviteUsers{
			fakeUsers: &fakeUsers{},
			byID: map[string]domain.User{
				"member": {ID: "member", Email: "member@example.com", Status: domain.UserDisabled, EmailVerified: true},
			},
		}
		if _, err := svc.AcceptInvite(ctx, "member", notifier.invite.AcceptToken); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		svc, _, _, _, _ := inviteFixture(t)
		if _, err := svc.AcceptInvite(ctx, "member", "not-a-token"); !errors.Is(err, domain.ErrInvalidInvite) {
			t.Fatalf("err = %v, want ErrInvalidInvite", err)
		}
	})
}

func TestRevokeInviteRequiresMembershipAndPendingInvite(t *testing.T) {
	svc, _, _, notifier, org := inviteFixture(t)
	ctx := context.Background()
	if _, err := svc.InviteMember(ctx, org.ID, "inviter", "member@example.com"); err != nil {
		t.Fatal(err)
	}
	inviteID := notifier.invite.CorrelationID

	if err := svc.RevokeInvite(ctx, org.ID, inviteID, "stranger"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("stranger revoke err = %v, want ErrNotMember", err)
	}
	if err := svc.RevokeInvite(ctx, org.ID, inviteID, "inviter"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeInvite(ctx, org.ID, inviteID, "inviter"); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("second revoke err = %v, want ErrInviteNotFound", err)
	}
}

func TestListInvitesRejectsUnknownOrganization(t *testing.T) {
	svc, _, _, _, _ := inviteFixture(t)
	if _, err := svc.ListInvites(context.Background(), "missing", "inviter"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrOrgNotFound", err)
	}
}

func TestInviteWritesRequireOrgAdministration(t *testing.T) {
	svc, orgs, _, notifier, org := inviteFixture(t)
	ctx := context.Background()
	orgs.seedMembership(domain.Membership{OrgID: org.ID, UserID: "member", Status: domain.MembershipActive})
	orgs.seedMembership(domain.Membership{OrgID: org.ID, UserID: "admin", Status: domain.MembershipActive, Role: domain.RoleAdmin})
	svc.users.(inviteUsers).byID["admin"] = domain.User{ID: "admin", Email: "admin@example.com", Status: domain.UserActive, EmailVerified: true}

	if _, err := svc.InviteMember(ctx, org.ID, "member", "new@example.com"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member invite err = %v, want ErrForbidden", err)
	}
	if notifier.inviteCalls != 0 {
		t.Fatalf("forbidden invite sent %d notifications", notifier.inviteCalls)
	}
	if _, err := svc.ListInvites(ctx, org.ID, "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member list err = %v, want ErrForbidden", err)
	}
	invite, err := svc.InviteMember(ctx, org.ID, "admin", "new@example.com")
	if err != nil {
		t.Fatalf("admin invite: %v", err)
	}
	if err := svc.RevokeInvite(ctx, org.ID, invite.ID, "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member revoke err = %v, want ErrForbidden", err)
	}
	if err := svc.RevokeInvite(ctx, org.ID, invite.ID, "admin"); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
}
