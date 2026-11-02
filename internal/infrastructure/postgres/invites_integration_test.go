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
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
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
		TokenHash: domain.HashInviteToken(token),
		InviterID: inviterID,
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(domain.InviteTTL),
		CreatedAt: now,
	}
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
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, acceptedAt); err != nil {
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
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, acceptedAt); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("second accept err = %v, want ErrInviteNotFound", err)
	}
	if err := store.RevokeInvite(ctx, org.ID, invite.ID); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("revoke accepted err = %v, want ErrInviteNotFound", err)
	}
}

func TestInviteAcceptRefusesSuspendedMember(t *testing.T) {
	store, org, inviter, member := createInviteFixture(t)
	ctx := context.Background()

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "suspended-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, time.Now().UTC()); !errors.Is(err, domain.ErrInvalidInvite) {
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

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "active-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, time.Now().UTC()); err != nil {
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

	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipInvited); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "member@example.com", "invited-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptInvite(ctx, invite.ID, org.ID, member.ID, time.Now().UTC()); err != nil {
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
	if err := store.AcceptInvite(ctx, uuid.NewString(), uuid.NewString(), "bad", time.Now()); err == nil {
		t.Fatal("invalid user id was accepted")
	}
}
