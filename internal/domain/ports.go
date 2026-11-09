package domain

import (
	"context"
	"errors"
	"time"
)

// ErrRefreshTokenNotFound is returned by RefreshTokenStore.Consume for a
// missing, revoked or expired token.
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// OTPStore persists one-time challenges.
type OTPStore interface {
	UpsertChallenge(ctx context.Context, ch OTPChallenge) error
	GetLatestChallenge(ctx context.Context, identifier, channel string) (OTPChallenge, error)
	IncrementAttempts(ctx context.Context, id string) error
	ConsumeChallenge(ctx context.Context, id string) error
	DeleteExpiredChallenges(ctx context.Context) error
}

// UserStore reads and creates users.
type UserStore interface {
	FindOrCreateByEmail(ctx context.Context, email string) (User, error)
	FindOrCreateByMobile(ctx context.Context, mobile string) (User, error)
	FindByEmail(ctx context.Context, email string) (User, error)
	FindByMobile(ctx context.Context, mobile string) (User, error)
	FindByID(ctx context.Context, userID string) (User, error)
	UpdateLastLogin(ctx context.Context, userID string) error
	UpsertDemoUser(ctx context.Context, email, mobile, passwordHash, displayName string) error
	// MarkMobileVerified records that a mobile OTP for userID succeeded. An
	// existing verification time is kept.
	MarkMobileVerified(ctx context.Context, userID string, verifiedAt time.Time) error
}

// SignupStore persists self-serve sign-up accounts. CreateSignupUser writes
// the account and, in the same transaction, either creates the organization it
// owns or accepts the invitation that proved the address. VerifySignup
// activates a pending account after the email OTP succeeded.
type SignupStore interface {
	CreateSignupUser(ctx context.Context, user User, org *Organization, invite *Invite, acceptedAt time.Time) (created bool, err error)
	// ReissuePendingSignup replaces the credentials of an existing unverified
	// sign-up. When challenge is non-nil it also invalidates the address's
	// older challenges and writes the new one; when invite is non-nil it
	// activates the account and accepts that invitation instead. It returns
	// false when the address is not a pending sign-up.
	ReissuePendingSignup(ctx context.Context, user User, challenge *OTPChallenge, invite *Invite, acceptedAt time.Time) (userID string, reissued bool, err error)
	VerifySignup(ctx context.Context, userID string, verifiedAt time.Time) error
	PurgeUnverifiedSignups(ctx context.Context, before time.Time) (int64, error)
	// CreatePhoneInviteSignup writes an active account for the invite's phone
	// number with user.Mobile and password hash, and in the same transaction
	// emits user.created, accepts the invitation and adds the membership with
	// the invited role. The mobile number is not marked verified. It returns
	// created=false when the number already belongs to an account, and
	// ErrInviteNotFound when the invitation is no longer pending. Either way
	// nothing is written.
	CreatePhoneInviteSignup(ctx context.Context, user User, invite Invite, acceptedAt time.Time) (created bool, err error)
}

// RefreshTokenStore persists refresh tokens by their hash. A stored token
// remembers the organization selected when the pair was issued; orgID is empty
// when no organization was active.
type RefreshTokenStore interface {
	Store(ctx context.Context, userID, orgID, tokenHash string, expiresAt time.Time) error
	FindValid(ctx context.Context, tokenHash string) (userID, orgID string, err error)
	// Consume atomically revokes a valid token and returns its owner, so two
	// concurrent redemptions of one token cannot both succeed. A missing,
	// revoked or expired token returns ErrRefreshTokenNotFound.
	Consume(ctx context.Context, tokenHash string) (userID, orgID string, err error)
	Revoke(ctx context.Context, tokenHash string) error
}

