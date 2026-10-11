package authorization

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Role is a product-defined role identifier, such as "admin" or "manager".
type Role string

// Permission is a product-defined action identifier, such as "receipts.read".
type Permission string

// Policy maps product-defined roles to the permissions they grant.
type Policy map[Role][]Permission

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

// Evaluator is an immutable, validated permission policy.
type Evaluator struct {
	permissions map[Role]map[Permission]struct{}
}

// NewEvaluator validates and copies policy so later caller mutations cannot
// change authorization decisions.
func NewEvaluator(policy Policy) (*Evaluator, error) {
	if len(policy) == 0 {
		return nil, fmt.Errorf("authorization policy must define at least one role")
	}

	permissions := make(map[Role]map[Permission]struct{}, len(policy))
	for role, grants := range policy {
		if err := validateIdentifier("role", string(role)); err != nil {
			return nil, err
		}
		if len(grants) == 0 {
			return nil, fmt.Errorf("role %q must grant at least one permission", role)
		}

		rolePermissions := make(map[Permission]struct{}, len(grants))
		for _, permission := range grants {
			if err := validateIdentifier("permission", string(permission)); err != nil {
				return nil, err
			}
			rolePermissions[permission] = struct{}{}
		}
		permissions[role] = rolePermissions
	}

	return &Evaluator{permissions: permissions}, nil
}

// Allows reports whether any assigned role grants permission.
func (e *Evaluator) Allows(roles []Role, permission Permission) bool {
	if e == nil || permission == "" {
		return false
	}
	for _, role := range roles {
		if _, ok := e.permissions[role][permission]; ok {
			return true
		}
	}
	return false
}

// Permissions returns the sorted, deduplicated permissions granted by roles.
func (e *Evaluator) Permissions(roles []Role) []Permission {
	if e == nil {
		return []Permission{}
	}
	set := make(map[Permission]struct{})
	for _, role := range roles {
		for permission := range e.permissions[role] {
			set[permission] = struct{}{}
		}
	}
	result := make([]Permission, 0, len(set))
	for permission := range set {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func validateIdentifier(kind, value string) error {
	if value != strings.TrimSpace(value) || !identifierPattern.MatchString(value) {
		return fmt.Errorf("invalid %s identifier %q", kind, value)
	}
	return nil
}
