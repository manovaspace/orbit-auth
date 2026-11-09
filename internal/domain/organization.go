package domain

import (
	"errors"
	"strings"
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
	// ErrOrgSuspended refuses selecting into, or refreshing into, an
	// organization that staff have suspended.
	ErrOrgSuspended = errors.New("organization suspended")
	// ErrStaffSubjectRequired reports a staff operation without the staff
	// subject that the gateway sends.
	ErrStaffSubjectRequired = errors.New("staff subject required")
	// ErrStaffReasonRequired reports a staff write without a reason.
	ErrStaffReasonRequired = errors.New("staff reason required")
	// ErrIdempotencyKeyRequired reports a provisioning call without its key.
	ErrIdempotencyKeyRequired = errors.New("idempotency_key required")
	// ErrIdempotencyConflict reports an idempotency key that already created an
	// organization for a different name or owner target.
	ErrIdempotencyConflict = errors.New("idempotency_key was used for a different name or owner")
	// ErrInvalidOrgStatus reports a status filter outside active and suspended.
	ErrInvalidOrgStatus = errors.New("invalid organization status")
	// ErrInvalidPageToken reports a page token this service did not issue.
	ErrInvalidPageToken = errors.New("invalid page_token")
	// ErrInvalidPageSize reports a page size outside 1 to 100.
	ErrInvalidPageSize = errors.New("page_size must be between 1 and 100")
)

// OrganizationStatus is the staff-controlled state of an organization. The
// values are part of the storage contract pinned by migration 014.
type OrganizationStatus string

const (
	// OrgStatusActive is the default. Members can select the organization.
	OrgStatusActive OrganizationStatus = "active"
	// OrgStatusSuspended refuses SelectOrg and refresh into the organization.
	OrgStatusSuspended OrganizationStatus = "suspended"
)

// StaffActor identifies the staff member behind a staff write. Subject comes
// from the gateway, Reason from the caller's request metadata. Staff are never
// organization members, so a StaffActor carries no membership.
type StaffActor struct {
	Subject string
	Reason  string
}

// Validate reports the missing field of a staff actor.
func (a StaffActor) Validate() error {
	if strings.TrimSpace(a.Subject) == "" {
		return ErrStaffSubjectRequired
	}
	if strings.TrimSpace(a.Reason) == "" {
		return ErrStaffReasonRequired
	}
	return nil
}

// StaffOrgSummary is an organization with the counts staff see. MemberCount
// counts every membership; OwnerUserIDs are the owners whose membership is
// active.
type StaffOrgSummary struct {
	Organization Organization
	MemberCount  int
	OwnerUserIDs []string
}

// StaffOrgCursor is the keyset position after the last organization of a page.
type StaffOrgCursor struct {
	CreatedAt time.Time
	ID        string
}

// StaffOrgListQuery selects one page of organizations. Query matches the name or
// the slug case-insensitively, and an empty Status lists both states. After is
// nil for the first page. Limit is the number of rows to return.
type StaffOrgListQuery struct {
	Query  string
	Status OrganizationStatus
	After  *StaffOrgCursor
	Limit  int
}

// OrganizationProvision is what a provisioning idempotency key created: the
// organization and the owner target its owner invitation was issued to. A repeat
// must name the same organization name and the same owner target.
type OrganizationProvision struct {
	Organization Organization
	OwnerTarget  InviteTarget
}

// Organization is a tenant. Users reach it through memberships.
type Organization struct {
	ID   string
	Name string
	// Slug is the organization's current host label, unique across all
	// organizations and across time.
	Slug string
	// Status is active or suspended. A suspended organization keeps its
	// members and memberships; only selecting into it is refused.
	Status OrganizationStatus
	// SuspendedAt and SuspendedReason are set while the organization is
	// suspended.
	SuspendedAt     *time.Time
	SuspendedReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
