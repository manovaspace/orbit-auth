package domain

import (
	"context"
	"time"
)

// Lifecycle event subjects. A subject is the three-segment
// {domain}.{entity}.{action} shape the message bus validates. The event
// version is not part of the subject: it travels in the envelope metadata as
// EventVersion.
const (
	SubjectUserCreated         = "auth.user.created"
	SubjectOrgCreated          = "auth.org.created"
	SubjectMembershipAdded     = "auth.membership.added"
	SubjectMembershipRemoved   = "auth.membership.removed"
	SubjectMembershipSuspended = "auth.membership.suspended"
	// SubjectMembershipRoleChanged is published when a member's
	// administration tier changes (owner, admin or member).
	SubjectMembershipRoleChanged = "auth.membership.role_changed"
	// SubjectOrgSlugChanged is published when an owner renames the slug of an
	// organization, which changes the address of its tenant host.
	SubjectOrgSlugChanged = "auth.org.slug_changed"
	// SubjectOrgSuspended is published when staff suspend an organization.
	SubjectOrgSuspended = "auth.org.suspended"
	// SubjectOrgReactivated is published when staff lift a suspension.
	SubjectOrgReactivated = "auth.org.reactivated"
)

// EventSource is the envelope metadata source of every event this service
// publishes.
const EventSource = "orbit-auth"

// EventVersion is the envelope metadata version. Adding an optional field keeps
// this value; a breaking change needs a new version and a coordinated consumer
// migration.
const EventVersion = "1"

// UserCreatedPayload is the body of auth.user.created. It deliberately carries
// no email, mobile, password or token.
type UserCreatedPayload struct {
	UserID     string    `json:"user_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// OrgCreatedPayload is the body of auth.org.created. It carries the name so a
// product can provision the organization without a synchronous read.
type OrgCreatedPayload struct {
	OrgID      string    `json:"org_id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	OccurredAt time.Time `json:"occurred_at"`
}

