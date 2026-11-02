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

var (
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
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Membership links one user to one organization.
type Membership struct {
	OrgID     string
	UserID    string
	Status    MembershipStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}
