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
	roleChanges []roleChange
	// slugs maps every slug an organization has held to its id.
	slugs       map[string]string
	slugChanges []string
}

// roleChange records one UpdateMemberRole call.
type roleChange struct {
	orgID, userID, actorID string
	role                   domain.MembershipRole
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
			Role:      domain.RoleOwner,
			CreatedAt: org.CreatedAt,
			UpdatedAt: org.UpdatedAt,
		},
	}
	return nil
}

func (f *fakeOrgStore) SlugAvailable(_ context.Context, slug string) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, taken := f.slugs[slug]; taken {
		return false, nil
	}
	for _, org := range f.orgs {
		if org.Slug == slug {
			return false, nil
		}
	}
	return true, nil
}

func (f *fakeOrgStore) SetOrgSlug(_ context.Context, orgID, slug string, by domain.MembershipActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	org, ok := f.orgs[orgID]
	if !ok {
		return domain.ErrOrgNotFound
	}
	if owner, taken := f.slugs[slug]; taken && owner != orgID {
		return domain.ErrSlugTaken
	}
	if org.Slug == slug {
		return nil
	}
	if f.slugs == nil {
		f.slugs = map[string]string{}
	}
	f.slugs[org.Slug] = orgID
	f.slugs[slug] = orgID
	org.Slug = slug
	f.orgs[orgID] = org
	f.slugChanges = append(f.slugChanges, by.CallerID+":"+slug)
	return nil
}

func (f *fakeOrgStore) ResolveSlug(_ context.Context, slug string) (domain.Organization, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if orgID, ok := f.slugs[slug]; ok {
		return f.orgs[orgID], nil
	}
	for _, org := range f.orgs {
		if org.Slug == slug {
			return org, nil
		}
	}
	return domain.Organization{}, domain.ErrSlugNotFound
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

func (f *fakeOrgStore) AddMember(_ context.Context, orgID, userID string, status domain.MembershipStatus, role domain.MembershipRole) error {
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
		Role:      role,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return nil
}

func (f *fakeOrgStore) UpdateMemberStatus(_ context.Context, orgID, userID string, status domain.MembershipStatus, _ domain.MembershipActor) error {
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
		if f.lastActiveOwnerLocked(orgID, membership) {
			return domain.ErrLastOwner
		}
	}
	membership.Status = status
	membership.UpdatedAt = time.Now().UTC()
	f.memberships[orgID][userID] = membership
	return nil
}

func (f *fakeOrgStore) RemoveMember(_ context.Context, orgID, userID string, _ domain.MembershipActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	membership, ok := f.memberships[orgID][userID]
	if !ok {
		return domain.ErrNotMember
	}
	if membership.Status == domain.MembershipActive && f.countActiveLocked(orgID) <= 1 {
		return domain.ErrLastActiveMember
	}
	if membership.Status == domain.MembershipActive && f.lastActiveOwnerLocked(orgID, membership) {
		return domain.ErrLastOwner
	}
	delete(f.memberships[orgID], userID)
	return nil
}

func (f *fakeOrgStore) UpdateMemberRole(_ context.Context, orgID, userID string, role domain.MembershipRole, actorID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	membership, ok := f.memberships[orgID][userID]
	if !ok {
		return domain.ErrNotMember
	}
	if membership.Role == role {
		return nil
	}
	if role != domain.RoleOwner && f.lastActiveOwnerLocked(orgID, membership) {
		return domain.ErrLastOwner
	}
	membership.Role = role
	membership.UpdatedAt = time.Now().UTC()
	f.memberships[orgID][userID] = membership
	f.roleChanges = append(f.roleChanges, roleChange{orgID: orgID, userID: userID, role: role, actorID: actorID})
	return nil
}

