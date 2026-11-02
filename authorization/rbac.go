package authorization

import (
	"context"
	"fmt"
	"sort"
)

const (
	reasonNoOrg             = "no_org"
	reasonOrgMismatch       = "org_mismatch"
	reasonUnknownPermission = "unknown_permission"
	reasonNoGrant           = "no_grant"
	reasonGranted           = "granted"
)

// Scope identifies where an assignment applies. Type "" means the whole
// organization, in which case ID is ignored. Any other type, such as
// "location", makes the assignment apply only to a resource that lists the
// same scope.
type Scope struct {
	Type string
	ID   string
}

// Resource is the thing a principal wants to act on. OrgID is the
// organization that owns it and Scopes lists the resources it belongs to, for
// example the location of a stock movement.
type Resource struct {
	OrgID  string
	Scopes []Scope
}

// RoleDef is one role as stored by a product. System roles are seeded from
// code and cannot be edited; custom roles pick permissions from the catalog.
type RoleDef struct {
	Key         string
	Permissions []Permission
	System      bool
}

// Assignment grants RoleKey to Subject in OrgID, limited to Scope. A zero
// Scope grants the role across the whole organization.
type Assignment struct {
	OrgID   string
	Subject string
	RoleKey string
	Scope   Scope
}

// Principal is the authenticated subject acting in an organization.
type Principal struct {
	Subject string
	OrgID   string
}

// Decision is the result of one authorization check. Reason is one of
// "no_org", "org_mismatch", "unknown_permission", "no_grant" or "granted".
type Decision struct {
	Allowed bool
	Reason  string
}

// Authorizer answers and lists authorization decisions. Products depend on
// this interface and NewRBAC supplies the in-process implementation; a
// relationship engine may replace it later without changing call sites.
type Authorizer interface {
	// Check reports whether Principal may use Permission on Resource. Deny is
	// the default. An error means the store could not be read and the caller
	// must treat the decision as denied.
	Check(ctx context.Context, p Principal, permission Permission, r Resource) (Decision, error)

	// AllowedScopes reports where Principal holds Permission. When all is
	// true the permission is held organization-wide and ids is empty.
	// Otherwise ids holds the sorted, deduplicated IDs of the requested scope
	// type where the permission is held; an empty slice means nowhere.
	AllowedScopes(ctx context.Context, p Principal, permission Permission, scopeType string) (all bool, ids []string, err error)
}

// Store is the port a product implements over its own tables. Both methods
// must already be scoped by orgID; Check never queries another organization.
type Store interface {
	Roles(ctx context.Context, orgID string) ([]RoleDef, error)
	Assignments(ctx context.Context, orgID, subject string) ([]Assignment, error)
}

type rbac struct {
	catalog *Catalog
	store   Store
}

// NewRBAC returns an in-process Role Based Access Control Authorizer backed by
// the product's Store. It has no hidden state and is safe for concurrent use
// as long as the Store is.
func NewRBAC(catalog *Catalog, store Store) Authorizer {
	return &rbac{catalog: catalog, store: store}
}

// Check evaluates an organization-scoped permission and denies by default.
func (r *rbac) Check(ctx context.Context, p Principal, permission Permission, resource Resource) (Decision, error) {
	if p.OrgID == "" {
		return deny(reasonNoOrg), nil
	}
	if resource.OrgID != p.OrgID {
		return deny(reasonOrgMismatch), nil
	}
	if p.Subject == "" {
		return deny(reasonNoGrant), nil
	}
	if !r.hasPermission(permission) {
		return deny(reasonUnknownPermission), nil
	}
	if r == nil || r.store == nil {
		return deny(reasonNoGrant), nil
	}

	roles, err := r.store.Roles(ctx, p.OrgID)
	if err != nil {
		return deny(reasonNoGrant), fmt.Errorf("authorization: list roles for org %q: %w", p.OrgID, err)
	}
	assignments, err := r.store.Assignments(ctx, p.OrgID, p.Subject)
	if err != nil {
		return deny(reasonNoGrant), fmt.Errorf("authorization: list assignments for subject %q in org %q: %w", p.Subject, p.OrgID, err)
	}

	byRole := permissionsByRole(roles)
	for _, assignment := range assignments {
		if assignment.OrgID != p.OrgID || assignment.Subject != p.Subject {
			continue
		}
		permissions, ok := byRole[assignment.RoleKey]
		if !ok {
			continue
		}
		if _, ok := permissions[permission]; !ok {
			continue
		}
		if assignmentApplies(assignment.Scope, resource.Scopes) {
			return Decision{Allowed: true, Reason: reasonGranted}, nil
		}
	}
	return deny(reasonNoGrant), nil
}

