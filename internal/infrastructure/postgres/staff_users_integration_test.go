package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func TestDisableUserRevokesRefreshTokensAndBlocksIssuance(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	user, err := store.FindOrCreateByEmail(ctx, "disable-me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	live := domain.HashRefreshToken("live-token-before-disable")
	if err := store.Store(ctx, user.ID, "", live, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	by := domain.StaffActor{Subject: "staff-1", Reason: "fraud review"}
	changed, err := store.DisableUser(ctx, user.ID, by)
	if err != nil || !changed {
		t.Fatalf("DisableUser = %v, %v; want changed", changed, err)
	}
	if _, _, err := store.FindValid(ctx, live); err == nil {
		t.Fatal("refresh token survived the disable")
	}
	if err := store.Store(ctx, user.ID, "", domain.HashRefreshToken("after-disable"), time.Now().Add(time.Hour)); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("Store after disable err = %v, want ErrAccountNotActive", err)
	}

	again, err := store.DisableUser(ctx, user.ID, by)
	if err != nil || again {
		t.Fatalf("second DisableUser = %v, %v; want unchanged", again, err)
	}

	var audited int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'user.disabled' AND subject_user_id = $1::uuid
		  AND actor_kind = 'staff' AND actor_id = 'staff-1' AND reason = 'fraud review'`,
		user.ID).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("user.disabled rows = %d, want 1 (the unchanged call writes none)", audited)
	}

	changed, err = store.EnableUser(ctx, user.ID, by)
	if err != nil || !changed {
		t.Fatalf("EnableUser = %v, %v; want changed", changed, err)
	}
	if err := store.Store(ctx, user.ID, "", domain.HashRefreshToken("after-enable"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Store after enable: %v", err)
	}
	if _, _, err := store.FindValid(ctx, live); err == nil {
		t.Fatal("enable must not restore a revoked refresh token")
	}
}

func TestDisableUserRefusesPendingAndUnknownAccounts(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	user, err := store.FindOrCreateByEmail(ctx, "pending-acct@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET status = 'signup_pending' WHERE id = $1::uuid`, user.ID); err != nil {
		t.Fatal(err)
	}
	by := domain.StaffActor{Subject: "staff-1", Reason: "check"}
	if _, err := store.DisableUser(ctx, user.ID, by); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("pending disable err = %v, want ErrAccountNotActive", err)
	}
	if _, err := store.EnableUser(ctx, user.ID, by); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("pending enable err = %v, want ErrAccountNotActive", err)
	}
	if _, err := store.DisableUser(ctx, uuid.NewString(), by); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown disable err = %v, want ErrUserNotFound", err)
	}
	if _, _, err := store.GetUserForStaff(ctx, uuid.NewString()); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown get err = %v, want ErrUserNotFound", err)
	}
}