func (f *fakeOrgStore) lastActiveOwnerLocked(orgID string, target domain.Membership) bool {
	if target.Role != domain.RoleOwner || target.Status != domain.MembershipActive {
		return false
	}
	owners := 0
	for _, membership := range f.memberships[orgID] {
		if membership.Role == domain.RoleOwner && membership.Status == domain.MembershipActive {
			owners++
		}
	}
	return owners <= 1
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
	if m.Role == "" {
		m.Role = domain.RoleMember
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

	org, err := svc.CreateOrg(context.Background(), "user-1", "  Acme  ", "")
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

	if _, err := svc.CreateOrg(context.Background(), "user-1", "   ", ""); !errors.Is(err, domain.ErrInvalidOrgName) {
		t.Fatalf("err = %v, want ErrInvalidOrgName", err)
	}
	if len(store.orgs) != 0 {
		t.Fatalf("blank name created %d organizations", len(store.orgs))
	}
}

func TestCreateOrgRequiresCreator(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)

	if _, err := svc.CreateOrg(context.Background(), "", "Acme", ""); err == nil {
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
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(context.Background(), org.ID, "owner", "member"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "member"); err != nil {
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
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
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
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
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
	if err := svc.AddMember(context.Background(), "missing", "owner", "user-2"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestMemberManagementRejectsUnknownUser(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.users = fakeOrgUsers{
		fakeUsers: &fakeUsers{},
		missing:   map[string]bool{"missing": true},
	}
	ctx := context.Background()

	calls := []func() error{
		func() error { return svc.AddMember(ctx, org.ID, "owner", "missing") },
		func() error { return svc.SuspendMember(ctx, org.ID, "owner", "missing") },
		func() error { return svc.RemoveMember(ctx, org.ID, "owner", "missing") },
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
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "owner"); !errors.Is(err, domain.ErrLastActiveMember) {
		t.Fatalf("err = %v, want ErrLastActiveMember", err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
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
	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatalf("second suspend: %v", err)
	}
}

func TestSuspendMemberRejectsNonMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "stranger"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestRemoveMemberKeepsLastActiveMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(context.Background(), org.ID, "owner", "owner"); !errors.Is(err, domain.ErrLastActiveMember) {
		t.Fatalf("err = %v, want ErrLastActiveMember", err)
	}

	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMembership(context.Background(), org.ID, "user-2"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("removed membership err = %v", err)
	}
}

func TestRemoveMemberAllowsNonActiveMembership(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "user-2", Status: domain.MembershipSuspended})

	if err := svc.RemoveMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveMemberRejectsNonMember(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(context.Background(), org.ID, "owner", "stranger"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestIsActiveMemberStates(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
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

// orgWithRoles creates an organization owned by "owner" and seeds one active
// member per role: "admin", "staff" (a plain member) and "second-owner".
func orgWithRoles(t *testing.T) (*Service, *fakeOrgStore, string) {
	t.Helper()
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "admin", Status: domain.MembershipActive, Role: domain.RoleAdmin})
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "staff", Status: domain.MembershipActive, Role: domain.RoleMember})
	store.seedMembership(domain.Membership{OrgID: org.ID, UserID: "other", Status: domain.MembershipActive, Role: domain.RoleMember})
	return svc, store, org.ID
}

func TestMemberCannotChangeOtherMembers(t *testing.T) {
	svc, store, orgID := orgWithRoles(t)
	ctx := context.Background()

	calls := map[string]func() error{
		"add":     func() error { return svc.AddMember(ctx, orgID, "staff", "new-user") },
		"suspend": func() error { return svc.SuspendMember(ctx, orgID, "staff", "other") },
		"remove":  func() error { return svc.RemoveMember(ctx, orgID, "staff", "admin") },
		"role": func() error {
			_, err := svc.SetMemberRole(ctx, orgID, "staff", "other", domain.RoleAdmin)
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s err = %v, want ErrForbidden", name, err)
		}
	}
	if m, err := store.GetMembership(ctx, orgID, "admin"); err != nil || m.Status != domain.MembershipActive {
		t.Fatalf("admin membership after forbidden remove = %+v, %v", m, err)
	}
	if _, err := store.GetMembership(ctx, orgID, "new-user"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("forbidden add left a membership: %v", err)
	}
}

func TestAdminCannotTouchAdminsOrOwners(t *testing.T) {
	svc, store, orgID := orgWithRoles(t)
	ctx := context.Background()
	store.seedMembership(domain.Membership{OrgID: orgID, UserID: "admin-2", Status: domain.MembershipActive, Role: domain.RoleAdmin})

	if err := svc.RemoveMember(ctx, orgID, "admin", "admin-2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin removes admin: %v, want ErrForbidden", err)
	}
	if err := svc.SuspendMember(ctx, orgID, "admin", "owner"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin suspends owner: %v, want ErrForbidden", err)
	}
	if _, err := svc.SetMemberRole(ctx, orgID, "admin", "staff", domain.RoleAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin promotes: %v, want ErrForbidden", err)
	}
	if err := svc.RemoveMember(ctx, orgID, "admin", "staff"); err != nil {
		t.Fatalf("admin removes plain member: %v", err)
	}
}

func TestSetMemberRoleByOwner(t *testing.T) {
	svc, store, orgID := orgWithRoles(t)
	ctx := context.Background()

	membership, err := svc.SetMemberRole(ctx, orgID, "owner", "staff", domain.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Role != domain.RoleAdmin {
		t.Fatalf("role = %q, want admin", membership.Role)
	}
	if len(store.roleChanges) != 1 || store.roleChanges[0].actorID != "owner" || store.roleChanges[0].userID != "staff" {
		t.Fatalf("role changes = %+v", store.roleChanges)
	}
	if _, err := svc.SetMemberRole(ctx, orgID, "owner", "staff", "root"); !errors.Is(err, domain.ErrInvalidRole) {
		t.Fatalf("unknown role err = %v, want ErrInvalidRole", err)
	}
	if _, err := svc.SetMemberRole(ctx, orgID, "owner", "stranger", domain.RoleAdmin); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("non-member target err = %v, want ErrNotMember", err)
	}
}

func TestLastOwnerCannotBeDemotedRemovedOrSuspended(t *testing.T) {
	svc, _, orgID := orgWithRoles(t)
	ctx := context.Background()

	if _, err := svc.SetMemberRole(ctx, orgID, "owner", "owner", domain.RoleAdmin); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("demote last owner err = %v, want ErrLastOwner", err)
	}
	if err := svc.SuspendMember(ctx, orgID, "owner", "owner"); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("suspend last owner err = %v, want ErrLastOwner", err)
	}
	if err := svc.RemoveMember(ctx, orgID, "owner", "owner"); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("remove last owner err = %v, want ErrLastOwner", err)
	}

	if _, err := svc.SetMemberRole(ctx, orgID, "owner", "admin", domain.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetMemberRole(ctx, orgID, "owner", "owner", domain.RoleAdmin); err != nil {
		t.Fatalf("demote with a second owner: %v", err)
	}
}

