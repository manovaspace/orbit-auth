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
	OccurredAt time.Time `json:"occurred_at"`
}

// MembershipAddedPayload is the body of auth.membership.added. Status is the
// role-free membership lifecycle state.
type MembershipAddedPayload struct {
	OrgID      string           `json:"org_id"`
	UserID     string           `json:"user_id"`
	Status     MembershipStatus `json:"status"`
	OccurredAt time.Time        `json:"occurred_at"`
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
func NewOrgCreatedEvent(orgID, name string, at time.Time) Event {
	return Event{
		Subject:    SubjectOrgCreated,
		Payload:    OrgCreatedPayload{OrgID: orgID, Name: name, OccurredAt: at},
		OccurredAt: at,
	}
}

// NewMembershipAddedEvent builds auth.membership.added.
func NewMembershipAddedEvent(orgID, userID string, status MembershipStatus, at time.Time) Event {
	return Event{
		Subject:    SubjectMembershipAdded,
		Payload:    MembershipAddedPayload{OrgID: orgID, UserID: userID, Status: status, OccurredAt: at},
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
