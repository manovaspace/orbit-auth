package application

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// fakeOrgStore is the in-memory OrgStore used by the use-case tests. It models
// the same rows as Postgres and returns the domain errors the adapter returns.
type fakeOrgStore struct {
	mu          sync.RWMutex
	orgs        map[string]domain.Organization
	memberships map[string]map[string]domain.Membership
}

func newFakeOrgStore() *fakeOrgStore {
	return &fakeOrgStore{
		orgs:        make(map[string]domain.Organization),
		memberships: make(map[string]map[string]domain.Membership),
	}
}

func (f *fakeOrgStore) CreateOrg(_ context.Context, org domain.Organization, creatorID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgs[org.ID] = org
	f.memberships[org.ID] = map[string]domain.Membership{
		creatorID: {
			OrgID:     org.ID,
			UserID:    creatorID,
			Status:    domain.MembershipActive,
			CreatedAt: org.CreatedAt,
			UpdatedAt: org.UpdatedAt,
		},
	}
	return nil
}

func (f *fakeOrgStore) GetOrg(_ context.Context, orgID string) (domain.Organization, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	org, ok := f.orgs[orgID]
	if !ok {
		return domain.Organization{}, domain.ErrOrgNotFound
	}
	return org, nil
}

func (f *fakeOrgStore) ListOrgsForUser(_ context.Context, userID string) ([]domain.Organization, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []domain.Organization
	for orgID, members := range f.memberships {
		membership, ok := members[userID]
		if ok && membership.Status == domain.MembershipActive {
			out = append(out, f.orgs[orgID])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeOrgStore) ListMembers(_ context.Context, orgID string) ([]domain.Membership, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []domain.Membership
	for _, membership := range f.memberships[orgID] {
		out = append(out, membership)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (f *fakeOrgStore) AddMember(_ context.Context, orgID, userID string, status domain.MembershipStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.orgs[orgID]; !ok {
		return domain.ErrOrgNotFound
	}
	if f.memberships[orgID] == nil {
		f.memberships[orgID] = make(map[string]domain.Membership)
	}
	if _, ok := f.memberships[orgID][userID]; ok {
		return nil
	}
	f.memberships[orgID][userID] = domain.Membership{
		OrgID:     orgID,
		UserID:    userID,
		Status:    status,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return nil
}

func (f *fakeOrgStore) UpdateMemberStatus(_ context.Context, orgID, userID string, status domain.MembershipStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	membership, ok := f.memberships[orgID][userID]
	if !ok {
		return domain.ErrNotMember
	}
	if status != domain.MembershipActive && membership.Status == domain.MembershipActive {
		if f.countActiveLocked(orgID) <= 1 {
			return domain.ErrLastActiveMember
		}
	}
	membership.Status = status
	membership.UpdatedAt = time.Now().UTC()
	f.memberships[orgID][userID] = membership
	return nil
}

func (f *fakeOrgStore) RemoveMember(_ context.Context, orgID, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	membership, ok := f.memberships[orgID][userID]
	if !ok {
		return domain.ErrNotMember
	}
	if membership.Status == domain.MembershipActive && f.countActiveLocked(orgID) <= 1 {
		return domain.ErrLastActiveMember
	}
	delete(f.memberships[orgID], userID)
	return nil
}

func (f *fakeOrgStore) GetMembership(_ context.Context, orgID, userID string) (domain.Membership, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	membership, ok := f.memberships[orgID][userID]
	if !ok {
		return domain.Membership{}, domain.ErrNotMember
	}
	return membership, nil
}

func (f *fakeOrgStore) countActiveLocked(orgID string) int {
	active := 0
	for _, membership := range f.memberships[orgID] {
		if membership.Status == domain.MembershipActive {
			active++
		}
	}
	return active
}

func (f *fakeOrgStore) seedMembership(m domain.Membership) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.memberships[m.OrgID] == nil {
		f.memberships[m.OrgID] = make(map[string]domain.Membership)
	}
	f.memberships[m.OrgID][m.UserID] = m
}

// fakeOrgUsers treats every id as an existing user unless it is listed in
// missing, so the existing organization tests can keep using readable ids.
type fakeOrgUsers struct {
	*fakeUsers
	missing map[string]bool
}

func (f fakeOrgUsers) FindByID(_ context.Context, userID string) (domain.User, error) {
	if f.missing[userID] {
		return domain.User{}, domain.ErrUserNotFound
	}
	return domain.User{ID: userID}, nil
}

func newOrgService(t *testing.T, orgs domain.OrgStore) *Service {
	t.Helper()
	return NewService(Deps{
		Orgs:   orgs,
		Users:  fakeOrgUsers{fakeUsers: &fakeUsers{}},
		Clock:  fixedClock{at: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)},
		IDs:    testUUIDs{},
		Logger: func(string, ...any) {},
	})
}

func TestCreateOrgAddsCreatorAsActiveMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)

	org, err := svc.CreateOrg(context.Background(), "user-1", "  Acme  ")
	if err != nil {
		t.Fatal(err)
	}
	if org.Name != "Acme" {
		t.Fatalf("name = %q, want Acme", org.Name)
	}
	if org.ID == "" {
		t.Fatal("organization has no id")
	}
	want := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if !org.CreatedAt.Equal(want) || !org.UpdatedAt.Equal(want) {
		t.Fatalf("timestamps = %v/%v, want %v", org.CreatedAt, org.UpdatedAt, want)
	}
	membership, err := store.GetMembership(context.Background(), org.ID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("creator status = %q, want active", membership.Status)
	}
}

func TestCreateOrgRejectsBlankName(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)

	if _, err := svc.CreateOrg(context.Background(), "user-1", "   "); !errors.Is(err, domain.ErrInvalidOrgName) {
		t.Fatalf("err = %v, want ErrInvalidOrgName", err)
	}
	if len(store.orgs) != 0 {
		t.Fatalf("blank name created %d organizations", len(store.orgs))
	}
}

