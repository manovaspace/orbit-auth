package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func createInviteFixture(t *testing.T) (*Store, *domain.Organization, domain.User, domain.User) {
	t.Helper()
	store, _ := migrationPool(t)
	ctx := context.Background()

	inviter, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := testOrg("Acme", now)
	if err := store.CreateOrg(ctx, org, inviter.ID); err != nil {
		t.Fatal(err)
	}
	return store, &org, inviter, member
}

func pendingInvite(orgID, inviterID, email, token string) domain.Invite {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return domain.Invite{
		ID:        uuid.NewString(),
		OrgID:     orgID,
		Email:     email,
		Role:      domain.RoleMember,
		TokenHash: domain.HashInviteToken(token),
		InviterID: inviterID,
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(domain.InviteTTL),
		CreatedAt: now,
	}
}

func pendingPhoneInvite(orgID, inviterID, phone string, role domain.MembershipRole, token string) domain.Invite {
	invite := pendingInvite(orgID, inviterID, "", token)
	invite.Email = ""
	invite.Phone = phone
	invite.Role = role
	return invite
}

func TestInviteCreateFindListAndHashOnly(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "raw-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}

	found, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("raw-token"))
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != invite.ID || found.OrgID != org.ID || found.Email != "member@example.com" ||
		found.InviterID != inviter.ID || found.Status != domain.InvitePending {
		t.Fatalf("found = %+v", found)
	}
	if found.AcceptedAt != nil {
		t.Fatalf("pending invite accepted_at = %v", found.AcceptedAt)
	}
	if _, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("other-token")); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("missing token err = %v, want ErrInviteNotFound", err)
	}

	list, err := store.ListInvites(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != invite.ID {
		t.Fatalf("list = %+v", list)
	}
	if list[0].TokenHash != "" {
		t.Fatal("ListInvites returned a token hash")
	}

	var stored string
	if err := store.pool.QueryRow(ctx,
		`SELECT token_hash FROM organization_invites WHERE id = $1::uuid`, invite.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != domain.HashInviteToken("raw-token") || stored == "raw-token" {
		t.Fatalf("stored token_hash = %q", stored)
	}
}

func TestInviteCreateReplacesThePendingInviteForTheSameEmail(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	first := pendingInvite(org.ID, inviter.ID, "member@example.com", "first-token")
	if err := store.CreateInvite(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := pendingInvite(org.ID, inviter.ID, "MEMBER@example.com", "second-token")
	if err := store.CreateInvite(ctx, second); err != nil {
		t.Fatal(err)
	}

	old, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("first-token"))
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != domain.InviteRevoked {
		t.Fatalf("old status = %q, want revoked", old.Status)
	}
	current, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("second-token"))
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.InvitePending {
		t.Fatalf("new status = %q, want pending", current.Status)
	}
	list, err := store.ListInvites(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("history = %d, want 2", len(list))
	}
}

func TestInviteAcceptIsSingleUseAndMakesAnActiveMember(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "accept-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	acceptedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleMember, acceptedAt); err != nil {
		t.Fatal(err)
	}

	found, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("accept-token"))
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.InviteAccepted || found.AcceptedAt == nil || !found.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("accepted invite = %+v", found)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}

	// Single use: the conditional update no longer matches.
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleMember, acceptedAt); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("second accept err = %v, want ErrInviteNotFound", err)
	}
	if err := store.RevokeInvite(ctx, org.ID, invite.ID); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("revoke accepted err = %v, want ErrInviteNotFound", err)
	}
}

func TestInviteAcceptRefusesSuspendedMember(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended, domain.MembershipActor{}); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "suspended-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleMember, time.Now().UTC()); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("accept err = %v, want ErrInvalidInvite", err)
	}

	found, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("suspended-token"))
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.InvitePending || found.AcceptedAt != nil {
		t.Fatalf("suspended acceptance consumed the invite: %+v", found)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipSuspended {
		t.Fatalf("membership = %q, want suspended", membership.Status)
	}
}

func TestInviteAcceptKeepsAnActiveMemberActive(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "active-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleMember, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	found, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("active-token"))
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", found.Status)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}
}

func TestInviteAcceptActivatesAnInvitedMember(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipInvited, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "invited-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleMember, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}
}

func TestInviteRevoke(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "revoke-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeInvite(ctx, org.ID, invite.ID); err != nil {
		t.Fatal(err)
	}
	found, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("revoke-token"))
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != domain.InviteRevoked {
		t.Fatalf("status = %q, want revoked", found.Status)
	}
	if err := store.RevokeInvite(ctx, org.ID, invite.ID); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("second revoke err = %v, want ErrInviteNotFound", err)
	}
	if err := store.RevokeInvite(ctx, uuid.NewString(), invite.ID); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("wrong organization revoke err = %v, want ErrInviteNotFound", err)
	}
}

