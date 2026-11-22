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
	// InviteExpired is derived, never stored: a pending invite whose expires_at
	// is at or before the clock. See EffectiveInviteStatus.
	InviteExpired InviteStatus = "expired"

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
	// ErrInvalidEmail reports a missing or malformed invitation address. It is
	// also the answer for an invitation target with neither field set, because
	// email is the default target.
	ErrInvalidEmail = errors.New("invalid email")
	// ErrInvalidPhone reports an invitation phone number that is not E.164.
	ErrInvalidPhone = errors.New("invalid phone")
	// ErrInvalidInviteTarget reports an invitation that names both an email
	// address and a phone number. An invitation targets exactly one of them.
	ErrInvalidInviteTarget = errors.New("invite target must be exactly one of email or phone")
	// ErrInviteSignupDisabled is returned while AUTH_INVITE_SIGNUP is off and an
	// invitation would create an account. Nothing is written.
	ErrInviteSignupDisabled = errors.New("invite sign-up is disabled")
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

// EffectiveInviteStatus returns the status an invitation shows at now. A
// pending invitation whose expiry is not after now (now is not before
// expiresAt) is expired; any other status is returned unchanged. The stored
// status is never rewritten, and the database check admits only pending,
// accepted and revoked.
func EffectiveInviteStatus(status InviteStatus, expiresAt, now time.Time) InviteStatus {
	if status == InvitePending && !now.Before(expiresAt) {
		return InviteExpired
	}
	return status
}

// InviteTarget is the one recipient of an invitation: an email address or an
// E.164 phone number, never both.
type InviteTarget struct {
	Email string
	Phone string
}

// NormalizeInviteTarget validates target and returns it in stored form. Exactly
// one field must be set. Email is normalised with NormalizeEmail and must pass
// IsEmail; phone is trimmed and must pass IsE164. A target with neither field
// set is ErrInvalidEmail, the same answer a missing address always gave.
func NormalizeInviteTarget(target InviteTarget) (InviteTarget, error) {
	email := NormalizeEmail(target.Email)
	phone := strings.TrimSpace(target.Phone)
	switch {
	case email != "" && phone != "":
		return InviteTarget{}, ErrInvalidInviteTarget
	case phone != "":
		if !IsE164(phone) {
			return InviteTarget{}, ErrInvalidPhone
		}
		return InviteTarget{Phone: phone}, nil
	default:
		if !IsEmail(email) {
			return InviteTarget{}, ErrInvalidEmail
		}
		return InviteTarget{Email: email}, nil
	}
}

// HashInviteToken returns the SHA-256 hex digest stored in
// organization_invites. The raw invitation token is never persisted; it only
// travels in the org_invite notification.
func HashInviteToken(token string) string {
	return HashRefreshToken(token)
}

// Invite is one pending or past invitation to join an organization. Exactly one
// of Email and Phone is set. Role is the organization tier the invitee receives
// on acceptance. TokenHash is used only when an invite is created or resolved
// from a token; listings leave it empty.
type Invite struct {
	ID        string
	OrgID     string
	Email     string
	Phone     string
	Role      MembershipRole
	TokenHash string
	// InviterID is the user who issued the invitation. It is empty for an
	// invitation issued by staff, which sets InviterStaffSubject instead.
	InviterID string
	// InviterStaffSubject is the staff subject that issued the invitation.
	InviterStaffSubject string
	Status              InviteStatus
	ExpiresAt           time.Time
	AcceptedAt          *time.Time
	CreatedAt           time.Time
}

// InviteNotification is the org_invite email orbit-notifications renders. The
// application never sends email itself.
type InviteNotification struct {
	Recipient   string
	OrgName     string
	InviterName string
	AcceptToken string
	// AcceptURL is the link the email shows: the resolved accept_url with the
	// raw token in its fragment. Empty when the inviter gave no accept_url. It is
	// a bearer link and is never logged.
	AcceptURL     string
	ExpiresAt     time.Time
	Locale        string
	CorrelationID string
}

// InvitePreview is what the holder of an invitation token may see before
// accepting it. It never carries the recipient's email address or phone number.
type InvitePreview struct {
	OrgID     string
	OrgName   string
	OrgSlug   string
	Role      MembershipRole
	Status    InviteStatus
	ExpiresAt time.Time
	// Channel is "email" or "phone": the kind of target the invitation names.
	Channel string
	// AccountExists is true when an active account already uses the invited
	// phone number or email. The preview never says whose account it is.
	AccountExists bool
}
