package authorization_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/manovaspace/orbit-auth/authorization"
	"github.com/manovaspace/orbit-auth/authorization/memstore"
)

const (
	orgID      = "acme"
	otherOrgID = "globex"
	subject    = "user-1"
)

type fakeStore struct {
	roles          []authorization.RoleDef
	assignments    []authorization.Assignment
	rolesErr       error
	assignmentsErr error
}

func (s *fakeStore) Roles(context.Context, string) ([]authorization.RoleDef, error) {
	if s.rolesErr != nil {
		return nil, s.rolesErr
	}
	return s.roles, nil
}

func (s *fakeStore) Assignments(context.Context, string, string) ([]authorization.Assignment, error) {
	if s.assignmentsErr != nil {
		return nil, s.assignmentsErr
	}
	return s.assignments, nil
}

func testCatalog(t *testing.T) *authorization.Catalog {
	t.Helper()

	catalog, err := authorization.NewCatalog(
		authorization.PermissionSpec{Key: "inventory.stock.read", Description: "Read stock"},
		authorization.PermissionSpec{Key: "inventory.stock.move", Description: "Move stock"},
		authorization.PermissionSpec{Key: "receipts.read", Description: "Read receipts"},
		authorization.PermissionSpec{Key: "receipts.write", Description: "Write receipts"},
	)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return catalog
}

func testRoles() []authorization.RoleDef {
	return []authorization.RoleDef{
		{
			Key:         "manager",
			Permissions: []authorization.Permission{"inventory.stock.read", "inventory.stock.move"},
			System:      true,
		},
		{
			Key:         "viewer",
			Permissions: []authorization.Permission{"inventory.stock.read"},
		},
	}
}

func TestCheck(t *testing.T) {
	location := func(id string) authorization.Scope {
		return authorization.Scope{Type: "location", ID: id}
	}
	assignment := func(org, roleKey string, scope authorization.Scope) authorization.Assignment {
		return authorization.Assignment{OrgID: org, Subject: subject, RoleKey: roleKey, Scope: scope}
	}

	tests := []struct {
		name        string
		principal   authorization.Principal
		permission  authorization.Permission
		resource    authorization.Resource
		assignments []authorization.Assignment
		allowed     bool
		reason      string
	}{
		{
			name:       "principal without an organization",
			principal:  authorization.Principal{Subject: subject},
			permission: "inventory.stock.read",
			resource:   authorization.Resource{OrgID: orgID},
			reason:     "no_org",
		},
		{
			name:       "resource in another organization",
			principal:  authorization.Principal{Subject: subject, OrgID: orgID},
			permission: "inventory.stock.read",
			resource:   authorization.Resource{OrgID: otherOrgID},
			reason:     "org_mismatch",
		},
		{
			name:       "permission outside the catalog",
			principal:  authorization.Principal{Subject: subject, OrgID: orgID},
			permission: "inventory.stock.delete",
			resource:   authorization.Resource{OrgID: orgID},
			reason:     "unknown_permission",
		},
		{
			name:       "no assignment",
			principal:  authorization.Principal{Subject: subject, OrgID: orgID},
			permission: "inventory.stock.read",
			resource:   authorization.Resource{OrgID: orgID},
			reason:     "no_grant",
		},
		{
			name:        "scoped grant on the matching location",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.move",
			resource:    authorization.Resource{OrgID: orgID, Scopes: []authorization.Scope{location("L1")}},
			assignments: []authorization.Assignment{assignment(orgID, "manager", location("L1"))},
			allowed:     true,
			reason:      "granted",
		},
		{
			name:        "scoped grant on another location",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.move",
			resource:    authorization.Resource{OrgID: orgID, Scopes: []authorization.Scope{location("L2")}},
			assignments: []authorization.Assignment{assignment(orgID, "manager", location("L1"))},
			reason:      "no_grant",
		},
		{
			name:        "scoped grant when the resource lists several scopes",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.move",
			resource:    authorization.Resource{OrgID: orgID, Scopes: []authorization.Scope{location("L1"), location("L2")}},
			assignments: []authorization.Assignment{assignment(orgID, "manager", location("L2"))},
			allowed:     true,
			reason:      "granted",
		},
		{
			name:        "scoped grant on a resource without scopes",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.move",
			resource:    authorization.Resource{OrgID: orgID},
			assignments: []authorization.Assignment{assignment(orgID, "manager", location("L1"))},
			reason:      "no_grant",
		},
		{
			name:        "organization-wide grant covers any location",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.read",
			resource:    authorization.Resource{OrgID: orgID, Scopes: []authorization.Scope{location("L9")}},
			assignments: []authorization.Assignment{assignment(orgID, "viewer", authorization.Scope{})},
			allowed:     true,
			reason:      "granted",
		},
		{
			name:        "assignment from another organization is ignored",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.read",
			resource:    authorization.Resource{OrgID: orgID},
			assignments: []authorization.Assignment{assignment(otherOrgID, "manager", authorization.Scope{})},
			reason:      "no_grant",
		},
		{
			name:        "role is not defined",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.read",
			resource:    authorization.Resource{OrgID: orgID},
			assignments: []authorization.Assignment{assignment(orgID, "ghost", authorization.Scope{})},
			reason:      "no_grant",
		},
		{
			name:        "role does not grant the permission",
			principal:   authorization.Principal{Subject: subject, OrgID: orgID},
			permission:  "inventory.stock.move",
			resource:    authorization.Resource{OrgID: orgID},
			assignments: []authorization.Assignment{assignment(orgID, "viewer", authorization.Scope{})},
			reason:      "no_grant",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auth := authorization.NewRBAC(testCatalog(t), &fakeStore{
				roles:       testRoles(),
				assignments: test.assignments,
			})

			decision, err := auth.Check(context.Background(), test.principal, test.permission, test.resource)
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if decision.Allowed != test.allowed {
				t.Fatalf("Check() allowed = %v, want %v (reason %q)", decision.Allowed, test.allowed, decision.Reason)
			}
			if decision.Reason != test.reason {
				t.Fatalf("Check() reason = %q, want %q", decision.Reason, test.reason)
			}
		})
	}
}