// AllowedScopes lists the scopes of one type where a principal holds a
// permission, or reports that it is held organization-wide. It is the bulk
// companion to Check for list endpoints.
func (r *rbac) AllowedScopes(ctx context.Context, p Principal, permission Permission, scopeType string) (bool, []string, error) {
	if p.OrgID == "" || p.Subject == "" {
		return false, nil, nil
	}
	if !r.hasPermission(permission) {
		return false, nil, nil
	}
	if r == nil || r.store == nil {
		return false, []string{}, nil
	}

	roles, err := r.store.Roles(ctx, p.OrgID)
	if err != nil {
		return false, nil, fmt.Errorf("authorization: list roles for org %q: %w", p.OrgID, err)
	}
	assignments, err := r.store.Assignments(ctx, p.OrgID, p.Subject)
	if err != nil {
		return false, nil, fmt.Errorf("authorization: list assignments for subject %q in org %q: %w", p.Subject, p.OrgID, err)
	}

	byRole := permissionsByRole(roles)
	ids := make(map[string]struct{})
	for _, assignment := range assignments {
		if assignment.OrgID != p.OrgID || assignment.Subject != p.Subject {
			continue
		}
		permissions, ok := byRole[assignment.RoleKey]
		if !ok {
			continue
		}
		if _, ok := permissions[permission]; !ok {
			continue
		}
		if assignment.Scope.Type == "" {
			return true, nil, nil
		}
		if assignment.Scope.Type == scopeType && assignment.Scope.ID != "" {
			ids[assignment.Scope.ID] = struct{}{}
		}
	}

	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return false, result, nil
}

// CanGrant reports whether a granter may hand out target role, following ADR
// 045 D7: they must hold every permission of the target role on the target
// scope or organization-wide. A granter cannot grant across organizations.
func CanGrant(granter []Assignment, roles []RoleDef, target Assignment) bool {
	if target.OrgID == "" {
		return false
	}
	byRole := permissionsByRole(roles)
	targetPermissions, ok := byRole[target.RoleKey]
	if !ok {
		return false
	}

	for permission := range targetPermissions {
		held := false
		for _, assignment := range granter {
			if assignment.OrgID != target.OrgID {
				continue
			}
			permissions, ok := byRole[assignment.RoleKey]
			if !ok {
				continue
			}
			if _, ok := permissions[permission]; !ok {
				continue
			}
			if assignment.Scope.Type == "" || assignment.Scope == target.Scope {
				held = true
				break
			}
		}
		if !held {
			return false
		}
	}
	return true
}

func (r *rbac) hasPermission(permission Permission) bool {
	return r != nil && r.catalog != nil && r.catalog.Has(permission)
}

func deny(reason string) Decision {
	return Decision{Allowed: false, Reason: reason}
}

func assignmentApplies(scope Scope, scopes []Scope) bool {
	if scope.Type == "" {
		return true
	}
	for _, resourceScope := range scopes {
		if resourceScope == scope {
			return true
		}
	}
	return false
}

func permissionsByRole(roles []RoleDef) map[string]map[Permission]struct{} {
	byRole := make(map[string]map[Permission]struct{}, len(roles))
	for _, role := range roles {
		permissions := byRole[role.Key]
		if permissions == nil {
			permissions = make(map[Permission]struct{}, len(role.Permissions))
			byRole[role.Key] = permissions
		}
		for _, permission := range role.Permissions {
			permissions[permission] = struct{}{}
		}
	}
	return byRole
}