func TestInviteStoreRejectsInvalidUUIDs(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	if err := store.CreateInvite(ctx, domain.Invite{ID: "bad", OrgID: uuid.NewString(), InviterID: uuid.NewString()}); err == nil {
		t.Fatal("invalid invite id was accepted")
	}
	if err := store.RevokeInvite(ctx, "bad", uuid.NewString()); err == nil {
		t.Fatal("invalid organization id was accepted")
	}
	if _, err := store.ListInvites(ctx, "bad"); err == nil {
		t.Fatal("invalid organization id was accepted")
	}
	if err := store.AcceptInvite(ctx, uuid.NewString(), uuid.NewString(), "bad", domain.RoleMember, time.Now()); err == nil {
		t.Fatal("invalid user id was accepted")
	}
}

func TestInvitePhonePendingUniqueness(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	first := pendingPhoneInvite(org.ID, inviter.ID, "+989121234567", domain.RoleMember, "phone-first")
	if err := store.CreateInvite(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := pendingPhoneInvite(org.ID, inviter.ID, "+989121234567", domain.RoleAdmin, "phone-second")
	if err := store.CreateInvite(ctx, second); err != nil {
		t.Fatal(err)
	}
	old, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("phone-first"))
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != domain.InviteRevoked {
		t.Fatalf("old phone invite status = %q, want revoked", old.Status)
	}
	current, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("phone-second"))
	if err != nil {
		t.Fatal(err)
	}
	if current.Phone != "+989121234567" || current.Email != "" || current.Role != domain.RoleAdmin || current.Status != domain.InvitePending {
		t.Fatalf("current phone invite = %+v", current)
	}

	// The partial unique index refuses a second pending row for the same phone
	// even when written around the application's revoke step.
	_, err = store.pool.Exec(ctx, `
		INSERT INTO organization_invites (id, org_id, phone, token_hash, inviter_id, status, expires_at)
		VALUES ($1::uuid, $2::uuid, '+989121234567', 'raw-hash-direct', $3::uuid, 'pending', NOW() + INTERVAL '1 day')`,
		uuid.NewString(), org.ID, inviter.ID)
	if err == nil {
		t.Fatal("a second pending invite for the same phone was stored")
	}
}

func TestInviteStoresExactlyOneTarget(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	insert := func(email, phone any) error {
		_, err := store.pool.Exec(ctx, `
			INSERT INTO organization_invites (id, org_id, email, phone, token_hash, inviter_id, status, expires_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, 'pending', NOW() + INTERVAL '1 day')`,
			uuid.NewString(), org.ID, email, phone, uuid.NewString(), inviter.ID)
		return err
	}
	if err := insert(nil, nil); err == nil {
		t.Fatal("an invite with neither email nor phone was stored")
	}
	if err := insert("both@example.com", "+989121234567"); err == nil {
		t.Fatal("an invite with both email and phone was stored")
	}
	if err := insert("only@example.com", nil); err != nil {
		t.Fatalf("email-only invite refused: %v", err)
	}
	if err := insert(nil, "+989121234567"); err != nil {
		t.Fatalf("phone-only invite refused: %v", err)
	}
}

func TestInviteRejectsAnUnknownRole(t *testing.T) {
	store, org, inviter, _ := createInviteFixture(t)
	ctx := context.Background()

	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "bad-role-token")
	invite.Role = "root"
	if err := store.CreateInvite(ctx, invite); err == nil {
		t.Fatal("an invite with an unknown role was stored")
	}
}

func TestInviteAcceptGrantsTheInvitedRole(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	invite := pendingPhoneInvite(org.ID, inviter.ID, "+989121234567", domain.RoleAdmin, "role-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleAdmin, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive || membership.Role != domain.RoleAdmin {
		t.Fatalf("membership = %+v, want active admin", membership)
	}
	found, err := store.FindInviteByTokenHash(ctx, domain.HashInviteToken("role-token"))
	if err != nil {
		t.Fatal(err)
	}
	if found.Role != domain.RoleAdmin {
		t.Fatalf("stored invite role = %q, want admin", found.Role)
	}
}

func TestInviteAcceptKeepsAnExistingRole(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "keep-role-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, domain.RoleMember, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(ctx, org.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Role != domain.RoleAdmin {
		t.Fatalf("role = %q, want the existing admin kept", membership.Role)
	}
}