func TestCheckReturnsStoreErrors(t *testing.T) {
	wantErr := errors.New("database down")
	tests := []struct {
		name  string
		store *fakeStore
	}{
		{name: "roles", store: &fakeStore{rolesErr: wantErr}},
		{name: "assignments", store: &fakeStore{assignmentsErr: wantErr}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auth := authorization.NewRBAC(testCatalog(t), test.store)
			decision, err := auth.Check(
				context.Background(),
				authorization.Principal{Subject: subject, OrgID: orgID},
				"inventory.stock.read",
				authorization.Resource{OrgID: orgID},
			)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Check() error = %v, want %v", err, wantErr)
			}
			if decision.Allowed {
				t.Fatal("Check() allowed a permission after a store error")
			}
		})
	}
}

func TestAllowedScopes(t *testing.T) {
	location := func(id string) authorization.Scope {
		return authorization.Scope{Type: "location", ID: id}
	}
	assignment := func(roleKey string, scope authorization.Scope) authorization.Assignment {
		return authorization.Assignment{OrgID: orgID, Subject: subject, RoleKey: roleKey, Scope: scope}
	}
	principal := authorization.Principal{Subject: subject, OrgID: orgID}

	tests := []struct {
		name        string
		assignments []authorization.Assignment
		scopeType   string
		all         bool
		ids         []string
	}{
		{
			name:        "organization-wide",
			assignments: []authorization.Assignment{assignment("viewer", authorization.Scope{})},
			scopeType:   "location",
			all:         true,
		},
		{
			name: "scoped only",
			assignments: []authorization.Assignment{
				assignment("viewer", location("L2")),
				assignment("viewer", location("L1")),
				assignment("viewer", location("L1")),
				assignment("viewer", authorization.Scope{Type: "warehouse", ID: "W1"}),
			},
			scopeType: "location",
			ids:       []string{"L1", "L2"},
		},
		{
			name:      "no assignment",
			scopeType: "location",
			ids:       []string{},
		},
		{
			name:        "other scope type",
			assignments: []authorization.Assignment{assignment("viewer", authorization.Scope{Type: "warehouse", ID: "W1"})},
			scopeType:   "warehouse",
			ids:         []string{"W1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auth := authorization.NewRBAC(testCatalog(t), &fakeStore{
				roles:       testRoles(),
				assignments: test.assignments,
			})

			all, ids, err := auth.AllowedScopes(context.Background(), principal, "inventory.stock.read", test.scopeType)
			if err != nil {
				t.Fatalf("AllowedScopes() error = %v", err)
			}
			if all != test.all {
				t.Fatalf("AllowedScopes() all = %v, want %v", all, test.all)
			}
			if test.all {
				if len(ids) != 0 {
					t.Fatalf("AllowedScopes() ids = %v, want none for an organization-wide grant", ids)
				}
				return
			}
			if !reflect.DeepEqual(ids, test.ids) {
				t.Fatalf("AllowedScopes() ids = %v, want %v", ids, test.ids)
			}
		})
	}
}

func TestAllowedScopesDenies(t *testing.T) {
	auth := authorization.NewRBAC(testCatalog(t), &fakeStore{
		roles: testRoles(),
		assignments: []authorization.Assignment{
			{OrgID: orgID, Subject: subject, RoleKey: "manager", Scope: authorization.Scope{}},
		},
	})

	tests := []struct {
		name       string
		principal  authorization.Principal
		permission authorization.Permission
	}{
		{
			name:       "principal without an organization",
			principal:  authorization.Principal{Subject: subject},
			permission: "inventory.stock.read",
		},
		{
			name:       "permission outside the catalog",
			principal:  authorization.Principal{Subject: subject, OrgID: orgID},
			permission: "inventory.stock.delete",
		},
		{
			name:       "role does not grant the permission",
			principal:  authorization.Principal{Subject: subject, OrgID: orgID},
			permission: "receipts.write",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			all, ids, err := auth.AllowedScopes(context.Background(), test.principal, test.permission, "location")
			if err != nil {
				t.Fatalf("AllowedScopes() error = %v", err)
			}
			if all || len(ids) != 0 {
				t.Fatalf("AllowedScopes() = (%v, %v), want deny", all, ids)
			}
		})
	}
}