func TestMemberCanLeaveTheOrganization(t *testing.T) {
	svc, store, orgID := orgWithRoles(t)
	ctx := context.Background()

	if err := svc.RemoveMember(ctx, orgID, "staff", "staff"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if _, err := store.GetMembership(ctx, orgID, "staff"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("membership after leaving: %v", err)
	}
}

func TestSuspendedOwnerCannotAdminister(t *testing.T) {
	svc, store, orgID := orgWithRoles(t)
	ctx := context.Background()
	store.seedMembership(domain.Membership{OrgID: orgID, UserID: "ghost", Status: domain.MembershipSuspended, Role: domain.RoleOwner})

	if err := svc.AddMember(ctx, orgID, "ghost", "new-user"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("suspended owner add err = %v, want ErrForbidden", err)
	}
}

func TestCreateOrgAllocatesASlug(t *testing.T) {
	ctx := context.Background()
	store := newFakeOrgStore()
	svc := newOrgService(t, store)

	derived, err := svc.CreateOrg(ctx, "u1", "Acme Cafe", "")
	if err != nil || derived.Slug != "acme-cafe" {
		t.Fatalf("derived slug = %q, %v; want acme-cafe", derived.Slug, err)
	}
	explicit, err := svc.CreateOrg(ctx, "u1", "Whatever", "  Fryto ")
	if err != nil || explicit.Slug != "fryto" {
		t.Fatalf("explicit slug = %q, %v; want fryto", explicit.Slug, err)
	}
	// The same derived name collides, so the id-based fallback is used.
	clash, err := svc.CreateOrg(ctx, "u1", "Acme Cafe", "")
	if err != nil || clash.Slug != domain.FallbackSlug(clash.ID) {
		t.Fatalf("clashing slug = %q, %v; want the id-based fallback", clash.Slug, err)
	}
	// A name with no usable ASCII also falls back.
	persian, err := svc.CreateOrg(ctx, "u1", "فرایتو", "")
	if err != nil || persian.Slug != domain.FallbackSlug(persian.ID) {
		t.Fatalf("persian slug = %q, %v; want the id-based fallback", persian.Slug, err)
	}
	// An explicit slug is never silently replaced.
	if _, err := svc.CreateOrg(ctx, "u1", "Other", "fryto"); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("taken slug err = %v, want ErrSlugTaken", err)
	}
	for _, bad := range []string{"www", "Bad_Slug", "a", "ab--cd"} {
		if _, err := svc.CreateOrg(ctx, "u1", "Other", bad); !errors.Is(err, domain.ErrInvalidSlug) {
			t.Fatalf("slug %q err = %v, want ErrInvalidSlug", bad, err)
		}
	}
}

