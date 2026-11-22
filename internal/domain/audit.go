package domain

import "time"

// Audit actor kinds. They are the storage contract of audit_events.actor_kind
// pinned by migration 017.
const (
	AuditActorUser   = "user"
	AuditActorStaff  = "staff"
	AuditActorSystem = "system"
)

// Audit actions. Each one is written in the same transaction as the change it
// records. The names are the storage contract of audit_events.action.
const (
	AuditInviteCreated         = "invite.created"
	AuditInviteRevoked         = "invite.revoked"
	AuditInviteAccepted        = "invite.accepted"
	AuditMembershipAdded       = "membership.added"
	AuditMembershipSuspended   = "membership.suspended"
	AuditMembershipReactivated = "membership.reactivated"
	AuditMembershipRemoved     = "membership.removed"
	AuditMembershipRoleChanged = "membership.role_changed"
	AuditOrgCreated            = "org.created"
	AuditOrgSuspended          = "org.suspended"
	AuditOrgReactivated        = "org.reactivated"
	AuditOrgSlugChanged        = "org.slug_changed"
	AuditUserCreated           = "user.created"
	AuditUserDisabled          = "user.disabled"
	AuditUserEnabled           = "user.enabled"
)

// AuditActor says who made a change. ID is the user id for a user actor and the
// staff subject for a staff actor; it is empty for a system actor. Reason is set
// only for a staff actor and carries the reason the staff member gave.
type AuditActor struct {
	Kind   string
	ID     string
	Reason string
}

// UserAuditActor is the actor of a change a user made, or a user's own change
// such as a sign-up.
func UserAuditActor(userID string) AuditActor {
	return AuditActor{Kind: AuditActorUser, ID: userID}
}

// StaffAuditActor is the actor of a staff write: the staff subject and reason.
func StaffAuditActor(by StaffActor) AuditActor {
	return AuditActor{Kind: AuditActorStaff, ID: by.Subject, Reason: by.Reason}
}

// SystemAuditActor is the actor of a change no person made, such as demo
// seeding or the rollback of an invitation whose email could not be sent.
func SystemAuditActor() AuditActor {
	return AuditActor{Kind: AuditActorSystem}
}

// MembershipAuditActor maps a membership actor to an audit actor. A zero caller
// is a system change.
func MembershipAuditActor(by MembershipActor) AuditActor {
	if by.CallerID == "" {
		return SystemAuditActor()
	}
	return UserAuditActor(by.CallerID)
}

// AuditEntry is one row of the audit trail. Details holds only identifiers and
// tier, slug or status values. It never holds an email address, a mobile number,
// a password, a token or a hash.
type AuditEntry struct {
	Action        string
	Actor         AuditActor
	OrgID         string
	SubjectUserID string
	InviteID      string
	Details       map[string]any
	OccurredAt    time.Time
}

// AuditEvent is one stored audit row as staff read it. Details is the JSON
// object as text.
type AuditEvent struct {
	ID            int64
	OccurredAt    time.Time
	Action        string
	ActorKind     string
	ActorID       string
	OrgID         string
	SubjectUserID string
	InviteID      string
	Reason        string
	Details       string
}

// AuditEventCursor is the keyset position after the last row of a page.
type AuditEventCursor struct {
	ID int64
}

// AuditEventQuery selects one page of audit rows, newest first. At least one of
// OrgID and SubjectUserID is set. Limit is the number of rows to return.
type AuditEventQuery struct {
	OrgID         string
	SubjectUserID string
	After         *AuditEventCursor
	Limit         int
}