// OrgSlugChangedPayload is the body of auth.org.slug_changed. ActorID is the
// caller who made the change: the owner, or the staff subject for a staff
// change. Reason is the reason staff gave, set only for a staff change.
type OrgSlugChangedPayload struct {
	OrgID        string    `json:"org_id"`
	Slug         string    `json:"slug"`
	PreviousSlug string    `json:"previous_slug"`
	ActorID      string    `json:"actor_id"`
	Reason       string    `json:"reason,omitempty"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// OrgStatusChangedPayload is the body of auth.org.suspended and
// auth.org.reactivated. StaffSubject and Reason identify the staff write. The
// organization is changed by staff, so no member is an actor.
type OrgStatusChangedPayload struct {
	OrgID        string    `json:"org_id"`
	StaffSubject string    `json:"staff_subject"`
	Reason       string    `json:"reason"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// MembershipAddedPayload is the body of auth.membership.added. Status is the
// membership lifecycle state and Role the organization administration tier
// (owner, admin or member), never a product role. Consumers that predate Role
// ignore the field.
type MembershipAddedPayload struct {
	OrgID      string           `json:"org_id"`
	UserID     string           `json:"user_id"`
	Status     MembershipStatus `json:"status"`
	Role       MembershipRole   `json:"role"`
	OccurredAt time.Time        `json:"occurred_at"`
}

// MembershipRoleChangedPayload is the body of auth.membership.role_changed.
// ActorID is the member who made the change.
type MembershipRoleChangedPayload struct {
	OrgID        string         `json:"org_id"`
	UserID       string         `json:"user_id"`
	Role         MembershipRole `json:"role"`
	PreviousRole MembershipRole `json:"previous_role"`
	ActorID      string         `json:"actor_id"`
	OccurredAt   time.Time      `json:"occurred_at"`
}

// MembershipRemovedPayload is the body of auth.membership.removed. Status is
// the lifecycle state the membership had immediately before it was deleted.
type MembershipRemovedPayload struct {
	OrgID      string           `json:"org_id"`
	UserID     string           `json:"user_id"`
	Status     MembershipStatus `json:"status"`
	OccurredAt time.Time        `json:"occurred_at"`
}

// MembershipSuspendedPayload is the body of auth.membership.suspended.
type MembershipSuspendedPayload struct {
	OrgID      string           `json:"org_id"`
	UserID     string           `json:"user_id"`
	Status     MembershipStatus `json:"status"`
	OccurredAt time.Time        `json:"occurred_at"`
}

// Event is one lifecycle fact to record in the same transaction as the change
// that caused it. The transaction-aware writer is the EventSink port.
type Event struct {
	Subject    string
	Payload    any
	OccurredAt time.Time
}

// EventSink records one lifecycle event. An implementation must write it in the
// same database transaction as the change that produced it. The Postgres adapter
// implements this port on top of its transactional outbox.
type EventSink interface {
	Emit(ctx context.Context, event Event) error
}

// NewUserCreatedEvent builds auth.user.created.
func NewUserCreatedEvent(userID string, at time.Time) Event {
	return Event{
		Subject:    SubjectUserCreated,
		Payload:    UserCreatedPayload{UserID: userID, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewOrgCreatedEvent builds auth.org.created.
func NewOrgCreatedEvent(orgID, name, slug string, at time.Time) Event {
	return Event{
		Subject:    SubjectOrgCreated,
		Payload:    OrgCreatedPayload{OrgID: orgID, Name: name, Slug: slug, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewMembershipAddedEvent builds auth.membership.added.
func NewMembershipAddedEvent(orgID, userID string, status MembershipStatus, role MembershipRole, at time.Time) Event {
	return Event{
		Subject:    SubjectMembershipAdded,
		Payload:    MembershipAddedPayload{OrgID: orgID, UserID: userID, Status: status, Role: role, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewMembershipRemovedEvent builds auth.membership.removed.
func NewMembershipRemovedEvent(orgID, userID string, status MembershipStatus, at time.Time) Event {
	return Event{
		Subject:    SubjectMembershipRemoved,
		Payload:    MembershipRemovedPayload{OrgID: orgID, UserID: userID, Status: status, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewMembershipSuspendedEvent builds auth.membership.suspended.
func NewMembershipSuspendedEvent(orgID, userID string, status MembershipStatus, at time.Time) Event {
	return Event{
		Subject:    SubjectMembershipSuspended,
		Payload:    MembershipSuspendedPayload{OrgID: orgID, UserID: userID, Status: status, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewMembershipRoleChangedEvent builds auth.membership.role_changed.
func NewMembershipRoleChangedEvent(orgID, userID string, role, previous MembershipRole, actorID string, at time.Time) Event {
	return Event{
		Subject: SubjectMembershipRoleChanged,
		Payload: MembershipRoleChangedPayload{
			OrgID: orgID, UserID: userID, Role: role, PreviousRole: previous, ActorID: actorID, OccurredAt: at,
		},
		OccurredAt: at,
	}
}

// NewOrgSuspendedEvent builds auth.org.suspended.
func NewOrgSuspendedEvent(orgID string, by StaffActor, at time.Time) Event {
	return Event{
		Subject:    SubjectOrgSuspended,
		Payload:    OrgStatusChangedPayload{OrgID: orgID, StaffSubject: by.Subject, Reason: by.Reason, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewOrgReactivatedEvent builds auth.org.reactivated.
func NewOrgReactivatedEvent(orgID string, by StaffActor, at time.Time) Event {
	return Event{
		Subject:    SubjectOrgReactivated,
		Payload:    OrgStatusChangedPayload{OrgID: orgID, StaffSubject: by.Subject, Reason: by.Reason, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewOrgSlugChangedEvent builds auth.org.slug_changed. reason is empty for an
// owner's change and set for a staff change.
func NewOrgSlugChangedEvent(orgID, slug, previous, actorID, reason string, at time.Time) Event {
	return Event{
		Subject:    SubjectOrgSlugChanged,
		Payload:    OrgSlugChangedPayload{OrgID: orgID, Slug: slug, PreviousSlug: previous, ActorID: actorID, Reason: reason, OccurredAt: at},
		OccurredAt: at,
	}
}