func TestSetOrgSlugNeedsAnOwnerAndKeepsTheOldSlug(t *testing.T) {
	ctx := context.Background()
	svc, store, orgID := orgWithRoles(t)
	seeded := store.orgs[orgID]
	seeded.Slug = "acme"
	store.orgs[orgID] = seeded

	for _, caller := range []string{"admin", "staff"} {
		if _, err := svc.SetOrgSlug(ctx, orgID, caller, "new-name"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s rename err = %v, want ErrForbidden", caller, err)
		}
	}
	if _, err := svc.SetOrgSlug(ctx, orgID, "stranger", "new-name"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("stranger rename err = %v, want ErrNotMember", err)
	}
	if _, err := svc.SetOrgSlug(ctx, orgID, "owner", "Not Valid"); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Fatalf("invalid slug err = %v, want ErrInvalidSlug", err)
	}

	renamed, err := svc.SetOrgSlug(ctx, orgID, "owner", " New-Name ")
	if err != nil || renamed.Slug != "new-name" {
		t.Fatalf("rename = %q, %v; want new-name", renamed.Slug, err)
	}
	resolved, err := svc.ResolveOrg(ctx, "acme")
	if err != nil || resolved.ID != orgID || resolved.Slug != "new-name" {
		t.Fatalf("old slug resolves to %+v, %v; want the same organization with its new slug", resolved, err)
	}
	if _, err := svc.ResolveOrg(ctx, "unknown"); !errors.Is(err, domain.ErrSlugNotFound) {
		t.Fatalf("unknown slug err = %v, want ErrSlugNotFound", err)
	}
	if _, err := svc.ResolveOrg(ctx, "WWW"); !errors.Is(err, domain.ErrSlugNotFound) {
		t.Fatalf("reserved slug err = %v, want ErrSlugNotFound", err)
	}

	other, err := svc.CreateOrg(ctx, "u2", "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetOrgSlug(ctx, other.ID, "u2", "acme"); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("claiming a retired slug err = %v, want ErrSlugTaken", err)
	}
}
