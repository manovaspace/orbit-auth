package memstore_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/manovaspace/orbit-auth/authorization"
	"github.com/manovaspace/orbit-auth/authorization/memstore"
)

func TestRolesAreScopedAndCopied(t *testing.T) {
	store := memstore.New()
	roles := []authorization.RoleDef{
		{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read"}},
	}
	store.SetRoles("acme", roles)

	roles[0].Permissions[0] = "inventory.stock.move"

	got, err := store.Roles(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Roles() error = %v", err)
	}
	if got[0].Permissions[0] != "inventory.stock.read" {
		t.Fatalf("Roles() = %v, want the role copied at SetRoles time", got)
	}

	got[0].Permissions[0] = "inventory.stock.move"
	again, err := store.Roles(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Roles() error = %v", err)
	}
	if again[0].Permissions[0] != "inventory.stock.read" {
		t.Fatal("Roles() exposed the store's internal slice")
	}

	other, err := store.Roles(context.Background(), "globex")
	if err != nil {
		t.Fatalf("Roles() error = %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("Roles() for an unprovisioned org = %v, want empty", other)
	}
}

func TestAddRoleReplacesByKey(t *testing.T) {
	store := memstore.New()
	store.AddRole("acme", authorization.RoleDef{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read"}})
	store.AddRole("acme", authorization.RoleDef{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.move"}})
	store.AddRole("acme", authorization.RoleDef{Key: "viewer", Permissions: []authorization.Permission{"inventory.stock.read"}})

	roles, err := store.Roles(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Roles() error = %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("Roles() = %v, want two roles", roles)
	}
	if roles[0].Key != "manager" || !reflect.DeepEqual(roles[0].Permissions, []authorization.Permission{"inventory.stock.move"}) {
		t.Fatalf("manager = %v, want the replacement permissions", roles[0])
	}
}

func TestAssignmentsAreScopedAndCopied(t *testing.T) {
	store := memstore.New()
	store.SetAssignments("acme", "user-1", []authorization.Assignment{
		{RoleKey: "manager", Scope: authorization.Scope{Type: "location", ID: "L1"}},
	})
	store.AddAssignment(authorization.Assignment{
		OrgID: "acme", Subject: "user-2", RoleKey: "viewer", Scope: authorization.Scope{},
	})
	store.AddAssignment(authorization.Assignment{
		OrgID: "globex", Subject: "user-1", RoleKey: "viewer", Scope: authorization.Scope{},
	})

	got, err := store.Assignments(context.Background(), "acme", "user-1")
	if err != nil {
		t.Fatalf("Assignments() error = %v", err)
	}
	want := []authorization.Assignment{
		{OrgID: "acme", Subject: "user-1", RoleKey: "manager", Scope: authorization.Scope{Type: "location", ID: "L1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Assignments() = %v, want %v", got, want)
	}

	got[0].RoleKey = "viewer"
	again, err := store.Assignments(context.Background(), "acme", "user-1")
	if err != nil {
		t.Fatalf("Assignments() error = %v", err)
	}
	if again[0].RoleKey != "manager" {
		t.Fatal("Assignments() exposed the store's internal slice")
	}

	other, err := store.Assignments(context.Background(), "acme", "user-3")
	if err != nil {
		t.Fatalf("Assignments() error = %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("Assignments() for an unknown subject = %v, want empty", other)
	}
}

func TestContextCancellation(t *testing.T) {
	store := memstore.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Roles(ctx, "acme"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Roles() error = %v, want context.Canceled", err)
	}
	if _, err := store.Assignments(ctx, "acme", "user-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Assignments() error = %v, want context.Canceled", err)
	}
}
