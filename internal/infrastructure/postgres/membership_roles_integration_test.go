package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// testOrg builds an organization with a valid, unique slug.
func testOrg(name string, now time.Time) domain.Organization {
	id := uuid.NewString()
	return domain.Organization{ID: id, Name: name, Slug: domain.FallbackSlug(id), CreatedAt: now, UpdatedAt: now}
}

// ownerFixture creates an organization owned by "owner" with one extra account
// per role: admin and member.
func ownerFixture(t *testing.T) (*Store, domain.Organization, map[string]string) {
	t.Helper()
	store, _ := migrationPool(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, name := range []string{"owner", "admin", "member"} {
		user, err := store.FindOrCreateByEmail(ctx, name+"@example.com")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = user.ID
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := testOrg("Acme", now)
	if err := store.CreateOrg(ctx, org, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, ids["admin"], domain.MembershipActive, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, ids["member"], domain.MembershipActive, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	return store, org, ids
}

func TestMembershipRolesAreStoredAndListed(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()

	members, err := store.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]domain.MembershipRole{}
	for _, m := range members {
		roles[m.UserID] = m.Role
	}
	want := map[string]domain.MembershipRole{
		ids["owner"]:  domain.RoleOwner,
		ids["admin"]:  domain.RoleAdmin,
		ids["member"]: domain.RoleMember,
	}
	for user, role := range want {
		if roles[user] != role {
			t.Fatalf("role[%s] = %q, want %q", user, roles[user], role)
		}
	}
}

func TestLastActiveOwnerCannotBeRemovedSuspendedOrDemoted(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()

	if err := store.RemoveMember(ctx, org.ID, ids["owner"], domain.MembershipActor{}); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("remove last owner err = %v, want ErrLastOwner", err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, ids["owner"], domain.MembershipSuspended, domain.MembershipActor{}); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("suspend last owner err = %v, want ErrLastOwner", err)
	}
	if err := store.UpdateMemberRole(ctx, org.ID, ids["owner"], domain.RoleAdmin, ids["owner"]); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("demote last owner err = %v, want ErrLastOwner", err)
	}
	if err := store.UpdateMemberRole(ctx, org.ID, ids["member"], "root", ids["owner"]); !errors.Is(err, domain.ErrInvalidRole) {
		t.Fatalf("unknown role err = %v, want ErrInvalidRole", err)
	}

	// Two owners: one can step down.
	if err := store.UpdateMemberRole(ctx, org.ID, ids["admin"], domain.RoleOwner, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberRole(ctx, org.ID, ids["owner"], domain.RoleAdmin, ids["admin"]); err != nil {
		t.Fatalf("demote with a second owner: %v", err)
	}
	// The remaining owner is now the last one.
	if err := store.UpdateMemberRole(ctx, org.ID, ids["admin"], domain.RoleMember, ids["admin"]); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("demote new last owner err = %v, want ErrLastOwner", err)
	}
}

func TestSuspendedOwnerDoesNotCountAsAnOwner(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()

	if err := store.UpdateMemberRole(ctx, org.ID, ids["admin"], domain.RoleOwner, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, ids["admin"], domain.MembershipSuspended, domain.MembershipActor{}); err != nil {
		t.Fatal(err)
	}
	// admin is a suspended owner; the only active owner may not leave.
	if err := store.UpdateMemberStatus(ctx, org.ID, ids["owner"], domain.MembershipSuspended, domain.MembershipActor{}); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("suspend err = %v, want ErrLastOwner", err)
	}
}

