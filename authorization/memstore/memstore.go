// Package memstore provides an in-memory authorization.Store for tests and
// small products. It is safe for concurrent use.
package memstore

import (
	"context"
	"sync"

	"github.com/manovaspace/orbit-auth/authorization"
)

// Store keeps roles per organization and assignments per organization and
// subject. It copies values in and out so callers cannot mutate stored state.
type Store struct {
	mu          sync.RWMutex
	roles       map[string][]authorization.RoleDef
	assignments map[string]map[string][]authorization.Assignment
}

var _ authorization.Store = (*Store)(nil)

// New returns an empty Store.
func New() *Store {
	return &Store{
		roles:       make(map[string][]authorization.RoleDef),
		assignments: make(map[string]map[string][]authorization.Assignment),
	}
}

// SetRoles replaces every role in the organization.
func (s *Store) SetRoles(orgID string, roles []authorization.RoleDef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		s.roles = make(map[string][]authorization.RoleDef)
	}
	s.roles[orgID] = cloneRoles(roles)
}

// AddRole inserts a role, replacing the role with the same key if it exists.
func (s *Store) AddRole(orgID string, role authorization.RoleDef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		s.roles = make(map[string][]authorization.RoleDef)
	}

	roles := s.roles[orgID]
	replaced := false
	for i := range roles {
		if roles[i].Key == role.Key {
			roles[i] = cloneRole(role)
			replaced = true
			break
		}
	}
	if !replaced {
		roles = append(roles, cloneRole(role))
	}
	s.roles[orgID] = roles
}

// SetAssignments replaces the assignments for one subject in one
// organization. The OrgID and Subject fields of each assignment are set from
// the arguments, so callers cannot accidentally file it under another tenant.
func (s *Store) SetAssignments(orgID, subject string, assignments []authorization.Assignment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([]authorization.Assignment, len(assignments))
	for i, assignment := range assignments {
		assignment.OrgID = orgID
		assignment.Subject = subject
		copied[i] = assignment
	}
	if s.assignments == nil {
		s.assignments = make(map[string]map[string][]authorization.Assignment)
	}
	if s.assignments[orgID] == nil {
		s.assignments[orgID] = make(map[string][]authorization.Assignment)
	}
	s.assignments[orgID][subject] = copied
}

// AddAssignment appends one assignment. A duplicate is harmless; Check and
// AllowedScopes only care whether a matching grant exists.
func (s *Store) AddAssignment(assignment authorization.Assignment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.assignments == nil {
		s.assignments = make(map[string]map[string][]authorization.Assignment)
	}
	if s.assignments[assignment.OrgID] == nil {
		s.assignments[assignment.OrgID] = make(map[string][]authorization.Assignment)
	}
	s.assignments[assignment.OrgID][assignment.Subject] = append(
		s.assignments[assignment.OrgID][assignment.Subject],
		assignment,
	)
}

// Roles returns a copy of the organization's roles.
func (s *Store) Roles(ctx context.Context, orgID string) ([]authorization.RoleDef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRoles(s.roles[orgID]), nil
}

// Assignments returns a copy of one subject's assignments in an organization.
func (s *Store) Assignments(ctx context.Context, orgID, subject string) ([]authorization.Assignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	assignments := s.assignments[orgID][subject]
	result := make([]authorization.Assignment, len(assignments))
	copy(result, assignments)
	return result, nil
}

func cloneRoles(roles []authorization.RoleDef) []authorization.RoleDef {
	result := make([]authorization.RoleDef, len(roles))
	for i, role := range roles {
		result[i] = cloneRole(role)
	}
	return result
}

func cloneRole(role authorization.RoleDef) authorization.RoleDef {
	role.Permissions = append([]authorization.Permission(nil), role.Permissions...)
	return role
}
