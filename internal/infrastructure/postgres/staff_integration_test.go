package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// staffOwnerInvite is the owner invitation a staff provisioning call stores: no
// user inviter, the staff subject instead.
func staffOwnerInvite(orgID string, at time.Time, token string) domain.Invite {
	return domain.Invite{
		ID:                  uuid.NewString(),
		OrgID:               orgID,
		Email:               "owner@staff-test.example",
		Role:                domain.RoleOwner,
		TokenHash:           domain.HashInviteToken(token),
		InviterStaffSubject: "staff-1",
		Status:              domain.InvitePending,
		ExpiresAt:           at.Add(domain.InviteTTL),
		CreatedAt:           at,
	}
}

func TestStaffProvisionWritesOrgWithoutMemberAndRepeatsByKey(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	org := testOrg("Provisioned", at)
	created, wasCreated, err := store.ProvisionOrganization(ctx, org, staffOwnerInvite(org.ID, at, "token-1"), "key-1")
	if err != nil || !wasCreated || created.Organization.ID != org.ID {
		t.Fatalf("provision = %q, created %v, %v; want a new organization %s", created.Organization.ID, wasCreated, err, org.ID)
	}
	members, err := store.ListMembers(ctx, org.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("members = %d, %v; want none, because staff are never members", len(members), err)
	}
	invites, err := store.ListInvites(ctx, org.ID)
	if err != nil || len(invites) != 1 || invites[0].Role != domain.RoleOwner || invites[0].InviterID != "" || invites[0].InviterStaffSubject != "staff-1" {
		t.Fatalf("invites = %+v, %v; want one owner invitation issued by staff-1", invites, err)
	}
	if n := countOutbox(t, store, domain.SubjectOrgCreated); n != 1 {
		t.Fatalf("auth.org.created events = %d, want 1", n)
	}

	// A repeat with the same key writes nothing and returns what the key recorded:
	// the first organization and its owner target, not the repeat's.
	repeat := testOrg("Provisioned", at)
	repeatOwner := staffOwnerInvite(repeat.ID, at, "token-2")
	repeatOwner.Email = "someone-else@staff-test.example"
	again, wasCreated, err := store.ProvisionOrganization(ctx, repeat, repeatOwner, "key-1")
	if err != nil || wasCreated || again.Organization.ID != org.ID {
		t.Fatalf("repeat = %q, created %v, %v; want the existing organization %s", again.Organization.ID, wasCreated, err, org.ID)
	}
	if again.OwnerTarget != (domain.InviteTarget{Email: "owner@staff-test.example"}) {
		t.Fatalf("repeat owner target = %+v, want the recorded target", again.OwnerTarget)
	}
	var rows int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id = $1`, repeat.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("repeat left %d organization rows behind, %v; want none", rows, err)
	}
	if n := countOutbox(t, store, domain.SubjectOrgCreated); n != 1 {
		t.Fatalf("auth.org.created events after repeat = %d, want 1", n)
	}
	found, ok, err := store.ProvisionedOrganization(ctx, "key-1")
	if err != nil || !ok || found.Organization.ID != org.ID {
		t.Fatalf("ProvisionedOrganization = %q, %v, %v; want %s", found.Organization.ID, ok, err, org.ID)
	}
	if found.OwnerTarget != (domain.InviteTarget{Email: "owner@staff-test.example"}) {
		t.Fatalf("recorded owner target = %+v, want the first owner", found.OwnerTarget)
	}
	if _, ok, err := store.ProvisionedOrganization(ctx, "never-used"); err != nil || ok {
		t.Fatalf("unused key found = %v, %v; want not found", ok, err)
	}
}

func TestStaffListPagesByCreatedAtAndMatchesLiterally(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	owner, err := store.FindOrCreateByEmail(ctx, "staff-list-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"Alpha Labs", "beta_tools", "Gamma 100%"}
	ids := make([]string, 0, len(names))
	for i, name := range names {
		org := testOrg(name, base.Add(time.Duration(i)*time.Minute))
		if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, org.ID)
	}

	first, err := store.ListOrganizationsForStaff(ctx, domain.StaffOrgListQuery{Limit: 2})
	if err != nil || len(first) != 2 || first[0].Organization.ID != ids[0] || first[1].Organization.ID != ids[1] {
		t.Fatalf("first page = %d rows, %v; want Alpha Labs and beta_tools, oldest first", len(first), err)
	}
	if first[0].MemberCount != 1 || len(first[0].OwnerUserIDs) != 1 || first[0].OwnerUserIDs[0] != owner.ID {
		t.Fatalf("summary = %+v; want one member, owned by the creator", first[0])
	}
	last := first[1].Organization
	second, err := store.ListOrganizationsForStaff(ctx, domain.StaffOrgListQuery{
		After: &domain.StaffOrgCursor{CreatedAt: last.CreatedAt, ID: last.ID},
		Limit: 2,
	})
	if err != nil || len(second) != 1 || second[0].Organization.ID != ids[2] {
		t.Fatalf("second page = %d rows, %v; want Gamma 100%% only", len(second), err)
	}

	// Search text matches literally: % and _ are not wildcards.
	for query, want := range map[string]string{
		"BETA": ids[1],
		"_":    ids[1],
		"100%": ids[2],
		"%":    ids[2],
		"labs": ids[0],
	} {
		rows, err := store.ListOrganizationsForStaff(ctx, domain.StaffOrgListQuery{Query: query, Limit: 10})
		if err != nil || len(rows) != 1 || rows[0].Organization.ID != want {
			t.Fatalf("query %q matched %d organizations, %v; want only %s", query, len(rows), err, want)
		}
	}

	if err := store.SuspendOrganization(ctx, ids[2], domain.StaffActor{Subject: "staff-1", Reason: "payment dispute"}); err != nil {
		t.Fatal(err)
	}
	suspended, err := store.ListOrganizationsForStaff(ctx, domain.StaffOrgListQuery{Status: domain.OrgStatusSuspended, Limit: 10})
	if err != nil || len(suspended) != 1 || suspended[0].Organization.ID != ids[2] || suspended[0].Organization.SuspendedReason != "payment dispute" {
		t.Fatalf("suspended page = %+v, %v; want Gamma with its reason", suspended, err)
	}
	active, err := store.ListOrganizationsForStaff(ctx, domain.StaffOrgListQuery{Status: domain.OrgStatusActive, Limit: 10})
	if err != nil || len(active) != 2 {
		t.Fatalf("active page = %d rows, %v; want 2", len(active), err)
	}
}

func TestStaffSuspendRecordsReasonEmitsOnceAndReactivates(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	owner, err := store.FindOrCreateByEmail(ctx, "suspend-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org := testOrg("Suspended Co", at)
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}

	by := domain.StaffActor{Subject: "staff-2", Reason: "chargeback"}
	if err := store.SuspendOrganization(ctx, org.ID, by); err != nil {
		t.Fatal(err)
	}
	// A repeat changes nothing and emits nothing.
	if err := store.SuspendOrganization(ctx, org.ID, by); err != nil {
		t.Fatalf("repeat suspend: %v", err)
	}
	got, err := store.GetOrg(ctx, org.ID)
	if err != nil || got.Status != domain.OrgStatusSuspended || got.SuspendedAt == nil || got.SuspendedReason != "chargeback" {
		t.Fatalf("suspended organization = %+v, %v; want status suspended with reason chargeback", got, err)
	}
	members, err := store.ListMembers(ctx, org.ID)
	if err != nil || len(members) != 1 || members[0].Status != domain.MembershipActive {
		t.Fatalf("members = %+v, %v; want the owner still active", members, err)
	}
	resolved, err := store.ResolveSlug(ctx, org.Slug)
	if err != nil || resolved.ID != org.ID || resolved.Status != domain.OrgStatusSuspended {
		t.Fatalf("ResolveSlug = %+v, %v; want the suspended organization", resolved, err)
	}
	listed, err := store.ListOrgsForUser(ctx, owner.ID)
	if err != nil || len(listed) != 1 || listed[0].Status != domain.OrgStatusSuspended {
		t.Fatalf("ListOrgsForUser = %+v, %v; want the suspended organization", listed, err)
	}
	if n := countOutbox(t, store, domain.SubjectOrgSuspended); n != 1 {
		t.Fatalf("auth.org.suspended events = %d, want 1", n)
	}
	var reason string
	if err := store.pool.QueryRow(ctx,
		`SELECT envelope -> 'payload' ->> 'reason' FROM outbox_events WHERE subject = $1`, domain.SubjectOrgSuspended).Scan(&reason); err != nil || reason != "chargeback" {
		t.Fatalf("auth.org.suspended reason = %q, %v; want chargeback", reason, err)
	}

	if err := store.ReactivateOrganization(ctx, org.ID, by); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetOrg(ctx, org.ID)
	if err != nil || got.Status != domain.OrgStatusActive || got.SuspendedAt != nil || got.SuspendedReason != "" {
		t.Fatalf("reactivated organization = %+v, %v; want active with no suspension", got, err)
	}
	if n := countOutbox(t, store, domain.SubjectOrgReactivated); n != 1 {
		t.Fatalf("auth.org.reactivated events = %d, want 1", n)
	}
	if err := store.SuspendOrganization(ctx, uuid.NewString(), by); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("suspend unknown organization = %v, want ErrOrgNotFound", err)
	}
}

// TestStaffProvisionConcurrentRepeatWithTheSameKeyCreatesOneOrganization races
// two attempts that share a key and an explicit slug. Neither may fail: the
// attempt that loses must return the organization the key created.
func TestStaffProvisionConcurrentRepeatWithTheSameKeyCreatesOneOrganization(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)

	type outcome struct {
		orgID   string
		created bool
		err     error
	}
	const attempts = 2
	outcomes := make(chan outcome, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		org := testOrg("Racing", at)
		org.Slug = "racing-org"
		go func() {
			<-start
			got, created, err := store.ProvisionOrganization(ctx, org, staffOwnerInvite(org.ID, at, uuid.NewString()), "race-key")
			outcomes <- outcome{orgID: got.Organization.ID, created: created, err: err}
		}()
	}
	close(start)

	createdCount := 0
	var orgID string
	for i := 0; i < attempts; i++ {
		got := <-outcomes
		if got.err != nil {
			t.Fatalf("concurrent provision failed: %v", got.err)
		}
		if got.created {
			createdCount++
		}
		if orgID == "" {
			orgID = got.orgID
		} else if got.orgID != orgID {
			t.Fatalf("attempts returned organizations %s and %s; want one", orgID, got.orgID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("%d attempts created an organization, want exactly 1", createdCount)
	}
	var rows int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE slug = 'racing-org'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("organizations with the slug = %d, %v; want 1", rows, err)
	}
	if n := countOutbox(t, store, domain.SubjectOrgCreated); n != 1 {
		t.Fatalf("auth.org.created events = %d, want 1", n)
	}
}

// TestStaffSetSlugEventNamesTheStaffSubjectAndReason checks the payload of
// auth.org.slug_changed: a staff change names the staff subject as the actor and
// carries the reason; an owner's change names the owner and carries no reason.
func TestStaffSetSlugEventNamesTheStaffSubjectAndReason(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	owner, err := store.FindOrCreateByEmail(ctx, "slug-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org := testOrg("Slug Co", at)
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}

	by := domain.StaffActor{Subject: "staff-slug-1", Reason: "rename for brand"}
	if err := store.SetOrgSlugByStaff(ctx, org.ID, "staff-renamed", by); err != nil {
		t.Fatal(err)
	}
	actor, reason, previous := latestSlugEvent(t, store, org.ID)
	if actor != "staff-slug-1" || reason == nil || *reason != "rename for brand" || previous != org.Slug {
		t.Fatalf("staff slug event actor %q, reason %v, previous %q; want the staff subject, the reason and %q", actor, reason, previous, org.Slug)
	}

	if err := store.SetOrgSlug(ctx, org.ID, "owner-renamed", ownerActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	actor, reason, previous = latestSlugEvent(t, store, org.ID)
	if actor != owner.ID || reason != nil || previous != "staff-renamed" {
		t.Fatalf("owner slug event actor %q, reason %v, previous %q; want the owner, no reason and staff-renamed", actor, reason, previous)
	}
}

// latestSlugEvent returns the actor, the optional reason and the previous slug of
// the newest auth.org.slug_changed event of orgID.
func latestSlugEvent(t *testing.T, store *Store, orgID string) (actor string, reason *string, previous string) {
	t.Helper()
	if err := store.pool.QueryRow(context.Background(),
		`SELECT envelope -> 'payload' ->> 'actor_id', envelope -> 'payload' ->> 'reason', envelope -> 'payload' ->> 'previous_slug'
		 FROM outbox_events
		 WHERE subject = $1 AND envelope -> 'payload' ->> 'org_id' = $2
		 ORDER BY id DESC LIMIT 1`, domain.SubjectOrgSlugChanged, orgID).Scan(&actor, &reason, &previous); err != nil {
		t.Fatal(err)
	}
	return actor, reason, previous
}
