package domain

import (
	"errors"
	"time"
)

// MembershipStatus is the lifecycle state of a user inside one organization.
// The values are part of the storage contract pinned by migration 004.
type MembershipStatus string

const (
	MembershipActive    MembershipStatus = "active"
	MembershipInvited   MembershipStatus = "invited"
	MembershipSuspended MembershipStatus = "suspended"
)

// MembershipRole is a member's administration tier inside one organization.
// It decides who may change memberships and invites; it is not a product
// role, and products keep their own roles and permissions (ADR-045 D1). The
// values are part of the storage contract pinned by migration 010.
type MembershipRole string

const (
	// RoleOwner administers the organization, including other admins and
	// owners. An organization keeps at least one active owner.
	RoleOwner MembershipRole = "owner"
	// RoleAdmin manages plain members and invitations.
	RoleAdmin MembershipRole = "admin"
	// RoleMember has no administration rights.
	RoleMember MembershipRole = "member"
)

// ParseMembershipRole validates a role string.
func ParseMembershipRole(value string) (MembershipRole, error) {
	switch role := MembershipRole(value); role {
	case RoleOwner, RoleAdmin, RoleMember:
		return role, nil
	default:
		return "", ErrInvalidRole
	}
}

var (
	// ErrForbidden reports a caller without the organization-administration
	// authority an operation needs.
	ErrForbidden = errors.New("organization administration required")
	// ErrLastOwner refuses a change that would leave an organization without
	// an active owner.
	ErrLastOwner = errors.New("organization must keep at least one active owner")
	// ErrInvalidRole reports a membership role outside owner, admin and member.
	ErrInvalidRole = errors.New("invalid membership role")
	// ErrOrgNotFound reports an organization that does not exist.
	ErrOrgNotFound = errors.New("organization not found")
	// ErrNotMember reports a user without a membership in the organization.
	ErrNotMember = errors.New("not a member of the organization")
	// ErrUserNotFound reports a user id that does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrLastActiveMember refuses a change that would leave an organization
	// without any active member.
	ErrLastActiveMember = errors.New("organization must keep at least one active member")
	// ErrInvalidOrgName reports a missing or blank organization name.
	ErrInvalidOrgName = errors.New("organization name is required")
)

// Organization is a tenant. Users reach it through memberships.
type Organization struct {
	ID   string
	Name string
	// Slug is the organization's current host label, unique across all
	// organizations and across time.
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Membership links one user to one organization.
type Membership struct {
	OrgID     string
	UserID    string
	Status    MembershipStatus
	Role      MembershipRole
	CreatedAt time.Time
	UpdatedAt time.Time
}
