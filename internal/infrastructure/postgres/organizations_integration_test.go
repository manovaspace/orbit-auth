package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/testutil"
)

func TestOrganizationCreateWritesCreatorMembership(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	creator, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, creator.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetOrg(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Acme" || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("organization = %+v", got)
	}

	membership, err := store.GetMembership(ctx, org.ID, creator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("creator status = %q, want active", membership.Status)
	}
	active := activeMemberCount(t, ctx, db, org.ID)
	if active != 1 {
		t.Fatalf("active members = %d, want 1", active)
	}

	list, err := store.ListOrgsForUser(ctx, creator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != org.ID {
		t.Fatalf("list = %+v", list)
	}
	var storedCreator uuid.UUID
	if err := db.Pool.QueryRow(ctx,
		`SELECT user_id FROM organization_memberships WHERE org_id = $1::uuid`, org.ID).Scan(&storedCreator); err != nil {
		t.Fatal(err)
	}
	if storedCreator.String() != creator.ID {
		t.Fatalf("stored creator = %s, want %s", storedCreator, creator.ID)
	}
}

// TestOrganizationCreateRollsBackWithoutCreator proves the organization and
// the creator membership share one transaction.
func TestOrganizationCreateRollsBackWithoutCreator(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	now := time.Now().UTC()
	org := domain.Organization{ID: uuid.NewString(), Name: "Orphan", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, uuid.NewString()); err == nil {
		t.Fatal("organization with a missing creator was created")
	}
	if _, err := store.GetOrg(ctx, org.ID); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("err = %v, want ErrOrgNotFound", err)
	}
}

func TestOrganizationMembershipLifecycleAndListing(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	owner, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	invited, err := store.FindOrCreateByEmail(ctx, "invited@example.com")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, invited.ID, domain.MembershipInvited); err != nil {
		t.Fatal(err)
	}

	list, err := store.ListOrgsForUser(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != org.ID {
		t.Fatalf("active member list = %+v", list)
	}
	list, err = store.ListOrgsForUser(ctx, invited.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("invited member list = %+v, want empty", list)
	}

	active := activeMemberCount(t, ctx, db, org.ID)
	if active != 2 {
		t.Fatalf("active members = %d, want 2", active)
	}

	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipSuspended {
		t.Fatalf("status = %q, want suspended", membership.Status)
	}
	list, err = store.ListOrgsForUser(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("suspended member list = %+v, want empty", list)
	}

	if err := store.RemoveMember(ctx, org.ID, invited.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMembership(ctx, org.ID, invited.ID); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("removed membership err = %v, want ErrNotMember", err)
	}
}

func TestOrganizationListMembersReturnsStatuses(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	owner, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	invited, err := store.FindOrCreateByEmail(ctx, "invited@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, invited.ID, domain.MembershipInvited); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended); err != nil {
		t.Fatal(err)
	}

	members, err := store.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %d, want 3", len(members))
	}
	statuses := make(map[string]domain.MembershipStatus, len(members))
	for _, membership := range members {
		if membership.OrgID != org.ID {
			t.Fatalf("membership org = %q, want %q", membership.OrgID, org.ID)
		}
		statuses[membership.UserID] = membership.Status
	}
	want := map[string]domain.MembershipStatus{
		owner.ID:   domain.MembershipActive,
		member.ID:  domain.MembershipSuspended,
		invited.ID: domain.MembershipInvited,
	}
	for userID, status := range want {
		if statuses[userID] != status {
			t.Fatalf("status[%q] = %q, want %q", userID, statuses[userID], status)
		}
	}
}

func TestAddMemberDoesNotReactivateSuspendedMember(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	owner, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended); err != nil {
		t.Fatal(err)
	}

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipSuspended {
		t.Fatalf("status = %q, want suspended", membership.Status)
	}
}

// TestConcurrentSuspendLeavesOneActiveMember proves the guard and the write
// share one organization-row lock: two concurrent suspensions of the final two
// active members cannot both succeed.
func TestConcurrentSuspendLeavesOneActiveMember(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	owner, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, userID := range []string{owner.ID, member.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			errs <- store.UpdateMemberStatus(ctx, org.ID, id, domain.MembershipSuspended)
		}(userID)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, lastActive := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrLastActiveMember):
			lastActive++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || lastActive != 1 {
		t.Fatalf("successes = %d, ErrLastActiveMember = %d; want 1 and 1", successes, lastActive)
	}
	if active := activeMemberCount(t, ctx, db, org.ID); active != 1 {
		t.Fatalf("active members = %d, want 1", active)
	}
}

func activeMemberCount(t *testing.T, ctx context.Context, db *testutil.Database, orgID string) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM organization_memberships WHERE org_id = $1::uuid AND status = 'active'`,
		orgID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestOrganizationStoreRejectsInvalidUUIDs(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	if _, err := store.GetOrg(ctx, "not-a-uuid"); err == nil {
		t.Fatal("invalid organization id was accepted")
	}
	if _, err := store.ListOrgsForUser(ctx, "not-a-uuid"); err == nil {
		t.Fatal("invalid user id was accepted")
	}
	if _, err := store.GetMembership(ctx, "not-a-uuid", "also-bad"); err == nil {
		t.Fatal("invalid membership ids were accepted")
	}
}