func TestCanGrant(t *testing.T) {
	location := func(id string) authorization.Scope {
		return authorization.Scope{Type: "location", ID: id}
	}
	assignment := func(org, roleKey string, scope authorization.Scope) authorization.Assignment {
		return authorization.Assignment{OrgID: org, Subject: "granter", RoleKey: roleKey, Scope: scope}
	}
	target := func(org, roleKey string, scope authorization.Scope) authorization.Assignment {
		return authorization.Assignment{OrgID: org, Subject: "target", RoleKey: roleKey, Scope: scope}
	}

	roles := []authorization.RoleDef{
		{Key: "owner", Permissions: []authorization.Permission{"inventory.stock.read", "inventory.stock.move", "receipts.write"}},
		{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read", "inventory.stock.move"}},
		{Key: "viewer", Permissions: []authorization.Permission{"inventory.stock.read"}},
	}

	tests := []struct {
		name    string
		granter []authorization.Assignment
		target  authorization.Assignment
		allowed bool
	}{
		{
			name:    "organization-wide granter grants a scoped role",
			granter: []authorization.Assignment{assignment(orgID, "owner", authorization.Scope{})},
			target:  target(orgID, "manager", location("L1")),
			allowed: true,
		},
		{
			name:    "scoped granter grants on the same scope",
			granter: []authorization.Assignment{assignment(orgID, "manager", location("L1"))},
			target:  target(orgID, "viewer", location("L1")),
			allowed: true,
		},
		{
			name:    "scoped granter cannot grant on another scope",
			granter: []authorization.Assignment{assignment(orgID, "manager", location("L1"))},
			target:  target(orgID, "viewer", location("L2")),
			allowed: false,
		},
		{
			name:    "scoped granter cannot grant organization-wide",
			granter: []authorization.Assignment{assignment(orgID, "manager", location("L1"))},
			target:  target(orgID, "viewer", authorization.Scope{}),
			allowed: false,
		},
		{
			name:    "granter misses one of the target permissions",
			granter: []authorization.Assignment{assignment(orgID, "viewer", location("L1"))},
			target:  target(orgID, "manager", location("L1")),
			allowed: false,
		},
		{
			name: "permissions held across several assignments",
			granter: []authorization.Assignment{
				assignment(orgID, "viewer", location("L1")),
				assignment(orgID, "manager", location("L1")),
			},
			target:  target(orgID, "manager", location("L1")),
			allowed: true,
		},
		{
			name:    "unknown target role",
			granter: []authorization.Assignment{assignment(orgID, "owner", authorization.Scope{})},
			target:  target(orgID, "ghost", location("L1")),
			allowed: false,
		},
		{
			name:    "granter from another organization",
			granter: []authorization.Assignment{assignment(otherOrgID, "owner", authorization.Scope{})},
			target:  target(orgID, "viewer", location("L1")),
			allowed: false,
		},
		{
			name:    "target without an organization",
			granter: []authorization.Assignment{assignment("", "owner", authorization.Scope{})},
			target:  target("", "viewer", location("L1")),
			allowed: false,
		},
		{
			name:    "unknown granter role",
			granter: []authorization.Assignment{assignment(orgID, "ghost", authorization.Scope{})},
			target:  target(orgID, "viewer", location("L1")),
			allowed: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := authorization.CanGrant(test.granter, roles, test.target); got != test.allowed {
				t.Fatalf("CanGrant() = %v, want %v", got, test.allowed)
			}
		})
	}
}

func TestCheckConcurrent(t *testing.T) {
	store := memstore.New()
	store.SetRoles(orgID, testRoles())
	store.AddAssignment(authorization.Assignment{
		OrgID:   orgID,
		Subject: subject,
		RoleKey: "manager",
		Scope:   authorization.Scope{Type: "location", ID: "L1"},
	})
	auth := authorization.NewRBAC(testCatalog(t), store)

	principal := authorization.Principal{Subject: subject, OrgID: orgID}
	resource := authorization.Resource{OrgID: orgID, Scopes: []authorization.Scope{{Type: "location", ID: "L1"}}}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				decision, err := auth.Check(context.Background(), principal, "inventory.stock.move", resource)
				if err != nil || !decision.Allowed {
					t.Errorf("Check() = (%v, %v), want granted", decision, err)
					return
				}
				all, ids, err := auth.AllowedScopes(context.Background(), principal, "inventory.stock.move", "location")
				if err != nil || all || !reflect.DeepEqual(ids, []string{"L1"}) {
					t.Errorf("AllowedScopes() = (%v, %v, %v), want ([L1], nil)", all, ids, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