func TestCreateOrgRequiresCreator(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)

	if _, err := svc.CreateOrg(context.Background(), "", "Acme"); err == nil {
		t.Fatal("expected an error for a missing creator")
	}
}

func TestListOrgsForUserReturnsOnlyActiveMemberships(t *testing.T) {
	store := newFakeOrgStore()
	now := time.Now().UTC()
	store.orgs["org-1"] = domain.Organization{ID: "org-1", Name: "Active", CreatedAt: now, UpdatedAt: now}
	store.orgs["org-2"] = domain.Organization{ID: "org-2", Name: "Suspended", CreatedAt: now, UpdatedAt: now}
	store.seedMembership(domain.Membership{OrgID: "org-1", UserID: "user-1", Status: domain.MembershipActive})
	store.seedMembership(domain.Membership{OrgID: "org-2", UserID: "user-1", Status: domain.MembershipSuspended})
	svc := newOrgService(t, store)

	orgs, err := svc.ListOrgsForUser(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0].ID != "org-1" {
		t.Fatalf("orgs = %+v, want only org-1", orgs)
	}

	orgs, err = svc.ListOrgsForUser(context.Background(), "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 0 {
		t.Fatalf("non-member sees %d organizations", len(orgs))
	}
}

func TestListMembersReturnsEveryMembershipWithStatus(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(context.Background(), org.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "member"); err != nil {
		t.Fatal(err)
	}
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "invited", Status: domain.MembershipInvited})

	members, err := svc.ListMembers(context.Background(), org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %d, want 3", len(members))
	}
	statuses := make(map[string]domain.MembershipStatus, len(members))
	for _, membership := range members {
		statuses[membership.UserID] = membership.Status
	}
	want := map[string]domain.MembershipStatus{
		"owner":   domain.MembershipActive,
		"member":  domain.MembershipSuspended,
		"invited": domain.MembershipInvited,
	}
	for userID, status := range want {
		if statuses[userID] != status {
			t.Fatalf("status[%q] = %q, want %q", userID, statuses[userID], status)
		}
	}
}

