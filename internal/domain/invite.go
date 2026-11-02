package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// InviteStatus is the lifecycle state of one organization invitation. The
// values are part of the storage contract pinned by migration 006.
type InviteStatus string

const (
	InvitePending  InviteStatus = "pending"
	InviteAccepted InviteStatus = "accepted"
	InviteRevoked  InviteStatus = "revoked"

	// InviteTTL is how long an invitation stays acceptable.
	InviteTTL = 7 * 24 * time.Hour
)

var (
	// ErrInviteNotFound reports an invite id or token that does not exist.
	// Callers that accept a token turn this into the generic invalid-invite
	// error so the reason is not revealed.
	ErrInviteNotFound = errors.New("invite not found")
	// ErrInvalidInvite is the single error for an expired, revoked, already
	// accepted, unknown or wrong-recipient invitation.
	ErrInvalidInvite = errors.New("invalid invite")
	// ErrInvalidEmail reports a missing or malformed invitation address.
	ErrInvalidEmail = errors.New("invalid email")
)

var inviteEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail trims and lower-cases an email address. Stored invitation and
// user addresses are compared in this form.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IsEmail reports whether value is a plausible email address. It does not
// prove deliverability; orbit-notifications owns delivery.
func IsEmail(value string) bool {
	return inviteEmailPattern.MatchString(value)
}

// HashInviteToken returns the SHA-256 hex digest stored in
// organization_invites. The raw invitation token is never persisted; it only
// travels in the org_invite notification.
func HashInviteToken(token string) string {
	return HashRefreshToken(token)
}

// Invite is one pending or past invitation to join an organization. TokenHash
// is used only when an invite is created or resolved from a token; listings
// leave it empty.
type Invite struct {
	ID         string
	OrgID      string
	Email      string
	TokenHash  string
	InviterID  string
	Status     InviteStatus
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	CreatedAt  time.Time
}

// InviteNotification is the org_invite email orbit-notifications renders. The
// application never sends email itself.
type InviteNotification struct {
	Recipient     string
	OrgName       string
	InviterName   string
	AcceptToken   string
	ExpiresAt     time.Time
	Locale        string
	CorrelationID string
}