func TestListUsersForStaffEscapesQueryAndPages(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	for _, email := range []string{"a_b@example.com", "axb@example.com", "c@example.com", "d@example.com"} {
		if _, err := store.FindOrCreateByEmail(ctx, email); err != nil {
			t.Fatal(err)
		}
	}

	// "_" is a literal in the query, so axb@ must not match.
	matched, err := store.ListUsersForStaff(ctx, domain.StaffUserListQuery{Query: "a_b", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].Email != "a_b@example.com" {
		t.Fatalf("query a_b matched %+v", matched)
	}

	var cursor *domain.StaffUserCursor
	seen := map[string]bool{}
	for pages := 0; pages < 10; pages++ {
		page, err := store.ListUsersForStaff(ctx, domain.StaffUserListQuery{After: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, u := range page {
			if seen[u.ID] {
				t.Fatalf("user %s repeated across pages", u.ID)
			}
			seen[u.ID] = true
		}
		last := page[len(page)-1]
		cursor = &domain.StaffUserCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		if len(page) < 2 {
			break
		}
	}
	if len(seen) < 4 {
		t.Fatalf("paging saw %d users, want at least the 4 seeded", len(seen))
	}
}

func TestListAuditEventsNeedsFilterAndReturnsOrgRows(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	if _, err := store.ListAuditEvents(ctx, domain.AuditEventQuery{Limit: 10}); !errors.Is(err, domain.ErrAuditFilterRequired) {
		t.Fatalf("unfiltered audit err = %v, want ErrAuditFilterRequired", err)
	}

	creator, err := store.FindOrCreateByEmail(ctx, "audit-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org := testOrg("Audited", time.Now().UTC().Truncate(time.Microsecond))
	if err := store.CreateOrg(ctx, org, creator.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAuditEvents(ctx, domain.AuditEventQuery{OrgID: org.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var sawCreated, sawMembership bool
	for _, row := range rows {
		switch row.Action {
		case domain.AuditOrgCreated:
			sawCreated = true
		case domain.AuditMembershipAdded:
			sawMembership = true
		}
		if row.Details == "" {
			t.Fatalf("row %s has empty details", row.Action)
		}
	}
	if !sawCreated || !sawMembership {
		t.Fatalf("org rows = %+v, want org.created and membership.added", rows)
	}
}

// TestSignInRacingDisableLeavesNoLiveToken runs a token issuance and a disable
// at once, many times. Either the issuance commits first and the disable revokes
// its token, or the disable commits first and the issuance is refused. A live
// token must never survive both.
func TestSignInRacingDisableLeavesNoLiveToken(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	user, err := store.FindOrCreateByEmail(ctx, "race-acct@example.com")
	if err != nil {
		t.Fatal(err)
	}
	by := domain.StaffActor{Subject: "staff-1", Reason: "race"}
	for i := 0; i < 20; i++ {
		hash := domain.HashRefreshToken(uuid.NewString())
		issued := make(chan error, 1)
		go func() {
			issued <- store.Store(ctx, user.ID, "", hash, time.Now().Add(time.Hour))
		}()
		if _, err := store.DisableUser(ctx, user.ID, by); err != nil {
			t.Fatalf("round %d: DisableUser: %v", i, err)
		}
		if err := <-issued; err != nil && !errors.Is(err, domain.ErrAccountNotActive) {
			t.Fatalf("round %d: Store: %v", i, err)
		}
		if _, _, err := store.FindValid(ctx, hash); err == nil {
			t.Fatalf("round %d: a live refresh token survived the disable", i)
		}
		if _, err := store.EnableUser(ctx, user.ID, by); err != nil {
			t.Fatalf("round %d: EnableUser: %v", i, err)
		}
	}
}

// TestDemoSeedKeepsDisabledAndActivatesPending checks that a service start
// re-running demo seeding does not re-enable a disabled account, and does
// activate a pending one.
func TestDemoSeedKeepsDisabledAndActivatesPending(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	const demoEmail = "demo-seed@example.com"
	if err := store.UpsertDemoUser(ctx, demoEmail, "", "demo-seed", "hash-1", "Demo"); err != nil {
		t.Fatal(err)
	}
	user, err := store.FindOrCreateByEmail(ctx, demoEmail)
	if err != nil {
		t.Fatal(err)
	}
	by := domain.StaffActor{Subject: "staff-1", Reason: "demo check"}
	if changed, err := store.DisableUser(ctx, user.ID, by); err != nil || !changed {
		t.Fatalf("DisableUser = %v, %v", changed, err)
	}
	var disabledAt time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT disabled_at FROM users WHERE id = $1::uuid`, user.ID).Scan(&disabledAt); err != nil {
		t.Fatal(err)
	}

	if err := store.UpsertDemoUser(ctx, demoEmail, "", "demo-seed", "hash-2", "Demo"); err != nil {
		t.Fatal(err)
	}
	var status string
	var keptAt time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT status, disabled_at FROM users WHERE id = $1::uuid`, user.ID).Scan(&status, &keptAt); err != nil {
		t.Fatal(err)
	}
	if status != domain.UserDisabled {
		t.Fatalf("status after re-seed = %q, want disabled", status)
	}
	if !keptAt.Equal(disabledAt) {
		t.Fatalf("disabled_at changed: %v -> %v", disabledAt, keptAt)
	}

	const pendingEmail = "demo-pending@example.com"
	if err := store.UpsertDemoUser(ctx, pendingEmail, "", "demo-pending", "hash-1", "Pending"); err != nil {
		t.Fatal(err)
	}
	pending, err := store.FindOrCreateByEmail(ctx, pendingEmail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET status = 'signup_pending' WHERE id = $1::uuid`, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDemoUser(ctx, pendingEmail, "", "demo-pending", "hash-2", "Pending"); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM users WHERE id = $1::uuid`, pending.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.UserActive {
		t.Fatalf("pending status after seed = %q, want active", status)
	}
}
