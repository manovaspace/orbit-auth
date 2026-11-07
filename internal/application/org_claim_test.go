package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func serviceWithOrgs(t *testing.T, users domain.UserStore, refresh *fakeRefreshStore, orgs *fakeOrgStore, autoSelect bool) *Service {
	t.Helper()
	return NewService(Deps{
		OTP:                 &fakeOTP{},
		Users:               users,
		Refresh:             refresh,
		Tokens:              fakeApiTokens{},
		Orgs:                orgs,
		Notifier:            &fakeNotifier{},
		Flags:               fakeFlags{},
		Clock:               fixedClock{at: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)},
		IDs:                 testUUIDs{},
		Signer:              testSigner{},
		Hasher:              testHasher{},
		Codes:               testCodes{},
		Secrets:             testSecrets{},
		AutoSelectSingleOrg: autoSelect,
		Logger:              func(string, ...any) {},
	})
}

func orgWithMember(t *testing.T, store *fakeOrgStore, userID string) string {
	t.Helper()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(context.Background(), org, userID); err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func signedOrg(access string) string {
	const marker = "-org-"
	index := strings.LastIndex(access, marker)
	if index < 0 {
		return ""
	}
	return access[index+len(marker):]
}

func TestLoginWithPasswordAutoSelectsSingleOrg(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrgStore()
	orgID := orgWithMember(t, orgs, "u1")
	refresh := newFakeRefreshStore()
	svc := serviceWithOrgs(t, passwordUser(t, "chef@example.com", "", domain.UserActive), refresh, orgs, true)

	access, token, _, err := svc.LoginWithPassword(ctx, "chef@example.com", "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if got := signedOrg(access); got != orgID {
		t.Fatalf("access org = %q, want %q", got, orgID)
	}
	_, storedOrg, err := refresh.FindValid(ctx, domain.HashRefreshToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if storedOrg != orgID {
		t.Fatalf("refresh org = %q, want %q", storedOrg, orgID)
	}
}

func TestLoginWithPasswordAutoSelectRules(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	twoOrgs := func() *fakeOrgStore {
		orgs := newFakeOrgStore()
		orgWithMember(t, orgs, "u1")
		second := domain.Organization{ID: uuid.NewString(), Name: "Beta", CreatedAt: now, UpdatedAt: now}
		if err := orgs.CreateOrg(ctx, second, "u1"); err != nil {
			t.Fatal(err)
		}
		return orgs
	}

	tests := []struct {
		name       string
		autoSelect bool
		orgs       *fakeOrgStore
		wantOrg    bool
	}{
		{name: "auto-select on with one org", autoSelect: true, orgs: func() *fakeOrgStore {
			orgs := newFakeOrgStore()
			orgWithMember(t, orgs, "u1")
			return orgs
		}(), wantOrg: true},
		{name: "auto-select off with one org", autoSelect: false, orgs: func() *fakeOrgStore {
			orgs := newFakeOrgStore()
			orgWithMember(t, orgs, "u1")
			return orgs
		}()},
		{name: "auto-select on with zero orgs", autoSelect: true, orgs: newFakeOrgStore()},
		{name: "auto-select on with two orgs", autoSelect: true, orgs: twoOrgs()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := serviceWithOrgs(t, passwordUser(t, "chef@example.com", "", domain.UserActive), newFakeRefreshStore(), tc.orgs, tc.autoSelect)
			access, _, _, err := svc.LoginWithPassword(ctx, "chef@example.com", "s3cret-pass")
			if err != nil {
				t.Fatal(err)
			}
			got := signedOrg(access)
			if tc.wantOrg && got == "" {
				t.Fatal("access token has no org claim")
			}
			if !tc.wantOrg && got != "" {
				t.Fatalf("access org = %q, want none", got)
			}
		})
	}
}

func TestRefreshKeepsOrgWhileActiveMember(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrgStore()
	orgID := orgWithMember(t, orgs, "u1")
	refresh := newFakeRefreshStore()
	svc := serviceWithOrgs(t, &fakeUsers{}, refresh, orgs, false)

	old := "refresh-with-org"
	if err := refresh.Store(ctx, "u1", orgID, domain.HashRefreshToken(old), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	access, token, _, err := svc.Refresh(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if got := signedOrg(access); got != orgID {
		t.Fatalf("access org = %q, want %q", got, orgID)
	}
	_, storedOrg, err := refresh.FindValid(ctx, domain.HashRefreshToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if storedOrg != orgID {
		t.Fatalf("refresh org = %q, want %q", storedOrg, orgID)
	}
	if _, _, err := refresh.FindValid(ctx, domain.HashRefreshToken(old)); err == nil {
		t.Fatal("old refresh token was not revoked")
	}
}

func TestRefreshDropsOrgAfterSuspensionOrRemoval(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name            string
		breakMembership func(t *testing.T, orgs *fakeOrgStore, orgID string)
	}{
		{name: "suspended", breakMembership: func(t *testing.T, orgs *fakeOrgStore, orgID string) {
			orgs.seedMembership(domain.Membership{OrgID: orgID, UserID: "u2", Status: domain.MembershipActive, Role: domain.RoleOwner})
			if err := orgs.UpdateMemberStatus(ctx, orgID, "u1", domain.MembershipSuspended); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "removed", breakMembership: func(t *testing.T, orgs *fakeOrgStore, orgID string) {
			orgs.seedMembership(domain.Membership{OrgID: orgID, UserID: "u2", Status: domain.MembershipActive, Role: domain.RoleOwner})
			if err := orgs.RemoveMember(ctx, orgID, "u1"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orgs := newFakeOrgStore()
			orgID := orgWithMember(t, orgs, "u1")
			tc.breakMembership(t, orgs, orgID)
			refresh := newFakeRefreshStore()
			svc := serviceWithOrgs(t, &fakeUsers{}, refresh, orgs, false)

			old := "refresh-after-membership-change"
			if err := refresh.Store(ctx, "u1", orgID, domain.HashRefreshToken(old), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			access, token, _, err := svc.Refresh(ctx, old)
			if err != nil {
				t.Fatalf("refresh failed after %s: %v", tc.name, err)
			}
			if got := signedOrg(access); got != "" {
				t.Fatalf("access org = %q, want none", got)
			}
			_, storedOrg, err := refresh.FindValid(ctx, domain.HashRefreshToken(token))
			if err != nil {
				t.Fatal(err)
			}
			if storedOrg != "" {
				t.Fatalf("refresh org = %q, want none", storedOrg)
			}
		})
	}
}

func TestSelectOrgRotatesRefreshTokenAndCarriesOrg(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrgStore()
	orgID := orgWithMember(t, orgs, "u1")
	refresh := newFakeRefreshStore()
	svc := serviceWithOrgs(t, &fakeUsers{}, refresh, orgs, false)

	old := "refresh-before-select"
	if err := refresh.Store(ctx, "u1", "", domain.HashRefreshToken(old), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	access, token, _, err := svc.SelectOrg(ctx, "u1", old, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if got := signedOrg(access); got != orgID {
		t.Fatalf("access org = %q, want %q", got, orgID)
	}
	if _, _, err := refresh.FindValid(ctx, domain.HashRefreshToken(old)); err == nil {
		t.Fatal("presented refresh token was not revoked")
	}
	userID, storedOrg, err := refresh.FindValid(ctx, domain.HashRefreshToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if userID != "u1" || storedOrg != orgID {
		t.Fatalf("new refresh = %q/%q, want u1/%q", userID, storedOrg, orgID)
	}
}

func TestSelectOrgRejectsAnotherUsersRefreshToken(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrgStore()
	orgID := orgWithMember(t, orgs, "u1")
	refresh := newFakeRefreshStore()
	svc := serviceWithOrgs(t, &fakeUsers{}, refresh, orgs, false)

	old := "someone-elses-refresh"
	if err := refresh.Store(ctx, "u2", "", domain.HashRefreshToken(old), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := svc.SelectOrg(ctx, "u1", old, orgID)
	if !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("err = %v, want ErrInvalidRefresh", err)
	}
	if _, _, err := refresh.FindValid(ctx, domain.HashRefreshToken(old)); err != nil {
		t.Fatal("another user's refresh token was revoked")
	}
}

func TestSelectOrgNonMemberDoesNotRevokeOrIssue(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrgStore()
	orgID := orgWithMember(t, orgs, "u2")
	refresh := newFakeRefreshStore()
	svc := serviceWithOrgs(t, &fakeUsers{}, refresh, orgs, false)

	old := "non-member-refresh"
	if err := refresh.Store(ctx, "u1", "", domain.HashRefreshToken(old), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	access, token, _, err := svc.SelectOrg(ctx, "u1", old, orgID)
	if !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want domain.ErrNotMember", err)
	}
	if access != "" || token != "" {
		t.Fatalf("non-member received access=%q refresh=%q", access, token)
	}
	userID, storedOrg, err := refresh.FindValid(ctx, domain.HashRefreshToken(old))
	if err != nil {
		t.Fatalf("presented refresh token was revoked: %v", err)
	}
	if userID != "u1" || storedOrg != "" {
		t.Fatalf("presented refresh = %q/%q, want u1 with no org", userID, storedOrg)
	}
	if len(refresh.tokens) != 1 {
		t.Fatalf("stored refresh tokens = %d, want only the presented token", len(refresh.tokens))
	}
}
