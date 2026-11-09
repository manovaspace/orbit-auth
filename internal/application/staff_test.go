package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

var staffAlice = domain.StaffActor{Subject: "staff-alice", Reason: "onboarding request"}

// staffFixture wires the service with every store the staff operations and the
// refresh path touch.
type staffFixture struct {
	svc      *Service
	orgs     *fakeOrgStore
	invites  *fakeInviteStore
	refresh  *fakeRefreshStore
	notifier *fakeNotifier
}

func newStaffFixture(t *testing.T) staffFixture {
	t.Helper()
	orgs := newFakeOrgStore()
	invites := newFakeInviteStore(orgs)
	orgs.invites = invites
	refresh := newFakeRefreshStore()
	notifier := &fakeNotifier{}
	svc := NewService(Deps{
		Orgs:                orgs,
		Invites:             invites,
		Users:               fakeOrgUsers{fakeUsers: &fakeUsers{}},
		Refresh:             refresh,
		Notifier:            notifier,
		Clock:               fixedClock{at: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)},
		IDs:                 testUUIDs{},
		Signer:              testSigner{},
		Secrets:             testSecrets{},
		AutoSelectSingleOrg: true,
		Logger:              func(string, ...any) {},
	})
	return staffFixture{svc: svc, orgs: orgs, invites: invites, refresh: refresh, notifier: notifier}
}