func TestListMembersRejectsUnknownOrganization(t *testing.T) {
	svc := newOrgService(t, newFakeOrgStore())
	if _, err := svc.ListMembers(context.Background(), "missing"); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("err = %v, want ErrOrgNotFound", err)
	}
}

func TestListMembersRequiresOrganization(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	if _, err := svc.ListMembers(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for a missing organization id")
	}
	if len(store.memberships) != 0 {
		t.Fatal("blank organization id reached the store")
	}
}

func TestAddMemberMakesUserActive(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(context.Background(), org.ID, "user-2")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("status = %q, want active", membership.Status)
	}
	active, err := svc.IsActiveMember(context.Background(), org.ID, "user-2")
	if err != nil || !active {
		t.Fatalf("IsActiveMember = %v, %v", active, err)
	}
}

func TestAddMemberDoesNotReactivateSuspendedMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(context.Background(), org.ID, "user-2")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipSuspended {
		t.Fatalf("status = %q, want suspended", membership.Status)
	}
}

func TestAddMemberRejectsUnknownOrganization(t *testing.T) {
	svc := newOrgService(t, newFakeOrgStore())
	if err := svc.AddMember(context.Background(), "missing", "user-2"); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("err = %v, want ErrOrgNotFound", err)
	}
}

func TestMemberManagementRejectsUnknownUser(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	svc.users = fakeOrgUsers{
		fakeUsers: &fakeUsers{},
		missing:   map[string]bool{"missing": true},
	}
	ctx := context.Background()

	calls := []func() error{
		func() error { return svc.AddMember(ctx, org.ID, "missing") },
		func() error { return svc.SuspendMember(ctx, org.ID, "missing") },
		func() error { return svc.RemoveMember(ctx, org.ID, "missing") },
	}
	for _, call := range calls {
		if err := call(); !errors.Is(err, domain.ErrUserNotFound) {
			t.Fatalf("err = %v, want ErrUserNotFound", err)
		}
	}
	if _, err := store.GetMembership(ctx, org.ID, "missing"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("unknown user left a membership: %v", err)
	}
}

func TestSuspendMemberKeepsLastActiveMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.SuspendMember(context.Background(), org.ID, "owner"); !errors.Is(err, domain.ErrLastActiveMember) {
		t.Fatalf("err = %v, want ErrLastActiveMember", err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(context.Background(), org.ID, "user-2")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipSuspended {
		t.Fatalf("status = %q, want suspended", membership.Status)
	}
	active, err := svc.IsActiveMember(context.Background(), org.ID, "user-2")
	if err != nil || active {
		t.Fatalf("suspended member IsActiveMember = %v, %v", active, err)
	}

	// Suspending an already suspended member is a no-op.
	if err := svc.SuspendMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatalf("second suspend: %v", err)
	}
}

func TestSuspendMemberRejectsNonMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "stranger"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestRemoveMemberKeepsLastActiveMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(context.Background(), org.ID, "owner"); !errors.Is(err, domain.ErrLastActiveMember) {
		t.Fatalf("err = %v, want ErrLastActiveMember", err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMembership(context.Background(), org.ID, "user-2"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("removed membership err = %v", err)
	}
}

func TestRemoveMemberAllowsNonActiveMembership(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "user-2", Status: domain.MembershipSuspended})

	if err := svc.RemoveMember(context.Background(), org.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveMemberRejectsNonMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(context.Background(), org.ID, "stranger"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestIsActiveMemberStates(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "suspended", Status: domain.MembershipSuspended})
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "invited", Status: domain.MembershipInvited})

	tests := []struct {
		name   string
		userID string
		want   bool
	}{
		{"active", "owner", true},
		{"suspended", "suspended", false},
		{"invited", "invited", false},
		{"non-member", "stranger", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.IsActiveMember(context.Background(), org.ID, tc.userID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("IsActiveMember = %v, want %v", got, tc.want)
			}
		})
	}
}
