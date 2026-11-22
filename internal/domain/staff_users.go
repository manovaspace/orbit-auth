package domain

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrAccountNotActive refuses an account that is not active: a disabled
	// account, or a sign-up that is still pending. Token issuance maps it to the
	// caller's generic error. Staff disable and enable map a pending account to
	// FailedPrecondition.
	ErrAccountNotActive = errors.New("account is not active")
	// ErrInvalidUserStatus reports a status filter outside active, disabled and
	// signup_pending.
	ErrInvalidUserStatus = errors.New("invalid user status")
	// ErrAuditFilterRequired refuses an audit listing with neither an
	// organization nor a user to filter by.
	ErrAuditFilterRequired = errors.New("org_id or user_id is required")
)

// StaffUser is a product account as staff see it. It never carries a password
// hash, a token or a hash of either: HasPassword and GoogleLinked report only
// whether those exist.
type StaffUser struct {
	ID              string
	Email           string
	Mobile          string
	Username        string
	DisplayName     string
	Status          string
	EmailVerified   bool
	MobileVerified  bool
	HasPassword     bool
	GoogleLinked    bool
	CreatedAt       time.Time
	LastLoginAt     *time.Time
	DisabledAt      *time.Time
	DisabledReason  string
	MembershipCount int
}

// StaffUserMembership is one organization membership of an account, with the
// organization fields staff need to read it.
type StaffUserMembership struct {
	OrgID     string
	OrgName   string
	OrgSlug   string
	OrgStatus OrganizationStatus
	Role      MembershipRole
	Status    MembershipStatus
	JoinedAt  time.Time
}

// StaffUserCursor is the keyset position after the last account of a page.
type StaffUserCursor struct {
	CreatedAt time.Time
	ID        string
}

// StaffUserListQuery selects one page of accounts, oldest first. Query matches
// the email, mobile number, username or display name case-insensitively. Status
// is "" or one of the account states. After is nil for the first page.
type StaffUserListQuery struct {
	Query  string
	Status string
	After  *StaffUserCursor
	Limit  int
}

// StaffUserStore is the staff view of accounts, their memberships and the audit
// trail. The writes DisableUser and EnableUser run their change, the revocation
// of refresh tokens, the audit row and the lifecycle event in one transaction.
type StaffUserStore interface {
	// ListUsersForStaff returns one keyset page of accounts.
	ListUsersForStaff(ctx context.Context, query StaffUserListQuery) ([]StaffUser, error)
	// GetUserForStaff returns one account with its memberships, or
	// ErrUserNotFound.
	GetUserForStaff(ctx context.Context, userID string) (StaffUser, []StaffUserMembership, error)
	// DisableUser makes an active account disabled, revokes every live refresh
	// token of the account, and writes user.disabled and auth.user.disabled. A
	// disabled account is unchanged (changed=false, nothing written). A pending
	// account is ErrAccountNotActive and an unknown one ErrUserNotFound.
	DisableUser(ctx context.Context, userID string, by StaffActor) (changed bool, err error)
	// EnableUser makes a disabled account active again and writes user.enabled
	// and auth.user.enabled. An active account is unchanged. A pending account is
	// ErrAccountNotActive and an unknown one ErrUserNotFound.
	EnableUser(ctx context.Context, userID string, by StaffActor) (changed bool, err error)
	// ListAuditEvents returns one page of audit rows, newest first.
	ListAuditEvents(ctx context.Context, query AuditEventQuery) ([]AuditEvent, error)
}