// APITokenStore persists API tokens.
type APITokenStore interface {
	Create(ctx context.Context, t APIToken) error
	ListByUser(ctx context.Context, userID string) ([]APITokenInfo, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error
	FindByPrefix(ctx context.Context, prefix string) (APIToken, error)
}

// OrgStore persists organizations and their memberships. Every membership
// operation is scoped by both organization and user.
type OrgStore interface {
	// CreateOrg writes the organization and the creator's active owner
	// membership in the same transaction.
	CreateOrg(ctx context.Context, org Organization, creatorID string) error
	GetOrg(ctx context.Context, orgID string) (Organization, error)
	// SlugAvailable reports whether no organization holds or has held slug.
	SlugAvailable(ctx context.Context, slug string) (bool, error)
	// SetOrgSlug makes slug the organization's current slug and retires the
	// old one (it stays reserved for this organization and still resolves).
	// It reruns the policy for the actor under the organization lock, returns
	// ErrSlugTaken when another organization holds or has held the slug, and
	// writes auth.org.slug_changed. Setting the current slug is a no-op.
	SetOrgSlug(ctx context.Context, orgID, slug string, by MembershipActor) error
	// SetOrgSlugByStaff is SetOrgSlug for a staff write. Staff are not members,
	// so no membership is checked; auth.org.slug_changed names the staff subject
	// as its actor and carries the reason.
	SetOrgSlugByStaff(ctx context.Context, orgID, slug string, by StaffActor) error
	// ResolveSlug finds the organization that holds or once held slug. The
	// returned organization carries its current slug, so a caller can redirect
	// from a retired one. An unknown slug is ErrSlugNotFound.
	ResolveSlug(ctx context.Context, slug string) (Organization, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]Organization, error)
	// ListMembers returns every membership of orgID with its lifecycle status.
	ListMembers(ctx context.Context, orgID string) ([]Membership, error)
	AddMember(ctx context.Context, orgID, userID string, status MembershipStatus, role MembershipRole) error
	// ListOrganizationsForStaff returns one page of organizations with their
	// member counts and active owners, oldest first.
	ListOrganizationsForStaff(ctx context.Context, query StaffOrgListQuery) ([]StaffOrgSummary, error)
	// ProvisionedOrganization returns what an idempotency key created: the
	// organization and the owner target it was provisioned for. found is false
	// when the key has not been used.
	ProvisionedOrganization(ctx context.Context, idempotencyKey string) (provision OrganizationProvision, found bool, err error)
	// ProvisionOrganization writes an organization with no members, its slug
	// registry row and organization.created, the owner invitation, and the
	// idempotency key with its owner target, in one transaction. With a
	// non-empty idempotencyKey that already created an organization, nothing is
	// written and the recorded provision is returned with created=false.
	// created=true means the invitation was written too; the caller decides
	// about notification.
	ProvisionOrganization(ctx context.Context, org Organization, owner Invite, idempotencyKey string) (provision OrganizationProvision, created bool, err error)
	// SuspendOrganization suspends an organization and emits
	// auth.org.suspended, unless it is already suspended.
	SuspendOrganization(ctx context.Context, orgID string, by StaffActor) error
	// ReactivateOrganization lifts a suspension and emits
	// auth.org.reactivated, unless the organization is active.
	ReactivateOrganization(ctx context.Context, orgID string, by StaffActor) error
	UpdateMemberStatus(ctx context.Context, orgID, userID string, status MembershipStatus, by MembershipActor) error
	// UpdateMemberRole changes a member's administration tier. It returns
	// ErrLastOwner when the change would leave no active owner, and records
	// actorID in auth.membership.role_changed.
	UpdateMemberRole(ctx context.Context, orgID, userID string, role MembershipRole, actorID string) error
	// UpdateMemberStatus and RemoveMember also return ErrLastOwner when the
	// member is the last active owner. With a non-zero actor they run
	// AuthorizeMembershipAction again under the organization lock and return
	// ErrForbidden when the actor no longer has the authority.
	RemoveMember(ctx context.Context, orgID, userID string, by MembershipActor) error
	GetMembership(ctx context.Context, orgID, userID string) (Membership, error)
}

// InviteStore persists organization invitations. Every operation is scoped by
// organization, and listings never select the token hash.
type InviteStore interface {
	// CreateInvite revokes any pending invite for the same organization and
	// email address, then writes the new pending invite in the same
	// transaction.
	CreateInvite(ctx context.Context, invite Invite) error
	FindInviteByTokenHash(ctx context.Context, tokenHash string) (Invite, error)
	ListInvites(ctx context.Context, orgID string) ([]Invite, error)
	// RevokeInvite revokes one pending invite or returns ErrInviteNotFound.
	RevokeInvite(ctx context.Context, orgID, inviteID string) error
	// AcceptInvite marks one pending invite accepted and makes the user an
	// active member with role in the same transaction. A user who is already an
	// active member keeps their current role. It returns ErrInviteNotFound when
	// the invite is no longer pending.
	AcceptInvite(ctx context.Context, inviteID, orgID, userID string, role MembershipRole, acceptedAt time.Time) error
}

// FeatureFlagStore reads the platform feature flags.
type FeatureFlagStore interface {
	ListFeatureFlags(ctx context.Context) ([]FeatureFlag, error)
}

// Notifier sends an OTP, a sign-up verification or an organization invitation
// through orbit-notifications. The service never talks to a mail or SMS
// provider directly.
type Notifier interface {
	SendOTP(ctx context.Context, channel, recipient, code, correlationID string) error
	SendSignupVerification(ctx context.Context, recipient, code string, expiresInMinutes int, locale, correlationID string) error
	SendOrgInvite(ctx context.Context, invite InviteNotification) error
}

// Flags evaluates boolean feature flags.
type Flags interface {
	Bool(ctx context.Context, flag string, defaultVal bool) bool
}

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// IDGenerator supplies UUID strings for challenges, users, refresh tokens and
// API tokens.
type IDGenerator interface {
	NewID() string
}

// TokenSigner signs an RS256 access token for subject, optionally carrying an
// active organization claim.
type TokenSigner interface {
	SignAccessToken(subject string, expires time.Time) (string, error)
	SignAccessTokenWithOrg(subject, orgID string, expires time.Time) (string, error)
}

// Hasher hashes and verifies passwords and API-token secrets. The production
// adapter is bcrypt at bcrypt.DefaultCost.
type Hasher interface {
	Hash(secret string) (string, error)
	Compare(hash, secret string) bool
}

// CodeGenerator generates a numeric OTP code.
type CodeGenerator interface {
	NewCode() (string, error)
}

// SecretGenerator generates an opaque API-token secret.
type SecretGenerator interface {
	NewSecret() (string, error)
}
