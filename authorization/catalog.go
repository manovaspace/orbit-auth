package authorization

import (
	"fmt"
	"sort"
)

// PermissionSpec declares one permission a module knows about. Keys use the
// product's `<module>.<resource>.<action>` convention and the same identifier
// rules as roles.
type PermissionSpec struct {
	Key         Permission
	Description string
}

// Catalog is an immutable set of permissions. A permission that is not in the
// catalog is never granted, even if a role references it.
type Catalog struct {
	specs map[Permission]PermissionSpec
}

// NewCatalog validates and copies the declared permissions. It rejects an
// invalid identifier and a duplicate key.
func NewCatalog(perms ...PermissionSpec) (*Catalog, error) {
	catalog := &Catalog{specs: make(map[Permission]PermissionSpec, len(perms))}
	for _, spec := range perms {
		if err := validateIdentifier("permission", string(spec.Key)); err != nil {
			return nil, err
		}
		if _, exists := catalog.specs[spec.Key]; exists {
			return nil, fmt.Errorf("duplicate permission %q", spec.Key)
		}
		catalog.specs[spec.Key] = spec
	}
	return catalog, nil
}

// Has reports whether the permission is in the catalog. A nil catalog has no
// permissions.
func (c *Catalog) Has(permission Permission) bool {
	if c == nil {
		return false
	}
	_, ok := c.specs[permission]
	return ok
}

// All returns every permission spec sorted by key. Callers may mutate the
// returned slice; the catalog keeps its own copy.
func (c *Catalog) All() []PermissionSpec {
	if c == nil {
		return []PermissionSpec{}
	}
	specs := make([]PermissionSpec, 0, len(c.specs))
	for _, spec := range c.specs {
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Key < specs[j].Key })
	return specs
}

// ValidateRoles checks that roles use valid identifiers and that every
// permission they grant is declared in the catalog. It is meant to run when a
// module seeds system roles or validates a role from an API request.
func ValidateRoles(catalog *Catalog, roles []RoleDef) error {
	if catalog == nil {
		return fmt.Errorf("authorization catalog is required")
	}

	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if err := validateIdentifier("role", role.Key); err != nil {
			return err
		}
		if _, exists := seen[role.Key]; exists {
			return fmt.Errorf("duplicate role %q", role.Key)
		}
		seen[role.Key] = struct{}{}

		for _, permission := range role.Permissions {
			if err := validateIdentifier("permission", string(permission)); err != nil {
				return err
			}
			if !catalog.Has(permission) {
				return fmt.Errorf("role %q references unknown permission %q", role.Key, permission)
			}
		}
	}
	return nil
}