func TestConcurrentOwnerDemotionsLeaveOneOwner(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()
	if err := store.UpdateMemberRole(ctx, org.ID, ids["admin"], domain.RoleOwner, ids["owner"]); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pair := range [][2]string{{ids["owner"], ids["admin"]}, {ids["admin"], ids["owner"]}} {
		wg.Add(1)
		go func(actor, target string) {
			defer wg.Done()
			<-start
			errs <- store.UpdateMemberRole(ctx, org.ID, target, domain.RoleAdmin, actor)
		}(pair[0], pair[1])
	}
	close(start)
	wg.Wait()
	close(errs)

	// The loser is refused either because the winner already demoted it (it no
	// longer has the authority) or because it would orphan the organization;
	// which one depends on lock order, and both keep one owner.
	successes, refused := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrLastOwner):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || refused != 1 {
		t.Fatalf("successes = %d, refused = %d; want 1 and 1", successes, refused)
	}
	var owners int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM organization_memberships WHERE org_id = $1::uuid AND status = 'active' AND role = 'owner'`,
		org.ID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("active owners = %d, want 1", owners)
	}
}

func TestUpdateMemberRoleWritesOneEventAndSkipsNoOps(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()

	count := func() int {
		var n int
		if err := store.pool.QueryRow(ctx,
			`SELECT count(*) FROM outbox_events WHERE subject = $1`, domain.SubjectMembershipRoleChanged).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := store.UpdateMemberRole(ctx, org.ID, ids["member"], domain.RoleMember, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("setting the current role wrote an event")
	}
	if err := store.UpdateMemberRole(ctx, org.ID, ids["member"], domain.RoleAdmin, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("role_changed events = %d, want 1", count())
	}
	var raw []byte
	if err := store.pool.QueryRow(ctx,
		`SELECT envelope -> 'payload' FROM outbox_events WHERE subject = $1`, domain.SubjectMembershipRoleChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload domain.MembershipRoleChangedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	if payload.OrgID != org.ID || payload.UserID != ids["member"] || payload.Role != domain.RoleAdmin ||
		payload.PreviousRole != domain.RoleMember || payload.ActorID != ids["owner"] {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAddMemberEventCarriesRole(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()
	var raw []byte
	if err := store.pool.QueryRow(ctx,
		`SELECT envelope -> 'payload' FROM outbox_events
		 WHERE subject = $1 AND envelope -> 'payload' ->> 'user_id' = $2`,
		domain.SubjectMembershipAdded, ids["admin"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload domain.MembershipAddedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OrgID != org.ID || payload.Role != domain.RoleAdmin {
		t.Fatalf("payload = %+v, want role admin", payload)
	}
}

// TestStoreRechecksTheActorUnderTheLock proves the store does not trust the
// application's earlier read: an actor demoted after authorization, or a
// target promoted after it, is refused when the write runs.
func TestStoreRechecksTheActorUnderTheLock(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()
	remove := func(caller, target string) error {
		return store.RemoveMember(ctx, org.ID, target, domain.MembershipActor{CallerID: caller, Action: domain.ActionRemoveMember})
	}
	suspend := func(caller, target string) error {
		return store.UpdateMemberStatus(ctx, org.ID, target, domain.MembershipSuspended,
			domain.MembershipActor{CallerID: caller, Action: domain.ActionSuspendMember})
	}

	// The admin is demoted after the application authorized their removal of
	// the plain member.
	if err := store.UpdateMemberRole(ctx, org.ID, ids["admin"], domain.RoleMember, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := remove(ids["admin"], ids["member"]); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("demoted admin removal err = %v, want ErrForbidden", err)
	}
	if err := suspend(ids["admin"], ids["member"]); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("demoted admin suspension err = %v, want ErrForbidden", err)
	}
	if m, err := store.GetMembership(ctx, org.ID, ids["member"]); err != nil || m.Status != domain.MembershipActive {
		t.Fatalf("member after refused writes = %+v, %v", m, err)
	}

	// The target is promoted after the application saw them as a plain member.
	if err := store.UpdateMemberRole(ctx, org.ID, ids["admin"], domain.RoleAdmin, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberRole(ctx, org.ID, ids["member"], domain.RoleAdmin, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := remove(ids["admin"], ids["member"]); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin removing a promoted admin err = %v, want ErrForbidden", err)
	}
	// An admin cannot change roles at all.
	if err := store.UpdateMemberRole(ctx, org.ID, ids["member"], domain.RoleMember, ids["admin"]); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin role change err = %v, want ErrForbidden", err)
	}
	// An owner still can, and a member can leave.
	if err := remove(ids["owner"], ids["member"]); err != nil {
		t.Fatalf("owner removal: %v", err)
	}
	if err := remove(ids["admin"], ids["admin"]); err != nil {
		t.Fatalf("admin leaving: %v", err)
	}
}

func TestAcceptingAnInviteKeepsTheStoredRoleInTheEvent(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	invite := pendingInvite(org.ID, inviter.ID, "owner@example.com", "owner-own-invite")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, inviter.ID, domain.RoleMember, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := store.pool.QueryRow(ctx, `
		SELECT envelope -> 'payload' FROM outbox_events
		WHERE subject = $1 AND envelope -> 'payload' ->> 'user_id' = $2
		ORDER BY id DESC LIMIT 1`, domain.SubjectMembershipAdded, inviter.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload domain.MembershipAddedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Role != domain.RoleOwner {
		t.Fatalf("event role = %q, want the owner's stored role", payload.Role)
	}
	if m, err := store.GetMembership(ctx, org.ID, inviter.ID); err != nil || m.Role != domain.RoleOwner {
		t.Fatalf("membership after accepting = %+v, %v", m, err)
	}
}