func provisionPhone(t *testing.T, f staffFixture, name, key, phone string) ProvisionOrgResult {
	t.Helper()
	result, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           name,
		IdempotencyKey: key,
		Target:         domain.InviteTarget{Phone: phone},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStaffProvisionCreatesOrgWithOwnerInviteAndNoMember(t *testing.T) {
	f := newStaffFixture(t)
	result, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "  Acme Holdings ",
		Slug:           "acme-holdings",
		IdempotencyKey: "req-1",
		Target:         domain.InviteTarget{Phone: "+989121234567"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Organization.Name != "Acme Holdings" || result.Summary.Organization.Slug != "acme-holdings" {
		t.Fatalf("organization = %+v", result.Summary.Organization)
	}
	if result.Summary.Organization.Status != domain.OrgStatusActive {
		t.Fatalf("status = %q, want active", result.Summary.Organization.Status)
	}
	if result.Summary.MemberCount != 0 || len(result.Summary.OwnerUserIDs) != 0 {
		t.Fatalf("staff provisioning must add no member: %+v", result.Summary)
	}
	if result.Token == "" {
		t.Fatal("phone owner must receive the raw token")
	}
	if result.Invite.Role != domain.RoleOwner || result.Invite.Phone != "+989121234567" {
		t.Fatalf("owner invite = %+v", result.Invite)
	}
	if result.Invite.TokenHash != "" {
		t.Fatal("token hash must not be returned")
	}
	stored := f.invites.get(t, result.Invite.ID)
	if stored.InviterStaffSubject != "staff-alice" || stored.InviterID != "" {
		t.Fatalf("stored inviter = id %q staff %q, want staff only", stored.InviterID, stored.InviterStaffSubject)
	}
	if f.notifier.inviteCalls != 0 {
		t.Fatal("a phone owner has no notification channel yet")
	}
}

func TestStaffProvisionEmailOwnerGetsEmailAndNoToken(t *testing.T) {
	f := newStaffFixture(t)
	result, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "Beta",
		IdempotencyKey: "req-email",
		Target:         domain.InviteTarget{Email: "Owner@Example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Token != "" {
		t.Fatal("an email owner must not receive the token in the response")
	}
	if f.notifier.inviteCalls != 1 || f.notifier.invite.Recipient != "owner@example.com" {
		t.Fatalf("invite email = calls %d recipient %q", f.notifier.inviteCalls, f.notifier.invite.Recipient)
	}
	if f.notifier.invite.AcceptToken == "" || f.notifier.invite.InviterName != "Manova" {
		t.Fatalf("notification = %+v", f.notifier.invite)
	}
}

func TestStaffProvisionRepeatReturnsSameOrgWithNewToken(t *testing.T) {
	f := newStaffFixture(t)
	first := provisionPhone(t, f, "Gamma", "req-repeat", "+989120000001")
	second, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "Gamma",
		Slug:           "gamma-explicit",
		IdempotencyKey: "req-repeat",
		Target:         domain.InviteTarget{Phone: "+989120000001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Summary.Organization.ID != first.Summary.Organization.ID {
		t.Fatalf("repeat created %q, want %q", second.Summary.Organization.ID, first.Summary.Organization.ID)
	}
	if second.Summary.Organization.Slug != first.Summary.Organization.Slug {
		t.Fatal("a repeat must not change the slug of the organization it returns")
	}
	if second.Token == "" || second.Token == first.Token {
		t.Fatal("a repeat must rotate the owner token")
	}
	if got := f.invites.pendingCount(first.Summary.Organization.ID); got != 1 {
		t.Fatalf("pending owner invites = %d, want 1 after rotation", got)
	}
	if f.invites.get(t, first.Invite.ID).Status != domain.InviteRevoked {
		t.Fatal("the first owner invitation must be revoked by the rotation")
	}
	if len(f.orgs.orgs) != 1 {
		t.Fatalf("organizations = %d, want 1", len(f.orgs.orgs))
	}
}

func TestStaffProvisionRepeatWithAnotherNameConflicts(t *testing.T) {
	f := newStaffFixture(t)
	provisionPhone(t, f, "Delta", "req-conflict", "+989120000002")
	_, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "Epsilon",
		IdempotencyKey: "req-conflict",
		Target:         domain.InviteTarget{Phone: "+989120000002"},
	})
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestStaffProvisionRepeatWithAnotherOwnerTargetConflicts(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	first, err := f.svc.StaffProvisionOrganization(ctx, staffAlice, ProvisionOrgInput{
		Name:           "Zeta",
		IdempotencyKey: "req-target",
		Target:         domain.InviteTarget{Email: "owner@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The same name with another owner is another request, on another channel or
	// at another address. It is refused rather than rotated.
	for _, target := range []domain.InviteTarget{
		{Phone: "+989120000003"},
		{Email: "other@example.com"},
	} {
		_, err := f.svc.StaffProvisionOrganization(ctx, staffAlice, ProvisionOrgInput{
			Name:           "Zeta",
			IdempotencyKey: "req-target",
			Target:         target,
		})
		if !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Fatalf("repeat for owner %+v err = %v, want ErrIdempotencyConflict", target, err)
		}
	}
	invites, err := f.invites.ListInvites(ctx, first.Summary.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 1 || invites[0].ID != first.Invite.ID || invites[0].Status != domain.InvitePending {
		t.Fatalf("invites after the conflicts = %+v, want only the first pending owner invitation", invites)
	}
	if f.notifier.inviteCalls != 1 {
		t.Fatalf("invitation emails = %d, want the first one only", f.notifier.inviteCalls)
	}
}

func TestStaffProvisionRepeatWithTheSameNameAndOwnerRotatesTheInvite(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	first, err := f.svc.StaffProvisionOrganization(ctx, staffAlice, ProvisionOrgInput{
		Name:           "Eta",
		IdempotencyKey: "req-same",
		Target:         domain.InviteTarget{Email: "owner@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The address is normalised before the comparison, so another letter case is
	// the same owner.
	repeat, err := f.svc.StaffProvisionOrganization(ctx, staffAlice, ProvisionOrgInput{
		Name:           "Eta",
		IdempotencyKey: "req-same",
		Target:         domain.InviteTarget{Email: "Owner@Example.com"},
	})
	if err != nil {
		t.Fatalf("repeat with the same name and owner = %v, want the same organization", err)
	}
	if repeat.Summary.Organization.ID != first.Summary.Organization.ID {
		t.Fatalf("repeat organization = %q, want %q", repeat.Summary.Organization.ID, first.Summary.Organization.ID)
	}
	if repeat.Invite.ID == first.Invite.ID {
		t.Fatal("repeat did not rotate the owner invitation")
	}
	if f.notifier.inviteCalls != 2 {
		t.Fatalf("invitation emails = %d, want one per owner invitation", f.notifier.inviteCalls)
	}
	invites, err := f.invites.ListInvites(ctx, first.Summary.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 2 {
		t.Fatalf("invites = %d, want the rotated invitation and its predecessor", len(invites))
	}
	for _, invite := range invites {
		switch invite.ID {
		case repeat.Invite.ID:
			if invite.Status != domain.InvitePending {
				t.Fatalf("rotated invitation status = %q, want pending", invite.Status)
			}
		case first.Invite.ID:
			if invite.Status != domain.InviteRevoked {
				t.Fatalf("previous invitation status = %q, want revoked", invite.Status)
			}
		default:
			t.Fatalf("unexpected invitation %+v", invite)
		}
	}
}

func TestStaffProvisionValidatesInput(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		actor domain.StaffActor
		in    ProvisionOrgInput
		want  error
	}{
		{"no subject", domain.StaffActor{Reason: "r"}, ProvisionOrgInput{Name: "A", IdempotencyKey: "k", Target: domain.InviteTarget{Phone: "+989120000003"}}, domain.ErrStaffSubjectRequired},
		{"no reason", domain.StaffActor{Subject: "s"}, ProvisionOrgInput{Name: "A", IdempotencyKey: "k", Target: domain.InviteTarget{Phone: "+989120000003"}}, domain.ErrStaffReasonRequired},
		{"blank name", staffAlice, ProvisionOrgInput{Name: "  ", IdempotencyKey: "k", Target: domain.InviteTarget{Phone: "+989120000003"}}, domain.ErrInvalidOrgName},
		{"no key", staffAlice, ProvisionOrgInput{Name: "A", Target: domain.InviteTarget{Phone: "+989120000003"}}, domain.ErrIdempotencyKeyRequired},
		{"both targets", staffAlice, ProvisionOrgInput{Name: "A", IdempotencyKey: "k", Target: domain.InviteTarget{Email: "a@example.com", Phone: "+989120000003"}}, domain.ErrInvalidInviteTarget},
		{"bad phone", staffAlice, ProvisionOrgInput{Name: "A", IdempotencyKey: "k", Target: domain.InviteTarget{Phone: "0912"}}, domain.ErrInvalidPhone},
		{"reserved slug", staffAlice, ProvisionOrgInput{Name: "A", Slug: "admin", IdempotencyKey: "k", Target: domain.InviteTarget{Phone: "+989120000003"}}, domain.ErrInvalidSlug},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.StaffProvisionOrganization(ctx, tc.actor, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if len(f.orgs.orgs) != 0 {
		t.Fatalf("refused provisioning wrote %d organizations", len(f.orgs.orgs))
	}
}

func TestStaffProvisionEmailFailureRevokesOwnerInvite(t *testing.T) {
	f := newStaffFixture(t)
	f.notifier.inviteErr = errors.New("notifications down")
	_, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, ProvisionOrgInput{
		Name:           "Zeta",
		IdempotencyKey: "req-fail",
		Target:         domain.InviteTarget{Email: "owner@example.com"},
	})
	if err == nil {
		t.Fatal("expected the send failure")
	}
	for _, invite := range f.invites.invites {
		if invite.Status == domain.InvitePending {
			t.Fatalf("pending invite %s left after a failed send", invite.ID)
		}
	}
}

func TestStaffSuspendBlocksSelectAndRefreshUntilReactivated(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	org, err := f.svc.CreateOrg(ctx, "owner-1", "Omega", "")
	if err != nil {
		t.Fatal(err)
	}
	_, refreshToken, _, err := f.svc.issueTokensForOrg(ctx, "owner-1", org.ID)
	if err != nil {
		t.Fatal(err)
	}

	summary, err := f.svc.StaffSuspendOrganization(ctx, staffAlice, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Organization.Status != domain.OrgStatusSuspended || summary.Organization.SuspendedReason != staffAlice.Reason {
		t.Fatalf("suspended organization = %+v", summary.Organization)
	}
	if summary.MemberCount != 1 || len(summary.OwnerUserIDs) != 1 {
		t.Fatalf("suspension must keep memberships: %+v", summary)
	}

	if _, _, _, err := f.svc.SelectOrg(ctx, "owner-1", refreshToken, org.ID); !errors.Is(err, domain.ErrOrgSuspended) {
		t.Fatalf("SelectOrg err = %v, want ErrOrgSuspended", err)
	}
	if _, _, _, err := f.svc.Refresh(ctx, refreshToken); !errors.Is(err, domain.ErrOrgSuspended) {
		t.Fatalf("Refresh err = %v, want ErrOrgSuspended", err)
	}
	if got := f.svc.singleActiveOrg(ctx, "owner-1"); got != "" {
		t.Fatalf("login auto-select returned %q for a suspended organization", got)
	}

	if _, err := f.svc.StaffReactivateOrganization(ctx, staffAlice, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.Refresh(ctx, refreshToken); err != nil {
		t.Fatalf("Refresh after reactivation: %v; the refused token must still be usable", err)
	}
}

func TestStaffSuspendRequiresReasonAndExistingOrg(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	if _, err := f.svc.StaffSuspendOrganization(ctx, domain.StaffActor{Subject: "s"}, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, domain.ErrStaffReasonRequired) {
		t.Fatalf("err = %v, want ErrStaffReasonRequired", err)
	}
	if _, err := f.svc.StaffSuspendOrganization(ctx, staffAlice, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("err = %v, want ErrOrgNotFound", err)
	}
}

func TestStaffSetSlugFollowsRegistryRules(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	omega, err := f.svc.CreateOrg(ctx, "owner-1", "Omega", "omega-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateOrg(ctx, "owner-2", "Other", "taken-slug"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.StaffSetOrganizationSlug(ctx, staffAlice, omega.ID, "taken-slug"); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("taken slug err = %v, want ErrSlugTaken", err)
	}
	if _, err := f.svc.StaffSetOrganizationSlug(ctx, staffAlice, omega.ID, "api"); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Fatalf("reserved slug err = %v, want ErrInvalidSlug", err)
	}
	summary, err := f.svc.StaffSetOrganizationSlug(ctx, staffAlice, omega.ID, "  Omega-Two ")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Organization.Slug != "omega-two" {
		t.Fatalf("slug = %q, want omega-two", summary.Organization.Slug)
	}
	if _, err := f.svc.ResolveOrg(ctx, "omega-one"); err != nil {
		t.Fatalf("retired slug must keep resolving: %v", err)
	}
	// The store receives the staff subject and reason as the actor of the change.
	if len(f.orgs.staffSlugChanges) != 1 || f.orgs.staffSlugChanges[0] != staffAlice {
		t.Fatalf("store staff slug changes = %+v, want one by %+v", f.orgs.staffSlugChanges, staffAlice)
	}
}

func TestStaffReinviteOwnerRotatesAndReturnsTokenOnlyForPhone(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	org, err := f.svc.CreateOrg(ctx, "owner-1", "Sigma", "")
	if err != nil {
		t.Fatal(err)
	}

	first, token, err := f.svc.StaffReinviteOwner(ctx, staffAlice, org.ID, domain.InviteTarget{Email: "next@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "" || first.Role != domain.RoleOwner || first.InviterStaffSubject != "staff-alice" {
		t.Fatalf("email reinvite = %+v token %q", first, token)
	}
	if f.notifier.inviteCalls != 1 {
		t.Fatalf("email reinvite must send one invitation, sent %d", f.notifier.inviteCalls)
	}

	second, token, err := f.svc.StaffReinviteOwner(ctx, staffAlice, org.ID, domain.InviteTarget{Email: "NEXT@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if f.invites.get(t, first.ID).Status != domain.InviteRevoked || f.invites.get(t, second.ID).Status != domain.InvitePending {
		t.Fatal("reinvite must rotate the pending owner invitation for the same address")
	}
	if token != "" {
		t.Fatal("token is returned for phone owners only")
	}

	phone, token, err := f.svc.StaffReinviteOwner(ctx, staffAlice, org.ID, domain.InviteTarget{Phone: "+989121112222"})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || phone.Phone != "+989121112222" {
		t.Fatalf("phone reinvite = %+v token %q", phone, token)
	}
}

func TestStaffGetOrganizationOmitsTokenHashesAndAcceptedInvites(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	result := provisionPhone(t, f, "Kappa", "req-get", "+989123330000")
	org, err := f.svc.CreateOrg(ctx, "owner-1", "Kappa member org", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.InviteMember(ctx, org.ID, "owner-1", domain.InviteTarget{Phone: "+989124440000"}, domain.RoleMember); err != nil {
		t.Fatal(err)
	}

	detail, err := f.svc.StaffGetOrganization(ctx, result.Summary.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.PendingInvites) != 1 || detail.PendingInvites[0].ID != result.Invite.ID {
		t.Fatalf("pending invites = %+v", detail.PendingInvites)
	}
	for _, invite := range detail.PendingInvites {
		if invite.TokenHash != "" {
			t.Fatal("pending invites must not carry token hashes")
		}
	}
	if _, err := f.svc.StaffGetOrganization(ctx, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StaffGetOrganization(ctx, "00000000-0000-0000-0000-00000000000f"); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("unknown org err = %v", err)
	}
}

func TestStaffListPagesThroughSearchAndStatusFilters(t *testing.T) {
	f := newStaffFixture(t)
	ctx := context.Background()
	names := []string{"Alpha Co", "Beta Co", "Gamma Co", "Delta Co", "Epsilon Co"}
	for _, name := range names {
		if _, err := f.svc.CreateOrg(ctx, "owner-1", name, ""); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	token := ""
	pages := 0
	for {
		page, err := f.svc.StaffListOrganizations(ctx, StaffListInput{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, summary := range page.Organizations {
			if seen[summary.Organization.ID] {
				t.Fatalf("organization %s listed twice", summary.Organization.ID)
			}
			seen[summary.Organization.ID] = true
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
		if pages > 5 {
			t.Fatal("paging does not end")
		}
	}
	if len(seen) != len(names) || pages != 3 {
		t.Fatalf("listed %d organizations in %d pages, want %d in 3", len(seen), pages, len(names))
	}

	search, err := f.svc.StaffListOrganizations(ctx, StaffListInput{Query: "  GAMMA "})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Organizations) != 1 || search.Organizations[0].Organization.Name != "Gamma Co" {
		t.Fatalf("search = %+v", search.Organizations)
	}

	if _, err := f.svc.StaffSuspendOrganization(ctx, staffAlice, search.Organizations[0].Organization.ID); err != nil {
		t.Fatal(err)
	}
	suspended, err := f.svc.StaffListOrganizations(ctx, StaffListInput{Status: "suspended"})
	if err != nil {
		t.Fatal(err)
	}
	if len(suspended.Organizations) != 1 {
		t.Fatalf("suspended filter = %d organizations, want 1", len(suspended.Organizations))
	}

	if _, err := f.svc.StaffListOrganizations(ctx, StaffListInput{Status: "archived"}); !errors.Is(err, domain.ErrInvalidOrgStatus) {
		t.Fatalf("status err = %v", err)
	}
	if _, err := f.svc.StaffListOrganizations(ctx, StaffListInput{PageSize: 101}); !errors.Is(err, domain.ErrInvalidPageSize) {
		t.Fatalf("page size err = %v", err)
	}
	if _, err := f.svc.StaffListOrganizations(ctx, StaffListInput{PageSize: -1}); !errors.Is(err, domain.ErrInvalidPageSize) {
		t.Fatalf("negative page size err = %v", err)
	}
	if _, err := f.svc.StaffListOrganizations(ctx, StaffListInput{PageToken: "%%%"}); !errors.Is(err, domain.ErrInvalidPageToken) {
		t.Fatalf("token err = %v", err)
	}
}

// lateKeyOrgStore misses the idempotency key on its first lookup, as when a
// concurrent attempt with the same key commits after that lookup and before the
// slug check.
type lateKeyOrgStore struct {
	*fakeOrgStore
	hideFirst bool
}

func (s *lateKeyOrgStore) ProvisionedOrganization(ctx context.Context, key string) (domain.OrganizationProvision, bool, error) {
	if s.hideFirst {
		s.hideFirst = false
		return domain.OrganizationProvision{}, false, nil
	}
	return s.fakeOrgStore.ProvisionedOrganization(ctx, key)
}

func TestStaffProvisionRepeatThatLosesTheSlugRaceReturnsItsOrganization(t *testing.T) {
	f := newStaffFixture(t)
	input := ProvisionOrgInput{
		Name:           "Race Co",
		Slug:           "race-slug",
		IdempotencyKey: "race-key",
		Target:         domain.InviteTarget{Email: "owner@example.com"},
	}
	first, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, input)
	if err != nil {
		t.Fatal(err)
	}

	// The first attempt committed, so the explicit slug is taken when this call
	// checks it. The key lookup at the start of the call missed it.
	f.svc.orgs = &lateKeyOrgStore{fakeOrgStore: f.orgs, hideFirst: true}
	repeat, err := f.svc.StaffProvisionOrganization(context.Background(), staffAlice, input)
	if err != nil {
		t.Fatalf("repeat after the slug race = %v; want the existing organization", err)
	}
	if repeat.Summary.Organization.ID != first.Summary.Organization.ID {
		t.Fatalf("repeat returned organization %s, want %s", repeat.Summary.Organization.ID, first.Summary.Organization.ID)
	}
}
