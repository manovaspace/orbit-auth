package authorization_test

import (
	"reflect"
	"testing"

	"github.com/manovaspace/orbit-auth/authorization"
)

func TestCatalog(t *testing.T) {
	catalog, err := authorization.NewCatalog(
		authorization.PermissionSpec{Key: "receipts.write", Description: "Write receipts"},
		authorization.PermissionSpec{Key: "inventory.read", Description: "Read inventory"},
		authorization.PermissionSpec{Key: "receipts.read", Description: "Read receipts"},
	)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}

	if !catalog.Has("receipts.read") {
		t.Fatal("Has() = false for a catalogued permission")
	}
	if catalog.Has("receipts.delete") {
		t.Fatal("Has() = true for an unknown permission")
	}

	want := []authorization.PermissionSpec{
		{Key: "inventory.read", Description: "Read inventory"},
		{Key: "receipts.read", Description: "Read receipts"},
		{Key: "receipts.write", Description: "Write receipts"},
	}
	all := catalog.All()
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("All() = %v, want %v", all, want)
	}

	all[0].Key = "mutated"
	if !reflect.DeepEqual(catalog.All(), want) {
		t.Fatal("All() exposed the catalog's internal slice")
	}
}

func TestNewCatalogRejectsInvalidPermissions(t *testing.T) {
	tests := []struct {
		name  string
		perms []authorization.PermissionSpec
	}{
		{
			name:  "invalid identifier",
			perms: []authorization.PermissionSpec{{Key: "Receipts.Read"}},
		},
		{
			name:  "empty identifier",
			perms: []authorization.PermissionSpec{{Key: ""}},
		},
		{
			name: "duplicate permission",
			perms: []authorization.PermissionSpec{
				{Key: "receipts.read"},
				{Key: "receipts.read"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := authorization.NewCatalog(test.perms...); err == nil {
				t.Fatal("NewCatalog() succeeded for an invalid catalog")
			}
		})
	}
}

func TestEmptyCatalogDeniesEverything(t *testing.T) {
	catalog, err := authorization.NewCatalog()
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	if catalog.Has("receipts.read") {
		t.Fatal("Has() = true for an empty catalog")
	}
	if got := catalog.All(); len(got) != 0 {
		t.Fatalf("All() = %v, want empty", got)
	}
}

func TestNilCatalogFailsClosed(t *testing.T) {
	var catalog *authorization.Catalog
	if catalog.Has("receipts.read") {
		t.Fatal("nil catalog allowed a permission")
	}
	if got := catalog.All(); len(got) != 0 {
		t.Fatalf("nil catalog All() = %v, want empty", got)
	}
}

func TestValidateRoles(t *testing.T) {
	catalog := testCatalog(t)
	valid := []authorization.RoleDef{
		{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read", "inventory.stock.move"}},
		{Key: "viewer", Permissions: []authorization.Permission{"inventory.stock.read"}},
	}
	if err := authorization.ValidateRoles(catalog, valid); err != nil {
		t.Fatalf("ValidateRoles() error = %v", err)
	}

	tests := []struct {
		name  string
		roles []authorization.RoleDef
	}{
		{
			name:  "unknown permission",
			roles: []authorization.RoleDef{{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.delete"}}},
		},
		{
			name:  "invalid role identifier",
			roles: []authorization.RoleDef{{Key: "Manager", Permissions: []authorization.Permission{"inventory.stock.read"}}},
		},
		{
			name:  "invalid permission identifier",
			roles: []authorization.RoleDef{{Key: "manager", Permissions: []authorization.Permission{"Inventory.Stock.Read"}}},
		},
		{
			name: "duplicate role",
			roles: []authorization.RoleDef{
				{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read"}},
				{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.move"}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := authorization.ValidateRoles(catalog, test.roles); err == nil {
				t.Fatal("ValidateRoles() succeeded for invalid roles")
			}
		})
	}
}

func TestValidateRolesRequiresCatalog(t *testing.T) {
	roles := []authorization.RoleDef{
		{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read"}},
	}
	if err := authorization.ValidateRoles(nil, roles); err == nil {
		t.Fatal("ValidateRoles() succeeded without a catalog")
	}
}
