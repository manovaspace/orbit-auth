package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/testutil"
)

// allAuditActions lists every domain.Audit* action. A new action must be added
// here and to auditOperations, or TestEveryAuditActionIsWritten fails.
var allAuditActions = []string{
	domain.AuditInviteCreated,
	domain.AuditInviteRevoked,
	domain.AuditInviteAccepted,
	domain.AuditMembershipAdded,
	domain.AuditMembershipSuspended,
	domain.AuditMembershipReactivated,
	domain.AuditMembershipRemoved,
	domain.AuditMembershipRoleChanged,
	domain.AuditOrgCreated,
	domain.AuditOrgSuspended,
	domain.AuditOrgReactivated,
	domain.AuditOrgSlugChanged,
	domain.AuditUserCreated,
	domain.AuditUserDisabled,
	domain.AuditUserEnabled,
	domain.AuditIdentityLinked,
	domain.AuditIdentityUnlinked,
	domain.AuditPasswordSet,
	domain.AuditSessionsRevoked,
}

// auditExpect is the exact content of the audit row one operation writes.
type auditExpect struct {
	action    string
	kind      string
	actorID   string
	orgID     string
	subjectID string
	inviteID  string
	reason    string
}

func maxAuditID(t *testing.T, ctx context.Context, db *testutil.Database) int64 {
	t.Helper()
	var id int64
	if err := db.Pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM audit_events`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// expectAuditRow fails unless a row written after afterID has the action and
// every field of want. Nullable columns are compared as empty strings.
func expectAuditRow(t *testing.T, ctx context.Context, db *testutil.Database, afterID int64, want auditExpect) {
	t.Helper()
	rows, err := db.Pool.Query(ctx, `
		SELECT actor_kind, actor_id, COALESCE(org_id::text, ''), COALESCE(subject_user_id::text, ''),
		       COALESCE(invite_id::text, ''), reason
		FROM audit_events WHERE id > $1 AND action = $2`, afterID, want.action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []auditExpect
	for rows.Next() {
		var row auditExpect
		if err := rows.Scan(&row.kind, &row.actorID, &row.orgID, &row.subjectID, &row.inviteID, &row.reason); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
		if row.kind == want.kind && row.actorID == want.actorID && row.orgID == want.orgID &&
			row.subjectID == want.subjectID && row.inviteID == want.inviteID && row.reason == want.reason {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("no %s row matches %+v; rows written by the operation: %+v", want.action, want, got)
}

func TestEveryAuditActionIsWritten(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	staff := domain.StaffActor{Subject: "staff-1", Reason: "audit table"}
	covered := map[string]bool{}
	mark := func(action string) { covered[action] = true }

	// Accounts with a mobile number, username and display name, so the PII
	// test below has real values to look for.
	if err := store.UpsertDemoUser(ctx, "audit-owner@example.com", "", "audit-owner-name", "hash", "Audit Owner PII"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDemoUser(ctx, "audit-member@example.com", "+989121230001", "audit-member-name", "hash", "Audit Member PII"); err != nil {
		t.Fatal(err)
	}
	owner, err := store.FindOrCreateByEmail(ctx, "audit-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "audit-member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	acceptor, err := store.FindOrCreateByEmail(ctx, "audit-acceptor@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// user.created: a sign-in creates the account and writes its own row.
	before := maxAuditID(t, ctx, db)
	fresh, err := store.FindOrCreateByEmail(ctx, "audit-new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditUserCreated, kind: domain.AuditActorUser, actorID: fresh.ID, subjectID: fresh.ID})
	mark(domain.AuditUserCreated)

	org := testOrg("Audit Org", now)
	before = maxAuditID(t, ctx, db)
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditOrgCreated, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID})
	mark(domain.AuditOrgCreated)

	before = maxAuditID(t, ctx, db)
	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive, domain.RoleMember, domain.UserAuditActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditMembershipAdded, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, subjectID: member.ID})
	mark(domain.AuditMembershipAdded)

	before = maxAuditID(t, ctx, db)
	if err := store.UpdateMemberRole(ctx, org.ID, member.ID, domain.RoleAdmin, owner.ID); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditMembershipRoleChanged, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, subjectID: member.ID})
	mark(domain.AuditMembershipRoleChanged)

	before = maxAuditID(t, ctx, db)
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended,
		domain.MembershipActor{CallerID: owner.ID, Action: domain.ActionSuspendMember}); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditMembershipSuspended, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, subjectID: member.ID})
	mark(domain.AuditMembershipSuspended)

	before = maxAuditID(t, ctx, db)
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipActive,
		domain.MembershipActor{CallerID: owner.ID, Action: domain.ActionReactivateMember}); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditMembershipReactivated, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, subjectID: member.ID})
	mark(domain.AuditMembershipReactivated)

	revoked := pendingInvite(org.ID, owner.ID, "audit-invitee@example.com", "raw-token-revoked-audit")
	before = maxAuditID(t, ctx, db)
	if err := store.CreateInvite(ctx, revoked, domain.UserAuditActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditInviteCreated, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, inviteID: revoked.ID})
	mark(domain.AuditInviteCreated)

	before = maxAuditID(t, ctx, db)
	if err := store.RevokeInvite(ctx, org.ID, revoked.ID, domain.UserAuditActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditInviteRevoked, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, inviteID: revoked.ID})
	mark(domain.AuditInviteRevoked)

	accepted := pendingInvite(org.ID, owner.ID, "audit-acceptor@example.com", "raw-token-accepted-audit")
	if err := store.CreateInvite(ctx, accepted, domain.UserAuditActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	before = maxAuditID(t, ctx, db)
	if err := store.AcceptInvite(ctx, accepted.ID, org.ID, acceptor.ID, domain.RoleMember, now); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditInviteAccepted, kind: domain.AuditActorUser, actorID: acceptor.ID, orgID: org.ID, subjectID: acceptor.ID, inviteID: accepted.ID})
	mark(domain.AuditInviteAccepted)

	before = maxAuditID(t, ctx, db)
	if err := store.SetOrgSlug(ctx, org.ID, "audit-renamed", domain.MembershipActor{CallerID: owner.ID, Action: domain.ActionSetSlug}); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditOrgSlugChanged, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID})
	mark(domain.AuditOrgSlugChanged)

	before = maxAuditID(t, ctx, db)
	if err := store.RemoveMember(ctx, org.ID, member.ID, domain.MembershipActor{CallerID: owner.ID, Action: domain.ActionRemoveMember}); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditMembershipRemoved, kind: domain.AuditActorUser, actorID: owner.ID, orgID: org.ID, subjectID: member.ID})
	mark(domain.AuditMembershipRemoved)

	before = maxAuditID(t, ctx, db)
	if err := store.SuspendOrganization(ctx, org.ID, staff); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditOrgSuspended, kind: domain.AuditActorStaff, actorID: "staff-1", orgID: org.ID, reason: "audit table"})
	mark(domain.AuditOrgSuspended)

	before = maxAuditID(t, ctx, db)
	if err := store.ReactivateOrganization(ctx, org.ID, staff); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditOrgReactivated, kind: domain.AuditActorStaff, actorID: "staff-1", orgID: org.ID, reason: "audit table"})
	mark(domain.AuditOrgReactivated)

	before = maxAuditID(t, ctx, db)
	if err := store.SetOrgSlugByStaff(ctx, org.ID, "audit-staff-slug", staff); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditOrgSlugChanged, kind: domain.AuditActorStaff, actorID: "staff-1", orgID: org.ID, reason: "audit table"})

	before = maxAuditID(t, ctx, db)
	if _, err := store.DisableUser(ctx, acceptor.ID, staff); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditUserDisabled, kind: domain.AuditActorStaff, actorID: "staff-1", subjectID: acceptor.ID, reason: "audit table"})
	mark(domain.AuditUserDisabled)

	before = maxAuditID(t, ctx, db)
	if _, err := store.EnableUser(ctx, acceptor.ID, staff); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditUserEnabled, kind: domain.AuditActorStaff, actorID: "staff-1", subjectID: acceptor.ID, reason: "audit table"})
	mark(domain.AuditUserEnabled)

	// Sign-in methods: link and unlink a Google identity on owner, who has a
	// username, so unlinking leaves a usable password identifier.
	ownerActor := domain.UserAuditActor(owner.ID)
	before = maxAuditID(t, ctx, db)
	if _, err := store.LinkGoogleIdentity(ctx, "google", domain.GoogleIdentity{Subject: "audit-owner-sub", Email: "audit-owner@example.com", EmailVerified: true}, owner.ID, now); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditIdentityLinked, kind: domain.AuditActorUser, actorID: owner.ID, subjectID: owner.ID})
	mark(domain.AuditIdentityLinked)

	before = maxAuditID(t, ctx, db)
	if err := store.UnlinkGoogleIdentity(ctx, "google", owner.ID, ownerActor, now); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditIdentityUnlinked, kind: domain.AuditActorUser, actorID: owner.ID, subjectID: owner.ID})
	mark(domain.AuditIdentityUnlinked)

	before = maxAuditID(t, ctx, db)
	if err := store.SetPassword(ctx, owner.ID, "audit-new-hash", "", false, ownerActor, now); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditPasswordSet, kind: domain.AuditActorUser, actorID: owner.ID, subjectID: owner.ID})
	mark(domain.AuditPasswordSet)

	before = maxAuditID(t, ctx, db)
	if err := store.RevokeSessions(ctx, owner.ID, "", ownerActor, now); err != nil {
		t.Fatal(err)
	}
	expectAuditRow(t, ctx, db, before, auditExpect{action: domain.AuditSessionsRevoked, kind: domain.AuditActorUser, actorID: owner.ID, subjectID: owner.ID})
	mark(domain.AuditSessionsRevoked)

	for _, action := range allAuditActions {
		if !covered[action] {
			t.Errorf("audit action %q is not exercised by TestEveryAuditActionIsWritten", action)
		}
	}
	if len(covered) != len(allAuditActions) {
		t.Errorf("covered %d actions, want %d", len(covered), len(allAuditActions))
	}
}

// TestAuditDetailsCarryNoPersonalData scans every audit row of the scenario for
// the email, mobile number, username, display name and raw invitation token of
// the accounts it touched.
func TestAuditDetailsCarryNoPersonalData(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.UpsertDemoUser(ctx, "pii-owner@example.com", "+989121230002", "pii-owner-name", "hash", "PII Owner Name"); err != nil {
		t.Fatal(err)
	}
	owner, err := store.FindOrCreateByEmail(ctx, "pii-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org := testOrg("PII Org", now)
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	const inviteeEmail = "pii-invitee@example.com"
	const rawToken = "raw-token-pii-scan-value"
	invite := pendingInvite(org.ID, owner.ID, inviteeEmail, rawToken)
	if err := store.CreateInvite(ctx, invite, domain.UserAuditActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeInvite(ctx, org.ID, invite.ID, domain.UserAuditActor(owner.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DisableUser(ctx, owner.ID, domain.StaffActor{Subject: "staff-1", Reason: "pii scan"}); err != nil {
		t.Fatal(err)
	}

	needles := []string{"pii-owner@example.com", "+989121230002", "pii-owner-name", "PII Owner Name", inviteeEmail, rawToken}
	rows, err := db.Pool.Query(ctx, `
		SELECT row_to_json(a)::text FROM audit_events a
		WHERE a.org_id = $1::uuid OR a.subject_user_id = $2::uuid`, org.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	scanned := 0
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, needle := range needles {
			if strings.Contains(row, needle) {
				t.Fatalf("audit row holds %q: %s", needle, row)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if scanned < 4 {
		t.Fatalf("scanned %d audit rows, want the scenario's rows", scanned)
	}
}
